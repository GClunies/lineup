package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/localissues"
	"github.com/gclunies/lineup/internal/scheduler"
)

// clientCommands are the commands that call the Scheduler. The Orchestrator uses them.
var clientCommands = map[string]func(c scheduler.Client, args []string, out io.Writer) error{
	"add":     cmdAdd,
	"start":   cmdStart,
	"review":  cmdReview,
	"reviews": cmdReviews,
	"ls":      cmdLs,
	"watch":   cmdWatch,
	"show":    cmdShow,
	"steer":   cmdSteer,
	"ask":     cmdAsk,
	"set":     cmdSet,
	"cap":     cmdCap,
	"rerun":   cmdRerun,
	"open":    cmdOpen,
	"merge":   simple("merge"),
	"close":   simple("close"),
	"archive": simple("archive"),
}

var errUsage = errors.New("usage")

func parse(name, usage string, args []string, setup func(*flag.FlagSet)) (*flag.FlagSet, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintf(fs.Output(), "usage: lineup %s %s\n", name, usage) }
	if setup != nil {
		setup(fs)
	}
	return fs, fs.Parse(args)
}

func cmdAdd(c scheduler.Client, args []string, out io.Writer) error {
	var repo string
	fs, err := parse("add", "<ISSUE> | --repo <repo> \"<text>\"", args, func(fs *flag.FlagSet) {
		fs.StringVar(&repo, "repo", "", "the Repo, as a name such as data or a path")
	})
	if err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errUsage
	}
	req := scheduler.Request{Op: "add", Repo: repo}
	if repo != "" && !looksLikeIssue(fs.Arg(0)) {
		req.Text = fs.Arg(0)
	} else {
		req.Issue = fs.Arg(0)
	}
	var v scheduler.TaskView
	if err := c.Do(req, &v); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s queued in %s: %s\n", v.ID, v.Repo, v.Title)
	return nil
}

// looksLikeIssue matches an Issue ID: ENG-123, owner/repo#12, or local-7.
func looksLikeIssue(s string) bool {
	switch scheduler.KindOf(s) {
	case scheduler.KindGitHub:
		_, _, err := github.ParseIssueID(s)
		return err == nil
	case scheduler.KindLocal:
		_, err := localissues.ParseID(s)
		return err == nil
	}
	team, num, ok := strings.Cut(s, "-")
	if !ok || team == "" || num == "" || strings.ToUpper(team) != team {
		return false
	}
	for _, r := range num {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func cmdStart(c scheduler.Client, args []string, out io.Writer) error {
	var over bool
	fs, err := parse("start", "[--over-cap] <task>", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&over, "over-cap", false, "start once over the WIP cap; only with the user's yes")
	})
	if err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errUsage
	}
	if err := c.Do(scheduler.Request{Op: "start", Task: fs.Arg(0), OverCap: over}, nil); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s started\n", fs.Arg(0))
	return nil
}

// cmdReview adds and starts a review Task for the PR. If the PR has an open
// review Task, it adds nothing. It starts that Task if it is Queued, runs it
// again if it stopped for good, and otherwise focuses its tab.
func cmdReview(c scheduler.Client, args []string, out io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: lineup review <PR URL>", errUsage)
	}
	var res scheduler.ReviewResult
	if err := c.Do(scheduler.Request{Op: "review", URL: args[0]}, &res); err != nil {
		return err
	}
	switch res.Action {
	case scheduler.ReviewAdded:
		fmt.Fprintf(out, "%s reviewing in %s: %s\n", res.ID, res.Repo, res.Title)
		return nil
	case scheduler.ReviewStarted, scheduler.ReviewRestarted, scheduler.ReviewReopened:
		fmt.Fprintf(out, "%s %s: %s\n", res.ID, res.Action, res.Title)
		return nil
	}
	fmt.Fprintf(out, "%s is already open (%s): %s\n", res.ID, res.Stage, res.Title)
	if err := c.Do(scheduler.Request{Op: "focus", Task: res.ID}, nil); err != nil {
		fmt.Fprintf(out, "Not focused: %v\n", err)
	}
	return nil
}

