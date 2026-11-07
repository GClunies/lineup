package main

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/localissues"
	"github.com/gclunies/lineup/internal/rex"
	"github.com/gclunies/lineup/internal/scheduler"
	"github.com/gclunies/lineup/internal/store"
	"github.com/gclunies/lineup/internal/worktree"
)

// xdgStateHome is the XDG state folder: XDG_STATE_HOME, else ~/.local/state.
func xdgStateHome() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state")
}

func configPath() string {
	if p := os.Getenv("LINEUP_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "lineup", "config.toml")
}

// adHocSession refuses the Hub's name: the Scheduler removes a session after
// its last Task, and that would take the Board and both agents with it.
func adHocSession(name string) (string, error) {
	if name == hubSession {
		return "", fmt.Errorf("ad_hoc_session can't be %q, the Hub's session", hubSession)
	}
	return name, nil
}

// requireTools checks the hard dependencies before the Scheduler starts.
func requireTools() (wtBin string, err error) {
	if wtBin, err = worktree.Require(); err != nil {
		return "", err
	}
	for _, bin := range []string{"rex", "gh"} {
		if _, err := exec.LookPath(bin); err != nil {
			return "", fmt.Errorf("%s is required and is not on PATH", bin)
		}
	}
	return wtBin, nil
}

const rexWatchEvery = 5 * time.Second

func serve() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	unlock, err := lockServe(stateDir())
	if err != nil {
		return err
	}
	defer unlock()
	if err := trimLog(filepath.Join(stateDir(), "scheduler.log"), logKeep); err != nil {
		log.Warn("trim log", "err", err)
	}
	wtBin, err := requireTools()
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath())
	if err != nil {
		return err
	}
	adHoc, err := adHocSession(cfg.AdHocSession())
	if err != nil {
		return err
	}
	defaults, err := cfg.Resolve(stateDir(), nil)
	if err != nil {
		return err
	}
	choicesPath, err := config.DefaultRepoChoicesPath()
	if err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(stateDir(), "state.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	gh := &githubPort{c: &github.Client{}}
	if gh.viewer, err = gh.c.Viewer(ctx); err != nil {
		return fmt.Errorf("gh: %w", err)
	}
	rc := &rex.Client{}
	instance, err := rc.ServerInstance(ctx)
	if err != nil {
		return fmt.Errorf("rex: %w", err)
	}
	tracker := trackerMux{
		scheduler.KindLinear: &trackerPort{cfg: cfg},
		scheduler.KindGitHub: githubTracker{c: gh.c, labels: cfg.GitHubLabels()},
		scheduler.KindLocal:  localTracker{s: localissues.Store{Dir: cfg.LocalIssuesDir()}},
	}
	s, err := scheduler.New(scheduler.Deps{
		Rex: rexPort{rc}, GitHub: gh, Worktrees: worktreePort{wtBin},
		Harness: harnessPort{cfg: cfg, tracker: tracker}, Tracker: tracker,
		Config:       configPort{cfg: cfg, choices: config.NewRepoChoices(choicesPath)},
		Store:        st,
		DefaultCap:   defaults.WIPCap,
		PollInterval: defaults.PollInterval,
		AdHocSession: adHoc,
		Queues:       reviewQueues(cfg),
		StateHome:    xdgStateHome(),
		BoardCache:   filepath.Join(stateDir(), "board.json"),
		Log:          log,
	})
	if err != nil {
		return err
	}
	errs := make(chan error, 3)
	go func() { errs <- s.Serve(ctx, socketPath()) }()
	go func() { errs <- s.Run(ctx, rexEvents(ctx, rc, log.Warn)) }()
	go func() { errs <- watchRexServer(ctx, rexWatchEvery, instance, rc.ServerInstance, log.Warn) }()
	log.Info("scheduler ready", "socket", socketPath(), "viewer", gh.viewer)
	err = <-errs
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// login signs in to a Tracker: lineup login [linear [workspace] | github].
// With no argument it asks which.
func login(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, "Sign in to which Tracker, linear or github? ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		args = []string{strings.TrimSpace(line)}
	}
	switch args[0] {
	case "linear":
		if len(args) > 2 {
			return errors.New("usage: lineup login linear [workspace]")
		}
		return loginLinear(args[1:], out)
	case "github":
		if len(args) > 1 {
			return errors.New("usage: lineup login github")
		}
		return loginGitHub(context.Background(), &github.Client{}, stateDir(), out)
	}
	return fmt.Errorf("unknown Tracker %q: use lineup login linear [workspace] or lineup login github", args[0])
}

func loginLinear(args []string, out io.Writer) error {
	var want string
	if len(args) == 1 {
		want = args[0]
	}
	cfg, err := config.Load(configPath())
	if err != nil {
		return err
	}
	ws, err := linear.Login(context.Background(), linear.UI{
		Status: func(s string) { fmt.Fprintln(out, s) },
		Link: func(url string, opened bool) {
			if opened {
				fmt.Fprintln(out, "Opened your browser to sign in. If it did not open, visit:")
			} else {
				fmt.Fprintln(out, "Visit this address to sign in:")
			}
			fmt.Fprintln(out, url)
		},
	}, want, cfg.LinearClientID(want))
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Signed in to Linear workspace %s (%s)\n", ws.Name, ws.URLKey)
	return nil
}

// loginGitHub makes GitHub a Tracker for the account that gh is signed in to.
func loginGitHub(ctx context.Context, gh *github.Client, dir string, out io.Writer) error {
	login, err := gh.Viewer(ctx)
	if err != nil {
		return fmt.Errorf("gh is not signed in (%w): run gh auth login, then lineup login github", err)
	}
	if err := github.SaveTracker(dir, login); err != nil {
		return err
	}
	fmt.Fprintf(out, "GitHub is a Tracker for %s\n", login)
	return nil
}

// logout stops GitHub being a Tracker. gh stays signed in, because lineup needs it for PRs.
// linearLogout is linear.Logout, which tests replace.
type linearLogout func(ctx context.Context, which string, local bool) ([]string, error)

func logout(args []string, out io.Writer, logoutLinear linearLogout) error {
	const use = "usage: lineup logout linear [workspace] [--local], or lineup logout github"
	if len(args) == 0 {
		return errors.New(use)
	}
	switch {
	case args[0] == "linear":
		var which string
		var local bool
		for _, a := range args[1:] {
			switch {
			case a == "--local":
				local = true
			case which == "" && !strings.HasPrefix(a, "-"):
				which = a
			default:
				return errors.New(use)
			}
		}
		done, err := logoutLinear(context.Background(), which, local)
		if errors.Is(err, linear.ErrNotSignedIn) {
			fmt.Fprintln(out, "Not signed in to Linear")
			return nil
		}
		for _, name := range done {
			fmt.Fprintf(out, "Signed out of Linear workspace %s\n", name)
		}
		return err
	case args[0] != "github" || len(args) != 1:
		return errors.New(use)
	}
	was, err := github.ForgetTracker(stateDir())
	if err != nil {
		return err
	}
	if !was {
		fmt.Fprintln(out, "GitHub was not a Tracker")
		return nil
	}
	fmt.Fprintln(out, "GitHub is no longer a Tracker. gh stays signed in for PRs.")
	return nil
}
