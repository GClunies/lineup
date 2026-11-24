package config

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
)

// Level is a place in the config file where a key can sit.
type Level int

const (
	// LevelTop is the top level of the file, for the keys that apply once.
	LevelTop Level = iota
	// LevelDefaults is the [defaults] table.
	LevelDefaults
	// LevelOwner is the [repos."<owner>/*"] table of an owner.
	LevelOwner
	// LevelRepo is the [repos."<owner>/<repo>"] table of one Repo.
	LevelRepo
)

var levelNames = []string{LevelTop: "top level", LevelDefaults: "[defaults]", LevelOwner: "owner/*", LevelRepo: "owner/repo"}

func (l Level) String() string { return levelNames[l] }

func (l Level) MarshalText() ([]byte, error) { return []byte(l.String()), nil }

// ValueType is the type of a key's value.
type ValueType string

const (
	TypeInt      ValueType = "int"
	TypeString   ValueType = "string"
	TypeBool     ValueType = "bool"
	TypeDuration ValueType = "duration"
	// TypeEnum is a string that is one of the key's Values.
	TypeEnum ValueType = "enum"
	// TypeList is a list of strings. Values, when set, lists the items it can hold.
	TypeList ValueType = "list"
	// TypeQueue is a [[review.queues]] entry, which the catalog shows and
	// does not edit.
	TypeQueue ValueType = "queue"
)

// The restarts that apply a change. The Board and the Hub read some keys,
// and only lineup restart starts them again (PROD-CONFIG-007).
const (
	RestartAll       = "lineup restart"
	RestartScheduler = "lineup restart scheduler"
)

// Key is one key of the config file.
type Key struct {
	// Path is the dotted path of the key inside its level. A part in angle
	// brackets, such as <stage>, stands for each key of a map.
	Path   string    `json:"path"`
	Levels []Level   `json:"levels"`
	Type   ValueType `json:"type"`
	// Values are the values of an enum, or the items that a list can hold.
	Values []string `json:"values,omitempty"`
	// Default is the text of the value when no level sets the key.
	Default string `json:"default"`
	Doc     string `json:"doc"`
	// Restart is the command that applies a change of the key.
	Restart string `json:"restart"`
}

var (
	top         = []Level{LevelTop}
	layerLevels = []Level{LevelDefaults, LevelOwner, LevelRepo}
)

