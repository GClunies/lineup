package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/scheduler"
)

type choice struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	TakesText bool   `json:"takes_text,omitempty"`
	run       func(c scheduler.Client, t scheduler.TaskView, text string, out io.Writer) error
}

func request(op string) func(scheduler.Client, scheduler.TaskView, string, io.Writer) error {
	return func(c scheduler.Client, t scheduler.TaskView, _ string, _ io.Writer) error {
		return c.Do(scheduler.Request{Op: op, Task: t.ID}, nil)
	}
}

func closeIt(c scheduler.Client, t scheduler.TaskView, _ string, out io.Writer) error {
	return cmdClose(c, []string{t.ID}, out)
}

func openWorker(c scheduler.Client, t scheduler.TaskView, _ string, out io.Writer) error {
	return cmdOpen(c, []string{t.ID}, out)
}

var (
	openTab   = choice{ID: "open", Title: "Open the tab", run: openWorker}
	restore   = choice{ID: "open", Title: "Restore the Worker", run: openWorker}
	closeTask = choice{ID: "close", Title: "Close the Task", run: closeIt}
	merge     = choice{ID: "merge", Title: "Merge the PR", run: request("merge")}
	rerun     = choice{ID: "rerun", Title: "Run the Stage again", run: func(c scheduler.Client, t scheduler.TaskView, _ string, _ io.Writer) error {
		return c.Do(scheduler.Request{Op: "rerun", Task: t.ID, Stage: t.StageKey}, nil)
	}}
	answer = choice{ID: "answer", Title: "Answer here", TakesText: true, run: func(c scheduler.Client, t scheduler.TaskView, text string, _ io.Writer) error {
		return c.Do(scheduler.Request{Op: "steer", Task: t.ID, Text: text}, nil)
	}}
)

// A build Task in Needs you with no flag is Ready to land, the only build Stage
// in that group.
const readyToLand lifecycle.Reason = ""

// choicesFor lists the choices for each Needs you reason in the order that the
// palette shows them, the default first.
var choicesFor = map[lifecycle.Reason][]choice{
	readyToLand:                 {merge, openTab},
	lifecycle.ReasonTabClosed:   {restore, closeTask},
	lifecycle.ReasonQuestion:    {answer, openTab},
	lifecycle.ReasonStopped:     {openTab, closeTask},
	lifecycle.ReasonBudgetSpent: {rerun, openTab, closeTask},
	lifecycle.ReasonPRClosed:    {closeTask, openTab},
	lifecycle.ReasonSetupFailed: {openTab, closeTask},
}

// reviewReady holds the choices for a review Task in Needs you with no flag,
// which is Review ready, the only review Stage in that group.
var reviewReady = []choice{openTab, closeTask}

// choicesOf lists the choices for t. A typed answer needs a Worker to read it.
func choicesOf(t scheduler.TaskView) []choice {
	reason := lifecycle.Reason(t.Flag)
	cs, ok := choicesFor[reason]
	switch {
	case t.Kind == lifecycle.KindReview && reason == readyToLand:
		cs = reviewReady
	case !ok:
		cs = []choice{openTab}
	}
	if t.Live {
		return cs
	}
	return slices.DeleteFunc(slices.Clone(cs), func(ch choice) bool { return ch.TakesText })
}

type decision struct {
	scheduler.TaskView
	Choices []choice `json:"choices"`
}

func decisions(v scheduler.BoardView) []decision {
	ds := []decision{}
	for _, kind := range []lifecycle.Kind{lifecycle.KindBuild, lifecycle.KindReview} {
		for _, t := range v.OfKind(kind).Tasks {
			if t.Group == lifecycle.GroupNeedsYou.String() {
				ds = append(ds, decision{t, choicesOf(t)})
			}
		}
	}
	return ds
}

// cmdDecide backs the palette actions in rex/lineup.lua, which read its --json
// output.
func cmdDecide(c scheduler.Client, args []string, out io.Writer) error {
	var asJSON bool
	fs, err := parse("decide", `[--json] | <task> <choice> ["<text>"]`, args, func(fs *flag.FlagSet) {
		fs.BoolVar(&asJSON, "json", false, "print JSON")
	})
	if err != nil {
		return err
	}
	if n := fs.NArg(); asJSON && n > 0 || n == 1 || n > 3 {
		fs.Usage()
		return errUsage
	}
	var v scheduler.BoardView
	if err := c.Do(scheduler.Request{Op: "ls"}, &v); err != nil {
		return err
	}
	ds := decisions(v)
	if fs.NArg() == 0 {
		return printDecisions(ds, asJSON, out)
	}
	id, choiceID, text := fs.Arg(0), fs.Arg(1), fs.Arg(2)
	i := slices.IndexFunc(ds, func(d decision) bool { return strings.EqualFold(d.ID, id) })
	if i < 0 {
		return fmt.Errorf("%s does not need you", scheduler.CanonicalID(id))
	}
	d := ds[i]
	id = d.ID
	j := slices.IndexFunc(d.Choices, func(ch choice) bool { return ch.ID == choiceID })
	if j < 0 {
		var ids []string
		for _, ch := range d.Choices {
			ids = append(ids, ch.ID)
		}
		return fmt.Errorf("%s has no choice %q. Its choices are %s", id, choiceID, strings.Join(ids, ", "))
	}
	ch := d.Choices[j]
	switch {
	case ch.TakesText && strings.TrimSpace(text) == "":
		return fmt.Errorf(`%w: lineup decide %s %s "<text>"`, errUsage, id, choiceID)
	case !ch.TakesText && fs.NArg() == 3:
		return fmt.Errorf("%w: the choice %s takes no text", errUsage, choiceID)
	}
	return ch.run(c, d.TaskView, text, out)
}

func printDecisions(ds []decision, asJSON bool, out io.Writer) error {
	if asJSON {
		return json.NewEncoder(out).Encode(struct {
			Tasks []decision `json:"tasks"`
		}{ds})
	}
	if len(ds) == 0 {
		fmt.Fprintln(out, "No Task needs you.")
		return nil
	}
	for _, d := range ds {
		note := d.Note
		if note == "" {
			note = d.Stage
		}
		fmt.Fprintf(out, "%s  %s  %s\n", d.ID, note, d.Title)
		for _, ch := range d.Choices {
			fmt.Fprintf(out, "  %-6s  %s\n", ch.ID, ch.Title)
		}
	}
	return nil
}