// cmdReviews lists each review queue with its started reviews and the PRs
// that wait, pauses or resumes every queue fill, or skips or unskips a PR.
func cmdReviews(c scheduler.Client, args []string, out io.Writer) error {
	var asJSON bool
	fs, err := parse("reviews", "[--json] | pause | resume | skip <PR> | unskip <PR>", args, func(fs *flag.FlagSet) { fs.BoolVar(&asJSON, "json", false, "print JSON") })
	if err != nil {
		return err
	}
	switch {
	case fs.NArg() == 0:
	case fs.NArg() == 1 && !asJSON && (fs.Arg(0) == "pause" || fs.Arg(0) == "resume"):
		if err := c.Do(scheduler.Request{Op: "reviews-" + fs.Arg(0)}, nil); err != nil {
			return err
		}
		if fs.Arg(0) == "pause" {
			fmt.Fprintln(out, "Review queues paused. Started reviews go on, and no fill starts another.")
		} else {
			fmt.Fprintln(out, "Review queues resumed.")
		}
		return nil
	case fs.NArg() == 2 && !asJSON && (fs.Arg(0) == "skip" || fs.Arg(0) == "unskip"):
		var res scheduler.SkipResult
		if err := c.Do(scheduler.Request{Op: "reviews-" + fs.Arg(0), URL: fs.Arg(1)}, &res); err != nil {
			return err
		}
		printSkip(out, fs.Arg(0), fs.Arg(1), res)
		return nil
	default:
		fs.Usage()
		return errUsage
	}
	var v scheduler.ReviewsView
	if err := c.Do(scheduler.Request{Op: "reviews"}, &v); err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(out).Encode(v)
	}
	if len(v.Queues) == 0 {
		fmt.Fprintln(out, "No review queues. Add a [[review.queues]] entry to the config.")
		return nil
	}
	if v.Paused {
		fmt.Fprintln(out, "Reviews paused: no fill starts a review until lineup reviews resume.")
	}
	if v.Slots > 0 {
		fmt.Fprintf(out, "Reviews: %d/%d started\n", v.Started, v.Slots)
	}
	for _, q := range v.Queues {
		fmt.Fprintf(out, "Q: %s  %d/%d started\n", q.Name, len(q.Started), q.Slots)
		tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		for _, t := range q.Started {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", t.ID, t.Stage, t.Title)
		}
		for i, c := range q.Ranked {
			why := ""
			switch {
			case c.Rereview:
				why = "re-review"
			case c.ByName:
				why = "by name"
			}
			_, name, _ := strings.Cut(c.PR.Repo, "/")
			fmt.Fprintf(tw, "  %d\t%s\t%s#%d %s\t%s\n", i+1, why, name, c.PR.Number, c.PR.Title, c.PR.Author)
		}
		if len(q.Ranked) == 0 {
			fmt.Fprintln(tw, "  no PR waits")
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if len(v.Skips) > 0 {
		fmt.Fprintf(out, "Skips: %d. Put one back with lineup reviews unskip <PR>.\n", len(v.Skips))
		tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		for _, k := range v.Skips {
			key, _ := scheduler.ParseReviewKey(k.URL)
			fmt.Fprintf(tw, "  %s#%d\t%s\tsince %s\n", key.Repo, key.Number, k.Title, k.At.Format(time.DateOnly))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	for _, s := range v.Unmapped {
		fmt.Fprintln(out, "Unmapped: "+s)
	}
	return nil
}

// printSkip says what a skip or an unskip did. pr is the PR as the user typed it.
func printSkip(out io.Writer, verb, pr string, res scheduler.SkipResult) {
	switch {
	case verb == "skip" && !res.Changed:
		fmt.Fprintf(out, "%s is already skipped.\n", pr)
	case verb == "skip":
		fmt.Fprintf(out, "Skipped %s: %s. No queue picks it until you run lineup reviews unskip %s.\n", pr, res.Title, pr)
		if res.Closed != "" {
			fmt.Fprintf(out, "Closed %s.\n", res.Closed)
		}
	case !res.Changed:
		fmt.Fprintf(out, "%s is not skipped.\n", pr)
	default:
		fmt.Fprintf(out, "Unskipped %s. The queues can pick it again.\n", pr)
	}
}

func cmdLs(c scheduler.Client, args []string, out io.Writer) error {
	var asJSON bool
	if _, err := parse("ls", "[--json]", args, func(fs *flag.FlagSet) { fs.BoolVar(&asJSON, "json", false, "print JSON") }); err != nil {
		return err
	}
	var v scheduler.BoardView
	if err := c.Do(scheduler.Request{Op: "ls"}, &v); err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(out).Encode(v)
	}
	fmt.Fprintf(out, "WIP %d/%d", v.Started, v.Cap)
	if v.OverCap > 0 {
		fmt.Fprintf(out, " +%d over", v.OverCap)
	}
	fmt.Fprintln(out)
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, t := range v.Tasks {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Group, t.Stage, t.Repo, t.Title, t.Note)
	}
	return tw.Flush()
}

func cmdShow(c scheduler.Client, args []string, out io.Writer) error {
	var asJSON bool
	fs, err := parse("show", "[--json] <task>", args, func(fs *flag.FlagSet) { fs.BoolVar(&asJSON, "json", false, "print JSON") })
	if err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errUsage
	}
	var d scheduler.TaskDetail
	if err := c.Do(scheduler.Request{Op: "show", Task: fs.Arg(0)}, &d); err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(out).Encode(d)
	}
	fmt.Fprintf(out, "%s  %s\n%s in %s", d.ID, d.Title, d.Stage, d.Repo)
	if d.Branch != "" { // a review Task has no branch
		fmt.Fprintf(out, " (%s)", d.Branch)
	}
	fmt.Fprintln(out)
	if d.Note != "" {
		fmt.Fprintln(out, d.Note)
	}
	for _, u := range []string{d.IssueURL, d.PRURL, d.Worktree} {
		if u != "" {
			fmt.Fprintln(out, u)
		}
	}
	if len(d.Bar) > 0 {
		fmt.Fprintln(out, "Landing bar")
		for _, line := range d.Bar {
			fmt.Fprintln(out, "  "+line)
		}
	}
	for _, e := range d.Events {
		fmt.Fprintf(out, "  %s  %-26s %s -> %s  %s\n", e.At.Local().Format(time.DateTime), e.Signal, e.From, e.To, e.Detail)
	}
	return nil
}

