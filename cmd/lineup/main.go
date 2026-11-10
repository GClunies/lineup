// Command lineup is the Board, the Scheduler, and the CLI that the Orchestrator uses.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/scheduler"
)

const usage = `usage: lineup [command]

  lineup                       open the Hub in Rex
  lineup serve                 run the Scheduler in the foreground
  lineup restart               install the Scheduler's launchd agent and restart it
  lineup stop                  stop the Scheduler until the next restart or login
  lineup logs                  follow the Scheduler's log
  lineup board                 run the Board: the plan, work, and review views
  lineup login [linear [workspace] | github]
                               sign in to a Tracker; with no argument, asks which
  lineup logout linear [workspace] [--local]
                               sign out of Linear; --local only forgets the tokens here
  lineup logout github         stop using GitHub Issues as a Tracker
  lineup trackers              name the Trackers that are on

  lineup add <ISSUE>           put an Issue on the Board: ENG-614, owner/repo#12, or local-7
  lineup add --repo <repo> "<text>"
  lineup start [--over-cap] <task>
  lineup review <PR URL>       review someone else's PR; it posts only what you decide
  lineup reviews [--json]      list each review queue: its started reviews and the PRs that wait
  lineup reviews pause | resume
                               stop or restart every review queue fill
  lineup ls [--json]           list the Tasks
  lineup show [--json] <task>
  lineup watch [--once]        print a line each time a build Task needs you or ends
  lineup steer <task> "<text>" type text into the Task's Worker
  lineup set <task> key=value  override configuration, for example harness=pi
  lineup cap <n>               set the WIP cap
  lineup rerun <task> <stage>  start a fresh Worker, for example for ci_debug
  lineup merge <task>
  lineup close <task>
  lineup archive <task>        hide a Done Task from the Board
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		if !errors.Is(err, errUsage) {
			fmt.Fprintln(os.Stderr, "lineup:", err)
		} else if err != errUsage {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return openHub()
	}
	if cmd, ok := clientCommands[args[0]]; ok {
		return cmd(scheduler.Client{Path: socketPath()}, args[1:], os.Stdout)
	}
	switch args[0] {
	case "serve":
		return serve()
	case "board":
		return runBoard(args[1:])
	case "restart":
		return cmdRestart()
	case "stop":
		return cmdStop()
	case "logs":
		return cmdLogs()
	case "login":
		return login(args[1:], os.Stdin, os.Stdout)
	case "logout":
		return logout(args[1:], os.Stdout, linear.Logout)
	case "trackers":
		on, err := currentTrackers()
		if err != nil {
			return err
		}
		fmt.Println(trackersLine(on))
		return nil
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	}
	fmt.Fprint(os.Stderr, usage)
	return errUsage
}