// Catalog lists every key of the config file. A test ties it to the fields
// that the loader decodes, so a key cannot go missing from it.
var Catalog = []Key{
	{Path: "roots", Levels: top, Type: TypeList, Default: "none", Restart: RestartAll,
		Doc: "The folders that hold the checkouts. lineup finds each checkout in a root and up to two folders below it."},
	{Path: "start_skills", Levels: top, Type: TypeList, Default: "none", Restart: RestartAll,
		Doc: "The skills that the Board offers when it starts a Task from an Issue."},
	{Path: "local_tracker", Levels: top, Type: TypeBool, Default: "false", Restart: RestartAll,
		Doc: "Turns the Local Tracker on beside Linear and GitHub. It is on by itself when no other Tracker is on."},
	{Path: "local_issues_dir", Levels: top, Type: TypeString, Default: "~/.local/share/lineup/issues", Restart: RestartAll,
		Doc: "The folder that holds the Issues of the Local Tracker."},
	{Path: "linear_client_id", Levels: top, Type: TypeString, Default: "lineup's own app", Restart: RestartScheduler,
		Doc: "The Linear OAuth app that lineup login linear signs in with."},
	{Path: "linear.<workspace>.client_id", Levels: top, Type: TypeString, Default: "linear_client_id", Restart: RestartScheduler,
		Doc: "The Linear OAuth app of one workspace, in place of linear_client_id."},
	{Path: "linear.<workspace>.status.<team>.<event>", Levels: top, Type: TypeString, Default: "no change", Restart: RestartScheduler,
		Doc: "The Linear status that lineup gives an Issue of the team at the lifecycle event."},
	{Path: "github.labels.<event>", Levels: top, Type: TypeString, Default: "none", Restart: RestartScheduler,
		Doc: "The label that lineup puts on a GitHub Issue at the lifecycle event."},
	{Path: "github.labels.backlog", Levels: top, Type: TypeString, Default: backlogKey, Restart: RestartAll,
		Doc: "The label that puts a GitHub Issue in Backlog on the Board. An empty label puts each GitHub Issue in Up next."},
	{Path: "review.slots", Levels: top, Type: TypeInt, Default: fmt.Sprintf("none: each queue starts up to %d", defaultSlots), Restart: RestartScheduler,
		Doc: "How many review Tasks of all queues together can be started at one time."},
	{Path: "review.queues.<field>", Levels: top, Type: TypeQueue, Default: "none", Restart: RestartScheduler,
		Doc: "A review queue: a PR filter whose PRs lineup reviews without being asked. The queues fill in file order. Edit them in the file."},
	{Path: "board.order", Levels: top, Type: TypeList, Values: sectionKeys(), Default: listText(sectionKeys()), Restart: RestartAll,
		Doc: "The order of the Board's sections. Each section that the list leaves out follows in its default place."},
	{Path: "board.collapsed", Levels: top, Type: TypeList, Values: sectionKeys(), Default: listText(defaultCollapsed), Restart: RestartAll,
		Doc: "The sections that start closed. needs_you cannot be in the list."},
	{Path: "board.hidden", Levels: top, Type: TypeList, Values: sectionKeys(), Default: listText(nil), Restart: RestartAll,
		Doc: "The sections that start out of sight. needs_you cannot be in the list."},
	{Path: "merged.close_tab", Levels: layerLevels, Type: TypeBool, Default: "false", Restart: RestartScheduler,
		Doc: "Closes the tab of a build Task when its PR merges, once its Worker is idle."},
	{Path: "merged.keep_worktree_days", Levels: layerLevels, Type: TypeInt, Default: strconv.Itoa(defaultKeepWorktreeDays), Restart: RestartScheduler,
		Doc: fmt.Sprintf("How many days lineup keeps the worktree of a merged build Task after it closes the tab, from 0 to %d.", maxKeepWorktreeDays)},
	{Path: "layout.agent_side", Levels: top, Type: TypeEnum, Values: agentSideNames(), Default: string(AgentLeft), Restart: RestartAll,
		Doc: "The side of the my-lineup tab that the LINEUP pane takes. The Board takes the other side."},
	{Path: "layout.agent_size", Levels: top, Type: TypeInt, Default: strconv.Itoa(defaultAgentSize), Restart: RestartAll,
		Doc: fmt.Sprintf("The percent of the my-lineup tab that the LINEUP pane takes, from %d to %d.", minAgentSize, maxAgentSize)},

	{Path: "tracker", Levels: []Level{LevelOwner, LevelRepo}, Type: TypeEnum, Values: TrackerKinds, Default: "none", Restart: RestartAll,
		Doc: "The one Tracker whose Issues the Repo takes. A Repo with none takes only ad hoc Tasks."},
	{Path: "teams", Levels: []Level{LevelRepo}, Type: TypeList, Default: "none", Restart: RestartAll,
		Doc: "The Linear teams whose Issues map to the Repo. The tracker of the Repo must be linear."},
	{Path: "prefix", Levels: []Level{LevelRepo}, Type: TypeString, Default: "none", Restart: RestartAll,
		Doc: "The prefix of the IDs of the Repo's Local Issues and ad hoc Tasks, as in HOD-3."},
	{Path: "wip_cap", Levels: layerLevels, Type: TypeInt, Default: strconv.Itoa(defaultWIPCap), Restart: RestartScheduler,
		Doc: "How many build Tasks can be started at one time."},
	{Path: "poll_interval", Levels: layerLevels, Type: TypeDuration, Default: defaultPollInterval.String(), Restart: RestartScheduler,
		Doc: "How often the Scheduler polls, as a duration such as 30s."},
	{Path: "budgets.ci_debug", Levels: layerLevels, Type: TypeInt, Default: strconv.Itoa(defaultBudget), Restart: RestartScheduler,
		Doc: "How many times the CI debug Stage runs on one PR before the Task goes to Needs you."},
	{Path: "budgets.responding", Levels: layerLevels, Type: TypeInt, Default: strconv.Itoa(defaultBudget), Restart: RestartScheduler,
		Doc: "How many times the Responding Stage runs on one PR before the Task goes to Needs you."},
	{Path: "review_bot.name", Levels: layerLevels, Type: TypeEnum, Values: slices.Sorted(maps.Keys(github.ReviewBots)), Default: "none: no gate", Restart: RestartScheduler,
		Doc: "The review bot whose score gates Ready to land."},
	{Path: "review_bot.min_review_score", Levels: layerLevels, Type: TypeInt, Default: "the bot's top score", Restart: RestartScheduler,
		Doc: "The lowest score of the review bot that passes the gate."},
	{Path: "review_bot.max_review_cycles", Levels: layerLevels, Type: TypeInt, Default: strconv.Itoa(DefaultReviewCycles), Restart: RestartScheduler,
		Doc: "How many review cycles the review bot gets before the Task goes to Needs you."},
	{Path: "landing_bar.checks", Levels: layerLevels, Type: TypeEnum, Values: lifecycle.CheckRuleNames(), Default: DefaultBar().Checks.String(), Restart: RestartScheduler,
		Doc: "The checks that must pass: the required checks of the base branch, or all checks."},
	{Path: "landing_bar.threads", Levels: layerLevels, Type: TypeEnum, Values: lifecycle.ThreadRuleNames(), Default: DefaultBar().Threads.String(), Restart: RestartScheduler,
		Doc: "What each review thread needs: nothing, a resolve, or a resolve and the user's reply in each thread that someone else opened."},
	{Path: "landing_bar.approvals", Levels: layerLevels, Type: TypeEnum, Values: lifecycle.ApprovalRuleNames(), Default: DefaultBar().Approvals.String(), Restart: RestartScheduler,
		Doc: "The reviews that must approve: the reviews that the base branch requires, or also each requested reviewer."},
	{Path: "stage.<stage>.model", Levels: layerLevels, Type: TypeString, Default: defaultModel, Restart: RestartScheduler,
		Doc: "The model of the Worker. Only default and the Stage that a Worker starts in take a model."},
	{Path: "stage.<stage>.effort", Levels: layerLevels, Type: TypeString, Default: defaultEffort, Restart: RestartScheduler,
		Doc: "The effort of the Worker. Only default and the Stage that a Worker starts in take an effort."},
	{Path: "stage.<stage>.skill", Levels: layerLevels, Type: TypeString, Default: "none", Restart: RestartScheduler,
		Doc: "The skill that becomes the first slash command of the Stage prompt."},
	{Path: "stage.<stage>.command", Levels: layerLevels, Type: TypeString, Default: "none", Restart: RestartScheduler,
		Doc: "A command that the Stage prompt tells the Worker to run first."},
}

