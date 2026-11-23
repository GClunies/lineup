package scheduler

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/store"
)

// Request is one call over the Scheduler's Unix socket. Each op reads only
// the fields it names.
type Request struct {
	Op      string `json:"op"`
	Task    string `json:"task,omitempty"`
	Issue   string `json:"issue,omitempty"`
	Kind    string `json:"kind,omitempty"` // the Issue's Tracker; empty reads it from the ID
	Repo    string `json:"repo,omitempty"`
	Text    string `json:"text,omitempty"`
	Stage   string `json:"stage,omitempty"`
	Key     string `json:"key,omitempty"`
	Value   string `json:"value,omitempty"`
	N       int    `json:"n,omitempty"`
	OverCap bool   `json:"over_cap,omitempty"`
	// URL is the PR that a review op names.
	URL string `json:"url,omitempty"`
	// Usage asks ls for each Task's usage.
	Usage bool `json:"usage,omitempty"`
}

type Response struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

type TaskDetail struct {
	TaskView
	Branch string `json:"branch"`
	// Bar is the Landing bar's checklist, one line for each item.
	Bar    []string      `json:"bar,omitempty"`
	Events []store.Event `json:"events"`
}

var ErrNoRepo = errors.New("no Repo mapping")

// drafts is the detail of the drafts flag, which the Board's row shows.
func drafts(n int) string {
	if n == 1 {
		return "1 reply to approve"
	}
	return fmt.Sprintf("%d replies to approve", n)
}

// maxQuestion is the most characters of a Worker's question that the Task keeps.
const maxQuestion = 300

// Serve accepts connections on the socket until ctx ends.
func (s *Scheduler) Serve(ctx context.Context, path string) error {
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.serveConn(ctx, conn)
	}
}

func (s *Scheduler) serveConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	enc := json.NewEncoder(conn)
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var req Request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			enc.Encode(Response{Error: "bad request: " + err.Error()})
			continue
		}
		if req.Op == "watch" {
			s.watch(ctx, enc)
			return
		}
		data, err := s.Handle(ctx, req)
		resp := Response{OK: err == nil}
		if err != nil {
			resp.Error = err.Error()
		} else if data != nil {
			resp.Data, _ = json.Marshal(data)
		}
		if enc.Encode(resp) != nil {
			return
		}
	}
}

func (s *Scheduler) watch(ctx context.Context, enc *json.Encoder) {
	ch := make(chan BoardView, 1)
	s.call(func() { s.watchers[ch] = struct{}{}; ch <- s.view() })
	defer s.call(func() { delete(s.watchers, ch) })
	for {
		select {
		case <-ctx.Done():
			return
		case v := <-ch:
			if enc.Encode(v) != nil {
				return
			}
		}
	}
}

