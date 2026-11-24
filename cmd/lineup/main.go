// Command lineup is the Board, the Scheduler, and the CLI that the Orchestrator uses.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/scheduler"
)

const usage = `usage: lineup [command]

  lineup                       open the Hub in Rex
  lineup serve                 run the Scheduler in the foreground
  lineup restart               restart the Scheduler, then open each Hub tab again: each
                               Board runs the installed lineup, and the Orchestrator starts
                               a new session
  lineup restart scheduler     install the Scheduler's launchd agent and restart only it
  lineup stop                  stop the Scheduler until the next restart or login
  lineup logs                  follow the Scheduler's log
  lineup board                 run the Board: your build Tasks and your Issues
  lineup login [linear [workspace] | github]
                               sign in to a Tracker; with no argument, asks which
  lineup logout linear [workspace] [--local]
                               sign out of Linear; --local only forgets the tokens here
  lineup logout github         stop using GitHub Issues as a Tracker
  lineup trackers              name the Trackers that are on
  lineup config                open the config panel: each setting with its doc, its default,
                               the value that applies, and the level that sets it. It
                               changes one setting at a time, and only if the file loads
  lineup config keys [--json] [--repo <owner>/<repo>]
                               list every config key; --repo adds the value that applies to
                               the Repo and the level that sets it
  lineup config migrate        rewrite the config file from [[repos]] tables to roots
                               and [repos."owner/repo"] tables, from trackers lists
                               to tracker keys, and from ready and queued in [board]
                               to up_next
  lineup repo detect <owner>/<repo> --json
                               print the detected setup of a Repo: its Tracker, review
                               bot, Landing bar, Linear teams, and worktree hooks
  lineup repo init <owner>/<repo> [key=value ...] | --defaults
                               add the Repo's [repos."owner/repo"] table, which lineup add
                               needs for a build Task, then restart the Scheduler.
                               --defaults writes what repo detect lists in init

  lineup add [--over-cap] [--set key=value]... <ISSUE> | --repo <repo> "<text>"
                               start a Task for an Issue (ENG-614, owner/repo#12, LOCAL-7,
                               or HOD-3), or for the text in the Repo
  lineup next-id [<repo>]      print the ID of the Repo's next Local Issue, for example HOD-4
  lineup review <PR URL>       review someone else's PR; it posts only what you decide
  lineup reviews [--json]      list each review queue: its started reviews and the PRs that wait
  lineup reviews pause | resume
                               stop or restart every review queue fill
  lineup ls [--json]           list the Tasks
  lineup ready                 name each Task whose PR is ready to land
  lineup show [--json] <task>
  lineup decide [--json]       list the Tasks that need you, with your choices for each
  lineup decide <task> <choice> ["<text>"]
                               do one of those choices, for example lineup decide ENG-34 close
  lineup steer <task> "<text>" type text into the Task's Worker
  lineup ask <task> "<question>"
                               a Worker's question for the user, on the Task's row
  lineup ask --drafts <n> <task>
                               a Responding Worker's Draft replies wait for the user
  lineup set <task> key=value  override configuration, for example model=claude-opus-5-5
  lineup cap <n>               set the WIP cap
  lineup rerun <task> <stage>  type the Stage's prompt into the Worker again, for example for ci_debug
  lineup open <task>           focus the Task's tab, or restore its closed Worker
  lineup merge <task>
  lineup close <task>          end the Task, and close its tab and worktree
  lineup archive <task>        close a Done Task's tab and worktree, and hide it from the Board
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
		return openHub(hubRepair)
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
		return cmdRestart(args[1:], os.Stdout, restartScheduler, openHub)
	case "hub": // openHub's detached copy runs this, so usage leaves it out
		return runHub(args[1:])
	case "stop":
		return cmdStop()
	case "logs":
		return cmdLogs()
	case "login":
		return login(args[1:], os.Stdin, os.Stdout)
	case "logout":
		return logout(args[1:], os.Stdout, linear.Logout)
	case "config":
		return cmdConfig(args[1:], os.Stdout, runConfigPanel)
	case "repo":
		return cmdRepo(context.Background(), args[1:], os.Stdout, realRepoEnv())
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
