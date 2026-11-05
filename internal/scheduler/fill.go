package scheduler

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
)

// membersEvery is how long the Scheduler keeps the members of a team.
const membersEvery = time.Hour

// fill starts the review Tasks that the queues want. Only one fill runs at a
// time: a fill that is due while another runs is skipped. The candidate
// check, the count, the add, and the start happen in one loop call, so a
// second fill in the same state starts nothing.
func (s *Scheduler) fill(ctx context.Context) {
	if len(s.d.Queues) == 0 || !s.filling.CompareAndSwap(false, true) {
		return
	}
	defer s.filling.Store(false)
	rows, err := s.readQueues(ctx)
	if err != nil {
		s.d.Log.Error("review queues", "err", err)
		return
	}
	if len(rows.Skipped) > 0 {
		s.d.Log.Warn("review queues skip PRs", "why", strings.Join(rows.Skipped, "; "))
	}
	s.call(func() { s.applyFill(planFill(s.fillInput(rows))) })
}

// queueRows is what a fill reads outside the loop: each queue's search rows,
// the Repo of each GitHub repository that has a candidate, and why the other
// repositories have none.
type queueRows struct {
	Found   map[string][]github.PullRequest
	Repos   map[string]queueRepo
	Skipped []string
}

// readQueues searches every queue in one GraphQL call, then finds the Repo of
// each repository that has a candidate. It reads GitHub and files, so only
// lanes and API handlers call it, never the loop.
func (s *Scheduler) readQueues(ctx context.Context) (queueRows, error) {
	members, err := s.teamMembers(ctx)
	if err != nil {
		return queueRows{}, err
	}
	var searches []github.Search
	for _, q := range s.d.Queues {
		// With every author team empty, the search would match every author.
		if query, ok := q.Filter.Query(members); ok {
			searches = append(searches, github.Search{Key: q.Name, Name: "Q: " + q.Name, Query: query})
		}
	}
	found, err := s.d.GitHub.SearchPRs(ctx, searches)
	if err != nil {
		return queueRows{}, err
	}
	rows := queueRows{Found: found, Repos: map[string]queueRepo{}}
	tried := map[string]bool{}
	for _, q := range s.d.Queues {
		for _, c := range github.Candidates(found[q.Name], nil) {
			slug := strings.ToLower(c.PR.Repo)
			if tried[slug] {
				continue
			}
			tried[slug] = true
			repo, policy, err := s.reviewRepo(c.PR.Repo)
			if err != nil {
				rows.Skipped = append(rows.Skipped, err.Error())
				continue
			}
			rows.Repos[slug] = queueRepo{Repo: repo, Policy: policy}
		}
	}
	return rows, nil
}

// teamMembers reads the members of the queues' author teams once an hour.
func (s *Scheduler) teamMembers(ctx context.Context) (map[string][]string, error) {
	var teams []string
	for _, q := range s.d.Queues {
		for _, t := range q.Filter.AuthorTeams {
			if !slices.Contains(teams, t) {
				teams = append(teams, t)
			}
		}
	}
	if len(teams) == 0 {
		return nil, nil
	}
	var members map[string][]string
	var at time.Time
	s.call(func() { members, at = s.members, s.membersAt })
	if s.now().Sub(at) < membersEvery {
		return members, nil
	}
	members, err := s.d.GitHub.TeamMembers(ctx, teams)
	if err != nil {
		return nil, err
	}
	at = s.now()
	s.call(func() { s.members, s.membersAt = members, at })
	return members, nil
}

// fillInput is the state that planFill reads. Only the loop calls it.
func (s *Scheduler) fillInput(rows queueRows) fillInput {
	return fillInput{Queues: s.d.Queues, Records: s.list(), Found: rows.Found, Repos: rows.Repos}
}