// Handle runs one request. The CLI and the Board reach it through the socket.
// An ID in the request can come in any case.
func (s *Scheduler) Handle(ctx context.Context, req Request) (any, error) {
	req.Issue = CanonicalID(req.Issue)
	switch req.Op {
	case "ls":
		var v BoardView
		var ws []WorkerSpec
		s.call(func() {
			v = s.view()
			if req.Usage {
				for _, t := range v.Tasks {
					ws = append(ws, s.recs[t.ID].usageSpec())
				}
			}
		})
		if req.Usage {
			for i, u := range s.d.Harness.Usage(ctx, ws) {
				v.Tasks[i].Usage = u
			}
		}
		return v, nil
	case "show":
		return s.show(ctx, req.Task)
	case "add":
		return s.add(ctx, req)
	case "next-id":
		return s.nextIssueID(req.Repo)
	case "review":
		return s.review(ctx, req.URL)
	case "reviews":
		return s.reviews(ctx)
	case "poll":
		// The poll outlives the request, so it runs on the Scheduler's context.
		s.call(func() { go s.poll(s.ctx, nil) })
		return nil, nil
	case "reviews-skip":
		return s.skip(ctx, req.URL)
	case "reviews-unskip":
		return s.unskip(req.URL)
	case "reviews-pause", "reviews-resume":
		return nil, s.pauseReviews(req.Op == "reviews-pause")
	case "cap":
		if req.N < 1 {
			return nil, errors.New("the cap must be at least 1")
		}
		var err error
		s.call(func() {
			s.board.Cap = req.N
			err = s.d.Store.SetSetting("wip_cap", strconv.Itoa(req.N))
			s.publish()
		})
		return nil, err
	case "start":
		return nil, s.signal(req.Task, lifecycle.Start{OverCap: req.OverCap})
	case "merge":
		return nil, s.signal(req.Task, lifecycle.PRMerge{})
	case "close":
		return nil, s.with(req.Task, func(r *Record) error {
			if r.Stage == lifecycle.Done && r.WindowID == "" && r.Worktree == "" {
				return nil
			}
			return s.apply(r, lifecycle.TaskClosed{}, "")
		})
	case "archive":
		return nil, s.with(req.Task, func(r *Record) error {
			if r.Stage != lifecycle.Done {
				return &lifecycle.NotAllowedError{Signal: "archive", Stage: r.Stage}
			}
			if !r.Archived {
				if r.keepsWork() {
					if err := s.apply(r, lifecycle.TaskClosed{}, ""); err != nil {
						return err
					}
				}
				r.Archived = true
				s.save(r, store.Event{TaskID: r.ID, At: s.now(), Signal: "archive", From: r.Stage.String(), To: r.Stage.String()})
			}
			return nil
		})
	case "rerun":
		stage, err := lifecycle.ParseStage(req.Stage)
		if err != nil {
			return nil, err
		}
		return nil, s.signal(req.Task, lifecycle.Rerun{Stage: stage})
	case "set":
		return nil, s.with(req.Task, func(r *Record) error {
			if r.Overrides == nil {
				r.Overrides = map[string]string{}
			}
			r.Overrides[req.Key] = req.Value
			s.save(r, store.Event{TaskID: r.ID, At: s.now(), Signal: "set", From: r.Stage.String(), To: r.Stage.String(), Detail: req.Key + "=" + req.Value})
			return nil
		})
	case "ask":
		return nil, s.with(req.Task, func(r *Record) error {
			if r.Job != JobWorker {
				return fmt.Errorf("%s has no live Worker", r.ID)
			}
			if _, met := s.contractOutput(r); met {
				return fmt.Errorf("%s: contract output exists", r.ID)
			}
			if r.Flag != "" && !r.Flag.Transient() {
				return fmt.Errorf("%s is flagged %s", r.ID, r.Flag)
			}
			if req.N > 0 {
				if r.Stage != lifecycle.Responding {
					return fmt.Errorf("%s: Draft replies wait only in Responding", r.ID)
				}
				return s.apply(r, lifecycle.WorkerStatus{State: "blocked", Kind: string(lifecycle.ReasonDrafts), Detail: drafts(req.N)}, "")
			}
			q := req.Text
			if rs := []rune(q); len(rs) > maxQuestion {
				q = string(rs[:maxQuestion])
			}
			return s.apply(r, lifecycle.WorkerStatus{State: "blocked", Kind: string(lifecycle.ReasonQuestion), Detail: q}, "")
		})
	case "open":
		return s.open(req.Task)
	case "steer", "focus", "capture":
		return s.toWorker(ctx, req)
	}
	return nil, fmt.Errorf("unknown op %q", req.Op)
}

// find is the Record of the Task that id names in any case. GitHub names
// ignore case, so owner/repo#12 matches in any case too. Only the loop calls
// it.
func (s *Scheduler) find(id string) (*Record, bool) {
	if r, ok := s.recs[CanonicalID(id)]; ok {
		return r, true
	}
	for k, r := range s.recs {
		if strings.EqualFold(k, id) {
			return r, true
		}
	}
	return nil, false
}