func cmdSteer(c scheduler.Client, args []string, out io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("%w: lineup steer <task> \"<text>\"", errUsage)
	}
	return c.Do(scheduler.Request{Op: "steer", Task: args[0], Text: args[1]}, nil)
}

// cmdAsk is the Worker's own command. It puts its question on the Task's row.
func cmdAsk(c scheduler.Client, args []string, out io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("%w: lineup ask <task> \"<question>\"", errUsage)
	}
	return c.Do(scheduler.Request{Op: "ask", Task: args[0], Text: args[1]}, nil)
}

func cmdSet(c scheduler.Client, args []string, out io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("%w: lineup set <task> key=value", errUsage)
	}
	k, v, ok := strings.Cut(args[1], "=")
	if !ok || k == "" {
		return fmt.Errorf("%w: lineup set <task> key=value", errUsage)
	}
	return c.Do(scheduler.Request{Op: "set", Task: args[0], Key: k, Value: v}, nil)
}

func cmdCap(c scheduler.Client, args []string, out io.Writer) error {
	var n int
	if len(args) != 1 {
		return fmt.Errorf("%w: lineup cap <n>", errUsage)
	}
	if _, err := fmt.Sscan(args[0], &n); err != nil {
		return fmt.Errorf("%w: lineup cap <n>", errUsage)
	}
	return c.Do(scheduler.Request{Op: "cap", N: n}, nil)
}

func cmdRerun(c scheduler.Client, args []string, out io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("%w: lineup rerun <task> <stage>, for example ci_debug", errUsage)
	}
	return c.Do(scheduler.Request{Op: "rerun", Task: args[0], Stage: args[1]}, nil)
}

// cmdOpen brings the Task's live tab to the front. If the Task has no tab, it
// opens a new one that resumes the Task's conversation.
func cmdOpen(c scheduler.Client, args []string, out io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: lineup open <task>", errUsage)
	}
	var res scheduler.OpenResult
	if err := c.Do(scheduler.Request{Op: "open", Task: args[0]}, &res); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s %s: %s\n", res.ID, res.Action, res.Title)
	return nil
}

func simple(op string) func(scheduler.Client, []string, io.Writer) error {
	return func(c scheduler.Client, args []string, out io.Writer) error {
		if len(args) != 1 {
			return fmt.Errorf("%w: lineup %s <task>", errUsage, op)
		}
		return c.Do(scheduler.Request{Op: op, Task: args[0]}, nil)
	}
}

func stateDir() string {
	if d := os.Getenv("LINEUP_STATE_DIR"); d != "" {
		return d
	}
	return filepath.Join(xdgStateHome(), "lineup")
}

func socketPath() string { return stateDir() + "/lineup.sock" }
