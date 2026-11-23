package scheduler

// sidePane is what a Task's side pane runs: its folder and its command. It
// never changes after it is made, so a lane's copy of the Record can share it.
type sidePane struct {
	Dir  string   `json:"dir"`
	Argv []string `json:"argv"`
}