func (s *Scheduler) with(id string, f func(r *Record) error) error {
	var err error
	s.call(func() {
		r, ok := s.find(id)
		if !ok {
			err = fmt.Errorf("%w: %s", lifecycle.ErrUnknownTask, CanonicalID(id))
			return
		}
		err = f(r)
	})
	return err
}

func (s *Scheduler) signal(id string, sig lifecycle.Signal) error {
	return s.with(id, func(r *Record) error { return s.apply(r, sig, "") })
}

func (s *Scheduler) show(ctx context.Context, id string) (TaskDetail, error) {
	var d TaskDetail
	var w WorkerSpec
	err := s.with(id, func(r *Record) error {
		d.TaskView, d.Branch, d.Bar, w = r.view(), r.Branch, r.checklist(), r.usageSpec()
		return nil
	})
	if err != nil {
		return d, err
	}
	d.Usage = s.d.Harness.Usage(ctx, []WorkerSpec{w})[0]
	d.Events, err = s.d.Store.Events(d.ID)
	return d, err
}

func (s *Scheduler) toWorker(ctx context.Context, req Request) (any, error) {
	var r Record
	err := s.with(req.Task, func(p *Record) error {
		r = *p
		if p.BlockID == "" {
			return fmt.Errorf("%s has no open tab", p.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	switch req.Op {
	case "steer":
		if r.Job != JobWorker {
			return nil, fmt.Errorf("%s has no live Worker", r.ID)
		}
		if err := s.d.Rex.Type(ctx, r.SessionID, r.BlockID, req.Text); err != nil {
			return nil, err
		}
		// A restart replays the Stage's steers, so each one is an event.
		return nil, s.with(r.ID, func(p *Record) error {
			s.save(p, store.Event{TaskID: p.ID, At: s.now(), Signal: steerSignal, From: p.Stage.String(), To: p.Stage.String(), Detail: req.Text})
			return nil
		})
	case "focus":
		return nil, s.d.Rex.Focus(ctx, r.SessionID, r.WindowID, r.BlockID)
	}
	return s.d.Rex.Capture(ctx, r.SessionID, r.BlockID)
}

var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

func (s *Scheduler) add(ctx context.Context, req Request) (TaskView, error) {
	r := &Record{Text: strings.TrimSpace(req.Text), CreatedAt: s.now(), StageSince: s.now()}
	var err error
	if req.Repo != "" {
		if r.Repo, err = s.d.Config.ResolveRepo(req.Repo); err != nil {
			return TaskView{}, err
		}
	}
	switch {
	case req.Issue != "":
		is, err := s.d.Tracker.Lookup(ctx, req.Kind, req.Issue)
		if err != nil {
			return TaskView{}, err
		}
		r.Issue, r.ID, r.Title, r.Branch = &is, is.Identifier, is.Title, is.BranchName
		if r.Repo == "" {
			if r.Repo, err = s.d.Config.RepoForIssue(is); err != nil {
				return TaskView{}, fmt.Errorf("%w: pass a repo", err)
			}
		}
		switch key, tracker := s.d.Config.RepoTracker(r.Repo); tracker {
		case is.Tracker():
		case "":
			return TaskView{}, fmt.Errorf("repo has no tracker: %s", key)
		default:
			return TaskView{}, fmt.Errorf("%s takes Issues only from its Tracker %s, and %s is a %s Issue", key, tracker, is.Identifier, is.Tracker())
		}
	case r.Text != "":
		if r.Repo == "" {
			return TaskView{}, errors.New("a Task without an Issue needs a repo")
		}
		r.Title = textTitle(r.Text)
	default:
		return TaskView{}, errors.New("add needs an Issue or a text")
	}
	policy, err := s.d.Config.Policy(r.Repo, nil)
	if err != nil {
		return TaskView{}, err
	}
	var v TaskView
	err = s.withLoop(func() error {
		if r.ID == "" {
			slug := strings.Trim(notSlug.ReplaceAllString(strings.ToLower(r.Title), "-"), "-")
			if len(slug) > 40 {
				slug = strings.Trim(slug[:40], "-")
			}
			if prefix := s.d.Config.RepoPrefix(r.Repo); prefix != "" {
				id, err := s.nextLocalID(prefix)
				if err != nil {
					return err
				}
				r.ID, r.Branch = id, strings.TrimSuffix(strings.ToLower(id)+"-"+slug, "-")
			} else {
				r.ID = s.nextID("AH")
				r.Branch = "lineup/" + strings.ToLower(r.ID) + "-" + slug
			}
		}
		if old, ok := s.find(r.ID); ok {
			if r.Issue != nil && (old.Issue == nil || old.Issue.Tracker() != r.Issue.Tracker()) {
				return fmt.Errorf("%s is already the ID of another Task: give the %s Issue another ID", r.ID, r.Issue.Tracker())
			}
			if old.keepsTab() {
				return fmt.Errorf("%s is Done and keeps its tab: close its tab first with lineup close %s", r.ID, r.ID)
			}
			if old.keepsWork() {
				return fmt.Errorf("%s is Done and keeps its worktree: remove it first with lineup close %s", r.ID, r.ID)
			}
			if old.Stage != lifecycle.Done {
				return fmt.Errorf("%s is already on the Board", r.ID)
			}
		}
		r.RepoLabel = sessionLabel(r.Repo, s.repos())
		r.Session = r.RepoLabel
		r.Task = *s.board.Add(r.ID, policy)
		s.board.Tasks[r.ID] = &r.Task
		s.recs[r.ID] = r
		s.save(r, store.Event{TaskID: r.ID, At: s.now(), Signal: "add", To: lifecycle.Queued.String()})
		v = r.view()
		return nil
	})
	return v, err
}

func (s *Scheduler) withLoop(f func() error) error {
	var err error
	s.call(func() { err = f() })
	return err
}

// nextID takes the next number of the ad hoc Task counter. Only the loop calls it.
func (s *Scheduler) nextID(prefix string) string {
	n := 1
	if cur, _ := s.d.Store.Setting("next_id"); cur != "" {
		n, _ = strconv.Atoi(cur)
	}
	s.d.Store.SetSetting("next_id", strconv.Itoa(n+1))
	return fmt.Sprintf("%s-%d", prefix, n)
}

// nextLocalID is <PREFIX>-<n>, with n one more than the highest n of the
// prefix among the Local Issues and every Task in the store. Only the loop
// calls it.
func (s *Scheduler) nextLocalID(prefix string) (string, error) {
	hi := 0
	if s.d.LocalIssues != nil {
		var err error
		if hi, err = s.d.LocalIssues.Highest(prefix); err != nil {
			return "", fmt.Errorf("read the Local Issues: %w", err)
		}
	}
	for id := range s.recs {
		if p, n, ok := SplitID(id); ok && p == prefix {
			hi = max(hi, n)
		}
	}
	return fmt.Sprintf("%s-%d", strings.ToUpper(prefix), hi+1), nil
}

// nextIssueID is the ID that the next Local Issue of the named Repo takes:
// <PREFIX>-<n> for a Repo with a prefix, and LOCAL-<n> for any other Repo
// or no name.
func (s *Scheduler) nextIssueID(name string) (string, error) {
	prefix := KindLocal
	if name != "" {
		repo, err := s.d.Config.ResolveRepo(name)
		if err != nil {
			return "", err
		}
		prefix = cmp.Or(s.d.Config.RepoPrefix(repo), prefix)
	}
	var id string
	err := s.withLoop(func() error {
		var err error
		id, err = s.nextLocalID(prefix)
		return err
	})
	return id, err
}

// repos lists the Repo of every Task. Only the loop calls it.
func (s *Scheduler) repos() []string {
	var out []string
	for _, o := range s.recs {
		out = append(out, o.Repo)
	}
	return out
}
