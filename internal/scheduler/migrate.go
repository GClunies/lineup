package scheduler

import (
	"bytes"
	"encoding/json"

	"github.com/gclunies/lineup/internal/lifecycle"
)

// legacyQueued is the Stage of a Task that waited on the Board for a start,
// before lineup add started each Task. Only migrate reads it.
var legacyQueued = struct{ key, name string }{"queued", "Queued"}

// migrate rewrites a stored Record from before lineup add started each Task.
// A Worker Stage of Queued meant no Worker, as a missing worker_stage does
// now. A Task in Queued never started: it becomes an archived Done Task with
// the outcome removed, so that its Issue shows as an Issue row again. removed
// reports that change.
func migrate(data []byte) (out []byte, removed bool, err error) {
	var rec map[string]json.RawMessage
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, false, err
	}
	queued, _ := json.Marshal(legacyQueued.key)
	noWorker := bytes.Equal(rec["worker_stage"], queued)
	removed = bytes.Equal(rec["stage"], queued)
	if !noWorker && !removed {
		return data, false, nil
	}
	delete(rec, "worker_stage")
	if removed {
		rec["stage"], _ = json.Marshal(lifecycle.Done.Key())
		rec["outcome"], _ = json.Marshal(lifecycle.OutcomeRemoved)
		rec["archived"] = json.RawMessage("true")
	}
	out, err = json.Marshal(rec)
	return out, removed, err
}
