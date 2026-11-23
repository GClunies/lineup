// Package rex wraps the rex CLI (API revision 7). Every call runs rex as a
// subprocess with exec and never through a shell.
package rex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

type Client struct{ Bin string } // "rex" when empty

type Session struct{ ID, Label string }

type Window struct{ SessionID, WindowID, BlockID string }

type WindowView struct {
	ID, Label string
	BlockIDs  []string
}

type SessionView struct {
	ID, Label string
	Windows   []WindowView
}

type StatusRecord struct{ State, Kind, Msg, App string }

func (c *Client) bin() string {
	if c.Bin == "" {
		return "rex"
	}
	return c.Bin
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("rex %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

var targetNotFound = regexp.MustCompile(`target "[^"]*" not found`)

// IsNotFound reports whether err says that a rex target is missing. A missing
// rex binary or a down server is not a missing target.
func IsNotFound(err error) bool {
	return err != nil && targetNotFound.MatchString(err.Error())
}

func (c *Client) Sessions(ctx context.Context) ([]Session, error) {
	out, err := c.run(ctx, "session", "ls", "--json")
	if err != nil {
		return nil, err
	}
	var v struct {
		Sessions []struct {
			ID    string `json:"session_id"`
			Label string `json:"label"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, err
	}
	var ss []Session
	for _, s := range v.Sessions {
		ss = append(ss, Session{ID: s.ID, Label: s.Label})
	}
	return ss, nil
}

// sessionMu keeps two Task setups for one Repo from both missing the session
// and both creating it.
var sessionMu sync.Mutex

// EnsureSession returns the session with label. A new session starts with a
// shell window, which this call closes so that the session holds only Task tabs.
func (c *Client) EnsureSession(ctx context.Context, label string) (string, error) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	ss, err := c.Sessions(ctx)
	if err != nil {
		return "", err
	}
	for _, s := range ss {
		if s.Label == label {
			return s.ID, nil
		}
	}
	out, err := c.run(ctx, "session", "new", label, "--json")
	if err != nil {
		return "", err
	}
	var v struct {
		ID      string `json:"session_id"`
		Windows []struct {
			ID string `json:"window_id"`
		} `json:"initial_windows"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", err
	}
	for _, w := range v.Windows {
		if err := c.CloseWindow(ctx, v.ID, w.ID); err != nil {
			return "", err
		}
	}
	return v.ID, nil
}

// RenameSession gives the session the label, unless another session has it.
// Rex lets two sessions share a label, and EnsureSession would then find
// either one. A missing session is no error.
func (c *Client) RenameSession(ctx context.Context, session, label string) error {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	ss, err := c.Sessions(ctx)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(ss, func(s Session) bool { return s.Label == label }) ||
		!slices.ContainsFunc(ss, func(s Session) bool { return s.ID == session }) {
		return nil
	}
	_, err = c.run(ctx, "session", "rename", session, label)
	return err
}

// NewWindow opens a tab that runs argv. With keep, the tab stays open after
// argv exits, so another block can take its place.
func (c *Client) NewWindow(ctx context.Context, session, label, cwd string, env map[string]string, argv []string, keep bool) (Window, error) {
	args := []string{"window", "new", "-s", session, label, "--focus=false", "--json"}
	if keep {
		args = append(args, "--keep-open")
	}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	out, err := c.run(ctx, append(append(args, "--"), withEnv(env, argv)...)...)
	if err != nil {
		return Window{}, err
	}
	var v struct {
		WindowID string   `json:"window_id"`
		BlockIDs []string `json:"block_ids"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return Window{}, err
	}
	if len(v.BlockIDs) == 0 {
		return Window{}, errors.New("rex window new returned no block")
	}
	return Window{SessionID: session, WindowID: v.WindowID, BlockID: v.BlockIDs[0]}, nil
}

// withEnv sets env for argv. Rex starts argv through a login shell that runs no
// direnv, so env is set by prefixing argv with env K=V.
func withEnv(env map[string]string, argv []string) []string {
	if len(env) == 0 {
		return argv
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []string{"env"}
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return append(out, argv...)
}

// Split opens argv in a new block labeled side to the right of block, with
// half of the tab, and keeps the focus where it is. It returns the new
// block's ID.
func (c *Client) Split(ctx context.Context, session, block, cwd string, argv []string) (string, error) {
	return c.split(ctx, session, block, cwd, "side", nil, argv)
}

// NewBlock opens argv in a new block of block's tab, as Split does. The new
// block gets the label of a new tab's block.
func (c *Client) NewBlock(ctx context.Context, session, block, cwd string, env map[string]string, argv []string) (string, error) {
	return c.split(ctx, session, block, cwd, "", env, argv)
}

func (c *Client) split(ctx context.Context, session, block, cwd, label string, env map[string]string, argv []string) (string, error) {
	args := []string{"split", "-s", session, "-b", block, "--split", "right", "--ratio", "50", "--focus=false", "--json"}
	if label != "" {
		args = append(args, "--label", label)
	}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	out, err := c.run(ctx, append(append(args, "--"), withEnv(env, argv)...)...)
	if err != nil {
		return "", err
	}
	var v struct {
		BlockIDs []string `json:"block_ids"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", err
	}
	if len(v.BlockIDs) == 0 {
		return "", errors.New("rex split returned no block")
	}
	return v.BlockIDs[0], nil
}

// CloseBlock closes one block. Its tab stays while it holds another block.
func (c *Client) CloseBlock(ctx context.Context, session, block string) error {
	_, err := c.run(ctx, "block", "close", "-s", session, block)
	if IsNotFound(err) {
		return nil
	}
	return err
}

func (c *Client) CloseWindow(ctx context.Context, session, windowID string) error {
	_, err := c.run(ctx, "window", "close", "-s", session, windowID)
	if IsNotFound(err) {
		return nil
	}
	return err
}

func (c *Client) DestroySession(ctx context.Context, session string) error {
	_, err := c.run(ctx, "session", "kill", session)
	if IsNotFound(err) {
		return nil
	}
	return err
}

func (c *Client) View(ctx context.Context, session string) (SessionView, error) {
	out, err := c.run(ctx, "session", "inspect", "--json", session)
	if err != nil {
		return SessionView{}, err
	}
	var v struct {
		ID      string `json:"session_id"`
		Label   string `json:"label"`
		Windows []struct {
			ID     string `json:"window_id"`
			Label  string `json:"label"`
			Layers []struct {
				Blocks []struct {
					ID string `json:"block_id"`
				} `json:"blocks"`
			} `json:"layers"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return SessionView{}, err
	}
	sv := SessionView{ID: v.ID, Label: v.Label}
	for _, w := range v.Windows {
		wv := WindowView{ID: w.ID, Label: w.Label}
		for _, l := range w.Layers {
			for _, b := range l.Blocks {
				wv.BlockIDs = append(wv.BlockIDs, b.ID)
			}
		}
		sv.Windows = append(sv.Windows, wv)
	}
	return sv, nil
}

func (c *Client) ProgramStatus(ctx context.Context, session, block string) ([]StatusRecord, error) {
	out, err := c.run(ctx, "block", "call", "-s", session, "-b", block, "program_status")
	if err != nil {
		return nil, err
	}
	var v struct {
		Records []struct {
			State, Kind, Msg, App string
		} `json:"records"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, err
	}
	var rs []StatusRecord
	for _, r := range v.Records {
		rs = append(rs, StatusRecord(r))
	}
	return rs, nil
}

// loginShell is the program that Rex starts in a plain tab on macOS.
const loginShell = "/usr/bin/login"

// IdleShell reports whether a block holds only the login shell of a plain tab,
// with no program in its foreground. Rex restores every block that way after a
// restart, so a program that lineup started there no longer runs.
func (c *Client) IdleShell(ctx context.Context, session, block string) (bool, error) {
	out, err := c.run(ctx, "block", "call", "-s", session, "-b", block, "process")
	if err != nil {
		return false, err
	}
	return idleShell(out)
}

// idleShell reads the record of the process method. Ancestors lists the
// foreground program's parents up to the block's own child, so the shell
// itself in the foreground has one ancestor, the login shell.
func idleShell(out []byte) (bool, error) {
	var v struct {
		Child struct {
			Path string `json:"path"`
		} `json:"child"`
		Ancestors []json.RawMessage `json:"ancestors"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return false, err
	}
	return v.Child.Path == loginShell && len(v.Ancestors) <= 1, nil
}

// LastExit returns the exit code of a block's command after it exits in a block
// that Rex keeps open. exited is false while a program runs in the block.
func (c *Client) LastExit(ctx context.Context, session, block string) (code int, exited bool, err error) {
	out, err := c.run(ctx, "block", "call", "-s", session, "-b", block, "process")
	if err != nil {
		return 0, false, err
	}
	return lastExit(out)
}

// lastExit reads the record of the process method. A kept block whose command
// exited has a last exit and no child.
func lastExit(out []byte) (int, bool, error) {
	var v struct {
		LastExit *struct {
			ExitCode int `json:"exit_code"`
		} `json:"last_exit"`
		Child *struct{} `json:"child"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return 0, false, err
	}
	if v.LastExit == nil || v.Child != nil {
		return 0, false, nil
	}
	return v.LastExit.ExitCode, true, nil
}

// Type writes each part to the block, then presses Enter. Claude reads text and
// Enter in one write as a paste, so a short pause follows each write. After a
// failure, Type clears the input box, so a retry does not append to a half-typed
// prompt.
func (c *Client) Type(ctx context.Context, session, block string, parts ...string) error {
	err := c.typeParts(ctx, session, block, append(parts, "\r"))
	if err != nil {
		c.clearInput(ctx, session, block, parts)
	}
	return err
}

func (c *Client) typeParts(ctx context.Context, session, block string, parts []string) error {
	for _, part := range parts {
		if err := c.write(ctx, session, block, part); err != nil {
			return err
		}
		select {
		case <-time.After(150 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Claude's Ctrl+U deletes the cursor's line, then the line break before it, so
// each line of parts needs two presses.
func (c *Client) clearInput(ctx context.Context, session, block string, parts []string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for range 2 * (1 + strings.Count(strings.Join(parts, ""), "\n")) {
		if c.write(ctx, session, block, "\x15") != nil {
			return
		}
	}
}

func (c *Client) write(ctx context.Context, session, block, data string) error {
	args, err := json.Marshal(map[string]string{"data": data})
	if err != nil {
		return err
	}
	_, err = c.run(ctx, "block", "call", "-s", session, "-b", block, "write", string(args))
	return err
}

func (c *Client) Capture(ctx context.Context, session, block string) (string, error) {
	out, err := c.run(ctx, "capture", "-s", session, "-b", block)
	return string(out), err
}

func (c *Client) Focus(ctx context.Context, session, block string) error {
	_, err := c.run(ctx, "focus", "-s", session, block)
	return err
}

// Show makes the Rex app display the window. Focus changes only the session's
// own focus, which the app shows only while it displays that session. The app
// takes actions only with Remote Control on in its settings.
func (c *Client) Show(ctx context.Context, session, window string) error {
	_, err := c.run(ctx, "do", "session.select", "session_id="+session, "window_id="+window)
	return err
}

// ServerInstance returns the id of the running Rex server. It changes when
// the server restarts, and then every Worker is gone.
func (c *Client) ServerInstance(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "server", "status", "--json")
	if err != nil {
		return "", err
	}
	var v struct {
		ID string `json:"instance_id"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", err
	}
	if v.ID == "" {
		return "", errors.New("rex server status has no instance_id")
	}
	return v.ID, nil
}