// applyFill starts and adds what the plan names. Only the loop calls it.
func (s *Scheduler) applyFill(p fillPlan) {
	for _, id := range p.Start {
		if err := s.apply(s.recs[id], lifecycle.Start{}, ""); err != nil {
			s.d.Log.Error("review queue start", "task", id, "err", err)
		}
	}
	for _, a := range p.Add {
		pr := a.PR
		_, err := s.addReview(reviewAdd{
			PR:    ReviewPR{Repo: pr.Repo, Number: pr.Number, URL: pr.URL, Head: pr.Head, Base: pr.Base},
			Title: pr.Title, Repo: a.Repo.Repo, Policy: a.Repo.Policy, Queue: a.Queue,
		})
		if err != nil {
			s.d.Log.Error("review queue add", "queue", a.Queue, "pr", pr.URL, "err", err)
		}
	}
}

// Queue is a [[review.queues]] entry: the filter of its search, and how many
// of its review Tasks can be started at one time.
type Queue struct {
	Name   string
	Filter github.Filter
	Slots  int
}

// queueRepo is the Repo whose main checkout reviews a GitHub repository's
// PRs, and that Repo's policy.
type queueRepo struct {
	Repo   string
	Policy lifecycle.Policy
}

// fillInput is everything that one fill decides from.
type fillInput struct {
	Paused bool
	// Queues are the configured queues, in config order.
	Queues []Queue
	// Records is every Task, oldest first.
	Records []*Record
	// Found is each queue's search rows, by queue name.
	Found map[string][]github.PullRequest
	// Repos maps each GitHub repository, owner/name in lower case, to its
	// Repo. A repository with no [[repos]] checkout is absent.
	Repos map[string]queueRepo
}

// fillPlan is what one fill does: it starts the Queued review Tasks in
// Start, then adds and starts a review Task for each PR in Add. Ranked is
// each queue's candidates that have a Repo, in rank order.
type fillPlan struct {
	Ranked map[string][]github.Candidate
	Start  []string
	Add    []fillAdd
}

type fillAdd struct {
	Queue string
	PR    github.PullRequest
	Repo  queueRepo
}

// planFill decides what one fill starts. Each queue starts its Queued review
// Tasks first, then its top-ranked candidates, until its started count
// reaches its slots. A fill starts at most one review in each Repo, because
// each start fetches into the Repo's main checkout. So a PR that two queues
// find gets one review Task.
func planFill(in fillInput) fillPlan {
	var tasks []github.ReviewTask
	for _, r := range in.Records {
		if t, ok := r.reviewTask(); ok {
			tasks = append(tasks, t)
		}
	}
	p := fillPlan{Ranked: map[string][]github.Candidate{}}
	for _, q := range in.Queues {
		for _, c := range github.Candidates(in.Found[q.Name], tasks) {
			if _, ok := in.Repos[strings.ToLower(c.PR.Repo)]; ok {
				p.Ranked[q.Name] = append(p.Ranked[q.Name], c)
			}
		}
	}
	if in.Paused {
		return p
	}
	busy := map[string]bool{}
	for _, q := range in.Queues {
		free := q.Slots
		var queued []*Record
		for _, r := range in.Records {
			switch {
			case r.Review == nil || r.Queue != q.Name:
			case r.Stage == lifecycle.Queued:
				queued = append(queued, r)
			case r.Stage.Started():
				free--
			}
		}
		for _, r := range queued {
			if free <= 0 {
				break
			}
			if !busy[r.Repo] {
				p.Start = append(p.Start, r.ID)
				busy[r.Repo] = true
				free--
			}
		}
		for _, c := range p.Ranked[q.Name] {
			if free <= 0 {
				break
			}
			repo := in.Repos[strings.ToLower(c.PR.Repo)]
			if busy[repo.Repo] {
				continue
			}
			p.Add = append(p.Add, fillAdd{Queue: q.Name, PR: c.PR, Repo: repo})
			busy[repo.Repo] = true
			free--
		}
	}
	return p
}
