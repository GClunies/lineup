package scheduler

import (
	"strings"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
)

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
