package scheduler

import (
	"bufio"
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
}

type Response struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

type TaskDetail struct {
	TaskView
	Branch string        `json:"branch"`
	Events []store.Event `json:"events"`
}

var ErrNoRepo = errors.New("no Repo mapping")

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
func (s *Scheduler) Handle(ctx context.Context, req Request) (any, error) {
	switch req.Op {
	case "ls":
		var v BoardView
		s.call(func() { v = s.view() })
		return v, nil
	case "show":
		return s.show(req.Task)
	case "add":
		return s.add(ctx, req)
	case "review":
		return s.review(ctx, req.URL)
	case "reviews":
		return s.reviews(ctx)
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
		return nil, s.signal(req.Task, lifecycle.TaskClosed{})
	case "archive":
		return nil, s.with(req.Task, func(r *Record) error {
			if r.Stage != lifecycle.Done {
				return &lifecycle.NotAllowedError{Signal: "archive", Stage: r.Stage}
			}
			if !r.Archived {
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
	case "steer", "focus", "capture":
		return s.toWorker(ctx, req)
	}
	return nil, fmt.Errorf("unknown op %q", req.Op)
}

func (s *Scheduler) with(id string, f func(r *Record) error) error {
	var err error
	s.call(func() {
		r, ok := s.recs[id]
		if !ok {
			err = fmt.Errorf("%w: %s", lifecycle.ErrUnknownTask, id)
			return
		}
		err = f(r)
	})
	return err
}

func (s *Scheduler) signal(id string, sig lifecycle.Signal) error {
	return s.with(id, func(r *Record) error { return s.apply(r, sig, "") })
}

func (s *Scheduler) show(id string) (TaskDetail, error) {
	var d TaskDetail
	err := s.with(id, func(r *Record) error {
		d.TaskView, d.Branch = r.view(), r.Branch
		return nil
	})
	if err != nil {
		return d, err
	}
	d.Events, err = s.d.Store.Events(id)
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
		return nil, s.d.Rex.Type(ctx, r.SessionID, r.BlockID, req.Text)
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
			var ok bool
			if r.Repo, ok = s.d.Config.RepoForIssue(is); !ok {
				return TaskView{}, fmt.Errorf("%w for %s: pass a repo", ErrNoRepo, is.Identifier)
			}
		}
	case r.Text != "":
		if r.Repo == "" {
			return TaskView{}, errors.New("a Task without an Issue needs a repo")
		}
		r.Title = r.Text
		if len(r.Title) > 60 {
			r.Title = r.Title[:60]
		}
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
			r.ID = s.nextID("B")
			slug := strings.Trim(notSlug.ReplaceAllString(strings.ToLower(r.Title), "-"), "-")
			if len(slug) > 40 {
				slug = strings.Trim(slug[:40], "-")
			}
			r.Branch = "lineup/" + strings.ToLower(r.ID) + "-" + slug
		}
		if old, ok := s.recs[r.ID]; ok && old.Stage != lifecycle.Done {
			return fmt.Errorf("%s is already on the Board", r.ID)
		}
		r.RepoLabel = sessionLabel(r.Repo, s.repos())
		r.Session = r.RepoLabel
		if r.Issue == nil {
			r.Session = s.d.AdHocSession
		}
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

// nextID takes the next number of the counter that ad hoc and review Tasks
// share. Only the loop calls it.
func (s *Scheduler) nextID(prefix string) string {
	n := 1
	if cur, _ := s.d.Store.Setting("next_id"); cur != "" {
		n, _ = strconv.Atoi(cur)
	}
	s.d.Store.SetSetting("next_id", strconv.Itoa(n+1))
	return fmt.Sprintf("%s-%d", prefix, n)
}

// repos lists the Repo of every Task. Only the loop calls it.
func (s *Scheduler) repos() []string {
	var out []string
	for _, o := range s.recs {
		out = append(out, o.Repo)
	}
	return out
}
