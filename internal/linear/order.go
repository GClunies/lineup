package linear

import (
	"sort"
	"strconv"
	"strings"
)

// Order returns issues in the tracker browser's order: dependency level
// first, then natural ID order (ENG-9 before ENG-10). An Issue with no open
// blocker has level 0. Any other Issue sits one level above its highest open
// blocker. A blocker outside issues counts as level 0. A cycle is cut at the
// edge that closes it, as in herdr-linear's graph.go. The input is unchanged.
func Order(issues []Issue) []Issue {
	at := make(map[string]int, len(issues))
	for i, is := range issues {
		at[is.ID] = i
	}
	// succ[u] lists the Issues that u blocks, inside the list.
	succ := make([][]int, len(issues))
	for v, is := range issues {
		for _, b := range is.Blockers {
			if u, ok := at[b.ID]; ok && u != v {
				succ[u] = append(succ[u], v)
			}
		}
	}
	breakCycles(succ)
	pred := make([][]int, len(issues))
	for u, vs := range succ {
		for _, v := range vs {
			pred[v] = append(pred[v], u)
		}
	}
	level := make([]int, len(issues))
	known := make([]bool, len(issues))
	var levelOf func(i int) int
	levelOf = func(i int) int {
		if !known[i] {
			known[i] = true // the graph is acyclic here, so this never re-enters
			for _, p := range pred[i] {
				level[i] = max(level[i], levelOf(p)+1)
			}
		}
		return level[i]
	}
	for i := range issues {
		levelOf(i)
	}
	out := append([]Issue(nil), issues...)
	lv := make(map[string]int, len(issues))
	for i, is := range issues {
		lv[is.ID] = level[i]
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := lv[out[i].ID], lv[out[j].ID]; a != b {
			return a < b
		}
		return naturalLess(out[i].Identifier, out[j].Identifier)
	})
	return out
}

// breakCycles drops each edge that closes a cycle (a back edge of a depth
// first walk), leaving a graph that has levels.
func breakCycles(succ [][]int) {
	state := make([]int8, len(succ)) // 0 unseen, 1 on the walk, 2 done
	var walk func(u int)
	walk = func(u int) {
		state[u] = 1
		kept := succ[u][:0]
		for _, v := range succ[u] {
			if state[v] == 1 {
				continue
			}
			kept = append(kept, v)
			if state[v] == 0 {
				walk(v)
			}
		}
		succ[u] = kept
		state[u] = 2
	}
	for u := range succ {
		if state[u] == 0 {
			walk(u)
		}
	}
}

// naturalLess orders identifiers by team key, then by number as a number.
func naturalLess(a, b string) bool {
	ka, na, oka := splitID(a)
	kb, nb, okb := splitID(b)
	if !oka || !okb {
		return a < b
	}
	if ka != kb {
		return ka < kb
	}
	return na < nb
}

func splitID(id string) (key string, n int, ok bool) {
	i := strings.LastIndexByte(id, '-')
	if i < 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(id[i+1:])
	return id[:i], n, err == nil
}

// issueStateRank is the order of the state groups in a Linear view.
var issueStateRank = map[string]int{"started": 0, "unstarted": 1, "backlog": 2, "triage": 3, "completed": 4, "canceled": 5}

// projectStateRank is the order of the Project status groups in a Linear view.
var projectStateRank = map[string]int{"started": 0, "planned": 1, "backlog": 2, "paused": 3, "completed": 4, "canceled": 5}

// rankOf returns a type's rank, with an unknown type after every known one.
func rankOf(ranks map[string]int, typ string) int {
	if r, ok := ranks[typ]; ok {
		return r
	}
	return len(ranks)
}

// priorityRank puts urgent (1) first and no priority (0) after low (4).
func priorityRank(p int) int {
	if p == 0 {
		return 5
	}
	return p
}

// cmpFloat orders a before b when less, as -1, 0, 1.
func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// firstNonZero is the first comparison that tells a and b apart.
func firstNonZero(cs ...int) int {
	for _, c := range cs {
		if c != 0 {
			return c
		}
	}
	return 0
}

// ViewOrder returns issues in the order of a Linear view grouped by cycle and
// state: cycles by start date with no cycle last, then state type (started,
// unstarted, backlog, triage, completed, canceled) with Linear's state
// position descending inside a type, then priority (urgent first, none last),
// then Linear's manual order. The input is unchanged.
func ViewOrder(issues []Issue) []Issue {
	out := append([]Issue(nil), issues...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return firstNonZero(
			cmpCycle(a.Cycle, b.Cycle),
			rankOf(issueStateRank, a.State.Type)-rankOf(issueStateRank, b.State.Type),
			cmpFloat(b.State.Position, a.State.Position),
			strings.Compare(a.State.Name, b.State.Name),
			priorityRank(a.Priority)-priorityRank(b.Priority),
			cmpFloat(a.PrioritySortOrder, b.PrioritySortOrder),
		) < 0
	})
	return out
}

func cmpCycle(a, b *Cycle) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	return firstNonZero(a.StartsAt.Compare(b.StartsAt), strings.Compare(a.Name, b.Name))
}

// ProjectOrder returns ps in the order of a Linear Project view grouped by
// status: status type (started, planned, backlog, paused, completed,
// canceled) with Linear's status position descending inside a type, then
// priority (urgent first, none last), then Linear's manual order. The input
// is unchanged.
func ProjectOrder(ps []ProjectInfo) []ProjectInfo {
	out := append([]ProjectInfo(nil), ps...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return firstNonZero(
			rankOf(projectStateRank, a.StatusType)-rankOf(projectStateRank, b.StatusType),
			cmpFloat(b.StatusPosition, a.StatusPosition),
			strings.Compare(a.Status, b.Status),
			priorityRank(a.Priority)-priorityRank(b.Priority),
			cmpFloat(a.PrioritySortOrder, b.PrioritySortOrder),
		) < 0
	})
	return out
}
