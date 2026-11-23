package config

import (
	"fmt"
	"slices"
	"strings"
)

// AgentSide is the side of the Hub's lineup tab that the LINEUP pane takes.
// The Board takes the other side.
type AgentSide string

const (
	AgentLeft   AgentSide = "left"
	AgentRight  AgentSide = "right"
	AgentTop    AgentSide = "top"
	AgentBottom AgentSide = "bottom"
)

// agentSides are the values of layout.agent_side, in the order that the
// error names them.
var agentSides = []AgentSide{AgentLeft, AgentRight, AgentTop, AgentBottom}

// The bounds of layout.agent_size, a percent of the tab.
const minAgentSize, maxAgentSize = 10, 90

// Layout is the split of the Hub's lineup tab. AgentSize is the LINEUP
// pane's percent of the tab's width for left or right, or of its height for
// top or bottom. The Board takes the rest.
type Layout struct {
	AgentSide AgentSide
	AgentSize int
}

// layoutFile is the [layout] table. A key that it leaves out takes its
// default.
type layoutFile struct {
	AgentSide *AgentSide `toml:"agent_side"`
	AgentSize *int       `toml:"agent_size"`
}

// resolve is the table's layout, by default left and 30. The error names
// the bad key.
func (f layoutFile) resolve() (Layout, error) {
	l := Layout{AgentSide: AgentLeft, AgentSize: 30}
	if s := f.AgentSide; s != nil {
		if !slices.Contains(agentSides, *s) {
			names := make([]string, len(agentSides))
			for i, side := range agentSides {
				names[i] = string(side)
			}
			return Layout{}, fmt.Errorf("layout.agent_side: %q is not one of %s", *s, strings.Join(names, ", "))
		}
		l.AgentSide = *s
	}
	if n := f.AgentSize; n != nil {
		if *n < minAgentSize || *n > maxAgentSize {
			return Layout{}, fmt.Errorf("layout.agent_size: %d is not between %d and %d", *n, minAgentSize, maxAgentSize)
		}
		l.AgentSize = *n
	}
	return l, nil
}

// Layout is the split of the Hub's lineup tab. Load refuses a bad [layout]
// table.
func (g *Global) Layout() Layout {
	l, _ := g.file.Layout.resolve()
	return l
}
