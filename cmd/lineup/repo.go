package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/scheduler"
	"github.com/gclunies/lineup/internal/worktree"
)

// This file holds lineup repo detect and lineup repo init. lineup add
// refuses a build Task in a Repo with no [repos."owner/repo"] table, and the
// Orchestrator sets the Repo up with these commands.

const (
	repoDetectUsage = "lineup repo detect <owner>/<repo> --json"
	repoInitUsage   = "lineup repo init <owner>/<repo> [key=value ...] | --defaults"
)

// repoEnv is what lineup repo reaches outside the config file. The tests
// replace each func.
type repoEnv struct {
	configPath string
	trackers   func() (trackersOn, error)
	facts      func(ctx context.Context, repo string) (github.RepoFacts, error)
	hooks      func(ctx context.Context, checkout string) ([]worktree.Hook, error)
	teams      func(ctx context.Context) ([]linearTeam, error)
	teamKeys   func(ctx context.Context) ([]string, error)
	restart    func() error
}

func realRepoEnv() repoEnv {
	return repoEnv{
		configPath: configPath(),
		trackers:   currentTrackers,
		facts:      (&github.Client{}).RepoFacts,
		hooks: func(ctx context.Context, checkout string) ([]worktree.Hook, error) {
			bin, err := worktree.Require()
			if err != nil {
				return nil, err
			}
			return worktree.Hooks(ctx, bin, checkout)
		},
		teams:    linearTeams,
		teamKeys: linearTeamKeys,
		restart:  restartScheduler,
	}
}

// detected is what lineup repo detect prints: the detected default of each
// setup question, and what the Orchestrator needs to ask them.
type detected struct {
	Repo       string   `json:"repo"`
	Checkout   string   `json:"checkout"`
	TrackersOn []string `json:"trackers_on"`
	// Tracker is "" when no Tracker is detected.
	Tracker    string     `json:"tracker"`
	ReviewBot  *reviewBot `json:"review_bot"`
	LandingBar landingBar `json:"landing_bar"`
	// Prefix is the suggested prefix of the Repo's own IDs, or "".
	Prefix   string        `json:"prefix"`
	Teams    []linearTeam  `json:"linear_teams,omitempty"`
	Worktree worktreeHooks `json:"worktree"`
	// Init are the key=value arguments that lineup repo init --defaults
	// writes: each detected value that no lower layer sets.
	Init []string `json:"init"`
}

type reviewBot struct {
	Name            string `json:"name"`
	MinReviewScore  int    `json:"min_review_score"`
	MaxReviewCycles int    `json:"max_review_cycles"`
}

type landingBar struct {
	Checks    string `json:"checks"`
	Threads   string `json:"threads"`
	Approvals string `json:"approvals"`
}

type linearTeam struct {
	Workspace string `json:"workspace"`
	Key       string `json:"key"`
	Name      string `json:"name"`
	// Repos are the keys of the other tables whose teams list holds the team.
	Repos []string `json:"repos,omitempty"`
}

type worktreeHooks struct {
	Hooks []wtHook `json:"hooks"`
	// SetUp is true when worktrunk has a hook that readies a new worktree.
	SetUp bool `json:"set_up"`
	// Error is why wt could not list the hooks.
	Error string `json:"error,omitempty"`
}

type wtHook struct {
	Type   string `json:"type"`
	Source string `json:"source"`
	Name   string `json:"name"`
}

// detectDefaults picks the detected default of the Tracker, the review
// bot, and the Landing bar. A value that [defaults] or the owner/* table
// sets comes first, then what GitHub says. facts is nil for a Repo with no
// GitHub origin.
func detectDefaults(in config.Inherited, facts *github.RepoFacts) detected {
	d := detected{Tracker: in.Tracker, Init: []string{}}
	if d.Tracker == "" && facts != nil && facts.HasIssues {
		d.Tracker = scheduler.KindGitHub
		d.Init = append(d.Init, "tracker="+d.Tracker)
	}
	bot := in.ReviewBot.Name
	if bot == "" && facts != nil && len(facts.ReviewBots) > 0 {
		bot = facts.ReviewBots[0]
		d.Init = append(d.Init, "review_bot.name="+bot)
	}
	if bot != "" {
		d.ReviewBot = &reviewBot{Name: bot, MinReviewScore: github.ReviewBots[bot].TopScore, MaxReviewCycles: config.DefaultReviewCycles}
		if n := in.ReviewBot.MinReviewScore; n != nil {
			d.ReviewBot.MinReviewScore = *n
		}
		if n := in.ReviewBot.MaxReviewCycles; n != nil {
			d.ReviewBot.MaxReviewCycles = *n
		}
	}
	def := config.DefaultBar()
	d.LandingBar = landingBar{
		Checks:    cmp.Or(in.LandingBar.Checks, def.Checks.String()),
		Threads:   cmp.Or(in.LandingBar.Threads, def.Threads.String()),
		Approvals: cmp.Or(in.LandingBar.Approvals, def.Approvals.String()),
	}
	if in.LandingBar.Checks == "" && facts != nil && len(facts.RequiredChecks) == 0 {
		d.LandingBar.Checks = lifecycle.AllChecks.String()
		d.Init = append(d.Init, "landing_bar.checks="+d.LandingBar.Checks)
	}
	return d
}