func sectionKeys() []string {
	out := make([]string, len(sections))
	for i, s := range sections {
		out[i] = s.key
	}
	return out
}

func agentSideNames() []string {
	out := make([]string, len(agentSides))
	for i, s := range agentSides {
		out[i] = string(s)
	}
	return out
}

// listText writes a list of strings as TOML writes it, as in ["a", "b"].
func listText(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = strconv.Quote(it)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// isPlaceholder reports whether a part of a Path stands for each key of a map.
func isPlaceholder(part string) bool {
	return strings.HasPrefix(part, "<") && strings.HasSuffix(part, ">")
}

// keyFor is the catalog entry of a concrete path, such as stage.working.model.
// A path that equals an entry's Path beats one that only fits its
// placeholders, so github.labels.backlog is its own entry.
func keyFor(path string) (Key, bool) {
	parts := strings.Split(path, ".")
	fit := -1
	for i, k := range Catalog {
		if k.Path == path {
			return k, true
		}
		if fit < 0 && fits(strings.Split(k.Path, "."), parts) {
			fit = i
		}
	}
	if fit < 0 {
		return Key{}, false
	}
	return Catalog[fit], true
}

// fits reports whether the parts of a concrete path fit the parts of a
// Path, where a placeholder fits any part that is not empty.
func fits(pattern, parts []string) bool {
	if len(pattern) != len(parts) {
		return false
	}
	for i, p := range pattern {
		if parts[i] == "" || !isPlaceholder(p) && p != parts[i] {
			return false
		}
	}
	return true
}

// Encode turns what the user typed into the TOML text of the key's value.
// A list takes its items with commas between them, and an empty text is an
// empty list.
func (k Key) Encode(input string) (string, error) {
	input = strings.TrimSpace(input)
	switch k.Type {
	case TypeInt:
		n, err := strconv.Atoi(input)
		if err != nil {
			return "", fmt.Errorf("%s: %q is not a whole number", k.Path, input)
		}
		return strconv.Itoa(n), nil
	case TypeBool:
		if input != "true" && input != "false" {
			return "", fmt.Errorf("%s: %q is not true or false", k.Path, input)
		}
		return input, nil
	case TypeList:
		if input == "" {
			return "[]", nil
		}
		v, err := tomlList(input)
		if err != nil {
			return "", fmt.Errorf("%s: %w", k.Path, err)
		}
		return v, nil
	case TypeQueue:
		return "", fmt.Errorf("%s: edit the review queues in the file", k.Path)
	}
	return strconv.Quote(input), nil
}
