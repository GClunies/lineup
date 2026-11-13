package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/gclunies/lineup/internal/config"
)

const serviceLabel = "com.github.gclunies.lineup.scheduler"

// service describes the launchd user agent that runs the Scheduler.
type service struct {
	Label   string
	Program []string
	Env     map[string]string
	Log     string
}

// serviceEnv lists the variables that the Scheduler reads. The agent gets
// no other variable, so a Rex block's REX_* variables never leak into it.
var serviceEnv = []string{"PATH", "LINEUP_STATE_DIR", "LINEUP_CONFIG", "XDG_STATE_HOME"}

func currentService() (service, error) {
	self, err := os.Executable()
	if err != nil {
		return service{}, err
	}
	env := map[string]string{}
	for _, k := range serviceEnv {
		if v := os.Getenv(k); v != "" || k == "PATH" {
			env[k] = v
		}
	}
	return service{
		Label:   serviceLabel,
		Program: []string{self, "serve"},
		Env:     env,
		Log:     filepath.Join(stateDir(), "scheduler.log"),
	}, nil
}

var plistTemplate = template.Must(template.New("plist").Funcs(template.FuncMap{
	"x": func(s string) (string, error) {
		var b bytes.Buffer
		err := xml.EscapeText(&b, []byte(s))
		return b.String(), err
	},
	"keys": func(m map[string]string) []string {
		ks := make([]string, 0, len(m))
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return ks
	},
}).Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{x .Label}}</string>
	<key>ProgramArguments</key>
	<array>
{{- range .Program}}
		<string>{{x .}}</string>
{{- end}}
	</array>
	<key>EnvironmentVariables</key>
	<dict>
{{- $env := .Env}}{{range keys .Env}}
		<key>{{x .}}</key>
		<string>{{x (index $env .)}}</string>
{{- end}}
	</dict>
	<key>StandardOutPath</key>
	<string>{{x .Log}}</string>
	<key>StandardErrorPath</key>
	<string>{{x .Log}}</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
`))

func (s service) plist() []byte {
	var b bytes.Buffer
	if err := plistTemplate.Execute(&b, s); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// launchctl runs one launchctl command. Tests replace it.
type launchctl func(args ...string) error

func realLaunchctl(args ...string) error {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// manager installs and controls a service for one user.
type manager struct {
	svc      service
	config   string // the global config file that the Scheduler loads
	plistDir string
	uid      int
	ctl      launchctl
	sleep    func(time.Duration)
}

func newManager() (manager, error) {
	svc, err := currentService()
	if err != nil {
		return manager{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return manager{}, err
	}
	return manager{svc: svc, config: configPath(), plistDir: filepath.Join(home, "Library", "LaunchAgents"), uid: os.Getuid(), ctl: realLaunchctl, sleep: time.Sleep}, nil
}

func (m manager) domain() string { return fmt.Sprintf("gui/%d", m.uid) }
func (m manager) target() string { return m.domain() + "/" + m.svc.Label }
func (m manager) plistPath() string {
	return filepath.Join(m.plistDir, m.svc.Label+".plist")
}
func (m manager) loaded() bool { return m.ctl("print", m.target()) == nil }

// unloaded waits for launchd to remove the agent. bootout returns before it
// does, and a bootstrap until then fails with "Operation already in progress".
func (m manager) unloaded() error {
	for range 100 {
		if !m.loaded() {
			return nil
		}
		m.sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("launchd still has %s loaded 10s after bootout", m.svc.Label)
}

// ensure writes the plist when it changed and loads the agent. A second
// call with nothing changed does nothing. started says it bootstrapped the
// agent, which starts the Scheduler.
func (m manager) ensure() (started bool, err error) {
	want := m.svc.plist()
	have, err := os.ReadFile(m.plistPath())
	changed := err != nil || !bytes.Equal(have, want)
	if changed {
		if err := os.MkdirAll(m.plistDir, 0o755); err != nil {
			return false, err
		}
		if err := os.MkdirAll(filepath.Dir(m.svc.Log), 0o700); err != nil {
			return false, err
		}
		if err := os.WriteFile(m.plistPath(), want, 0o644); err != nil {
			return false, err
		}
	}
	loaded := m.loaded()
	if loaded && changed {
		if err := m.ctl("bootout", m.target()); err != nil {
			return false, err
		}
		if err := m.unloaded(); err != nil {
			return false, err
		}
		loaded = false
	}
	if !loaded {
		return true, m.ctl("bootstrap", m.domain(), m.plistPath())
	}
	return false, nil
}

// start loads the agent when it is not loaded. It leaves a loaded agent as
// it is, because each shell has its own PATH and a reload restarts the
// Scheduler in the middle of its work.
func (m manager) start() error {
	if m.loaded() {
		return nil
	}
	_, err := m.ensure()
	return err
}

// restart loads the config first, so that a bad edit leaves the running
// Scheduler as it is.
func (m manager) restart() error {
	if _, err := config.Load(m.config); err != nil {
		return fmt.Errorf("the Scheduler keeps running on the old config: %w", err)
	}
	started, err := m.ensure()
	if err != nil || started { // a kickstart now would kill the new Scheduler as it starts
		return err
	}
	return m.ctl("kickstart", "-k", m.target())
}

// stop unloads the agent. It reports false when the agent was not loaded.
func (m manager) stop() (wasLoaded bool, err error) {
	if !m.loaded() {
		return false, nil
	}
	return true, m.ctl("bootout", m.target())
}

func startService() error {
	m, err := newManager()
	if err != nil {
		return err
	}
	return m.start()
}

func cmdRestart() error {
	m, err := newManager()
	if err != nil {
		return err
	}
	if err := m.restart(); err != nil {
		return err
	}
	if err := waitListening(socketPath(), 30*time.Second); err != nil {
		return err
	}
	fmt.Println("The Scheduler restarted.")
	return nil
}

// waitListening returns once the Scheduler accepts a connection on sock.
// launchd starts the Scheduler and returns at once, and the Scheduler needs
// some seconds before it listens. A command run before then fails.
func waitListening(sock string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		c, err := net.DialTimeout("unix", sock, time.Second)
		if err == nil {
			return c.Close()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the Scheduler did not listen on %s within %v: read %s", sock, limit, filepath.Join(stateDir(), "scheduler.log"))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func cmdStop() error {
	m, err := newManager()
	if err != nil {
		return err
	}
	was, err := m.stop()
	if err != nil {
		return err
	}
	if !was {
		fmt.Println("The Scheduler is already stopped.")
		return nil
	}
	fmt.Println("The Scheduler stopped. Run lineup restart to start it.")
	return nil
}

func cmdLogs() error {
	path := filepath.Join(stateDir(), "scheduler.log")
	if _, err := os.Stat(path); err != nil {
		return errors.New("no Scheduler log yet; run lineup restart")
	}
	cmd := exec.Command("tail", "-n", "100", "-F", path)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