// readyHooks keeps the hooks that ready a new worktree: each pre-start
// hook, and each hook that copies the ignored files.
func readyHooks(hooks []worktree.Hook, err error) worktreeHooks {
	w := worktreeHooks{Hooks: []wtHook{}}
	if err != nil {
		w.Error = err.Error()
		return w
	}
	for _, h := range hooks {
		if h.Type == "pre-start" || strings.Contains(h.Template, "copy-ignored") {
			w.Hooks = append(w.Hooks, wtHook{Type: h.Type, Source: h.Source, Name: h.Name})
		}
	}
	w.SetUp = len(w.Hooks) > 0
	return w
}

func (on trackersOn) kinds() []string {
	out := []string{}
	for _, t := range []struct {
		on   bool
		kind string
	}{{on.linear, scheduler.KindLinear}, {on.github, scheduler.KindGitHub}, {on.local, scheduler.KindLocal}} {
		if t.on {
			out = append(out, t.kind)
		}
	}
	return out
}

// linearTeams lists the teams of each signed-in Linear workspace.
func linearTeams(ctx context.Context) ([]linearTeam, error) {
	wss, err := linear.Workspaces()
	if err != nil {
		return nil, err
	}
	var out []linearTeam
	for _, ws := range wss {
		teams, err := linear.NewClient(ws).Teams(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ws.Name, err)
		}
		for _, t := range teams {
			out = append(out, linearTeam{Workspace: ws.URLKey, Key: t.Key, Name: t.Name})
		}
	}
	return out, nil
}

// detect reads the Repo's setup defaults. Only a Repo with a GitHub origin
// asks GitHub.
func detect(ctx context.Context, env repoEnv, key string) (detected, error) {
	g, err := config.Load(env.configPath)
	if err != nil {
		return detected{}, err
	}
	checkout, ok := g.CheckoutFor(key)
	if !ok {
		if c := g.KeyClash(key); c != "" {
			return detected{}, errors.New(c)
		}
		return detected{}, fmt.Errorf("no checkout of %s below the roots", key)
	}
	on, err := env.trackers()
	if err != nil {
		return detected{}, err
	}
	var facts *github.RepoFacts
	if owner, _, _ := strings.Cut(key, "/"); owner != "local" {
		f, err := env.facts(ctx, key)
		if err != nil {
			return detected{}, err
		}
		facts = &f
	}
	d := detectDefaults(g.Inherited(key), facts)
	d.Repo, d.Checkout, d.TrackersOn = key, checkout, on.kinds()
	if on.linear {
		teams, err := env.teams(ctx)
		if err != nil {
			return detected{}, err
		}
		for _, t := range teams {
			t.Repos = slices.DeleteFunc(g.ReposForTeam(t.Key), func(k string) bool { return strings.EqualFold(k, key) })
			d.Teams = append(d.Teams, t)
		}
	}
	keys := make([]string, len(d.Teams))
	for i, t := range d.Teams {
		keys[i] = t.Key
	}
	if d.Prefix = g.SuggestPrefix(key, keys); d.Prefix != "" {
		d.Init = append(d.Init, "prefix="+d.Prefix)
	}
	d.Worktree = readyHooks(env.hooks(ctx, checkout))
	return d, nil
}

func cmdRepo(ctx context.Context, args []string, out io.Writer, env repoEnv) error {
	if len(args) < 2 {
		return fmt.Errorf("%w: %s\n       %s", errUsage, repoDetectUsage, repoInitUsage)
	}
	key, rest := args[1], args[2:]
	switch args[0] {
	case "detect":
		if !slices.Equal(rest, []string{"--json"}) {
			return fmt.Errorf("%w: %s", errUsage, repoDetectUsage)
		}
		d, err := detect(ctx, env, key)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(d)
	case "init":
		if slices.Contains(rest, "--defaults") {
			if len(rest) != 1 {
				return fmt.Errorf("%w: pass --defaults or key=value arguments, not both: %s", errUsage, repoInitUsage)
			}
			d, err := detect(ctx, env, key)
			if err != nil {
				return err
			}
			rest = d.Init
		}
		table, err := config.InitRepo(env.configPath, key, rest, func() []string {
			return liveTeamKeys(ctx, env.teamKeys, out, "repo init")
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Added to %s:\n\n%s\n", env.configPath, table)
		if err := env.restart(); err != nil {
			return fmt.Errorf("the table is in the file, but the Scheduler did not restart, so it does not apply yet: %w", err)
		}
		fmt.Fprintln(out, restarted)
		return nil
	}
	return fmt.Errorf("%w: %s\n       %s", errUsage, repoDetectUsage, repoInitUsage)
}
