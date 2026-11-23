// Package route picks the one Repo where a Linear Issue's work happens, when
// the Issue's team is on more than one Repo.
package route

import (
	"context"
	"regexp"
	"strings"
)

// Clues are what an Issue says about its Repo.
type Clues struct {
	Title, Description string
	Labels             []string
	// Links are the URLs of the Issue's attachments, for example its PRs.
	Links []string
}

// Step is the step of the routing that picked the Repo.
type Step string

const (
	One     Step = "one candidate"
	Named   Step = "the Issue names it"
	Project Step = "the Project's Repo"
	Linked  Step = "links"
	Content Step = "content"
)

// Writes reports whether lineup writes the Repo line into the Issue after the
// step, so that the next start takes the Named step.
func (s Step) Writes() bool { return s == Linked || s == Content }

// Judge reads the Issue against the candidates and names one of them. It
// returns "" when it is not sure, and when it fails.
type Judge func(ctx context.Context, c Clues, candidates []string) string

type Inputs struct {
	// Candidates are the keys of the Repos whose teams list holds the
	// Issue's team, for example crexi/data.
	Candidates []string
	// Project is the key of the Repo that the user picked for the Issue's
	// Project, or "".
	Project string
	Clues   func(context.Context) (Clues, error)
	Judge   Judge
}

// Choose picks one candidate with the first step that names exactly one. A
// key of "" means that no step did, so the user must pick.
func Choose(ctx context.Context, in Inputs) (key string, step Step, err error) {
	if len(in.Candidates) == 1 {
		return in.Candidates[0], One, nil
	}
	c, err := in.Clues(ctx)
	if err != nil {
		return "", "", err
	}
	if k := NamedRepo(c, in.Candidates); k != "" {
		return k, Named, nil
	}
	if k := only(in.Candidates, []string{in.Project}); k != "" {
		return k, Project, nil
	}
	if k := only(in.Candidates, links(c)); k != "" {
		return k, Linked, nil
	}
	if in.Judge != nil {
		if k := only(in.Candidates, []string{in.Judge(ctx, c, in.Candidates)}); k != "" {
			return k, Content, nil
		}
	}
	return "", "", nil
}

// repoLine is a line "Repo: <owner>/<repo>" in a description.
var repoLine = regexp.MustCompile("(?mi)^[ \t]*Repo:[ \t]*`?([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)`?[ \t]*$")

const labelPrefix = "repo:"

// NamedRepo is the one candidate that the Issue's Repo lines and repo:<name>
// labels name, or "". A label names a Repo by its key or by its repo name.
func NamedRepo(c Clues, candidates []string) string {
	var names []string
	for _, m := range repoLine.FindAllStringSubmatch(c.Description, -1) {
		names = append(names, m[1])
	}
	for _, l := range c.Labels {
		if len(l) > len(labelPrefix) && strings.EqualFold(l[:len(labelPrefix)], labelPrefix) {
			names = append(names, strings.TrimSpace(l[len(labelPrefix):]))
		}
	}
	return only(candidates, names)
}

var githubURL = regexp.MustCompile(`(?i)https?://(?:www\.)?github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)`)

// links are the owner/repo keys of the GitHub URLs in the description and
// in the attachments.
func links(c Clues) []string {
	var keys []string
	for _, text := range append([]string{c.Description}, c.Links...) {
		for _, m := range githubURL.FindAllStringSubmatch(text, -1) {
			keys = append(keys, m[1]+"/"+strings.TrimSuffix(m[2], ".git"))
		}
	}
	return keys
}

// only is the one candidate that the names match, or "". A name with a slash
// matches a key, and a name without one matches the repo part of a key.
// Case is ignored.
func only(candidates, names []string) string {
	found := ""
	for _, c := range candidates {
		_, repo, _ := strings.Cut(c, "/")
		for _, n := range names {
			if strings.EqualFold(n, c) || !strings.Contains(n, "/") && n != "" && strings.EqualFold(n, repo) {
				if found != "" {
					return ""
				}
				found = c
				break
			}
		}
	}
	return found
}

// WithRepoLine is the description with one Repo line, for the key, at its
// end. It drops each Repo line that the description had.
func WithRepoLine(description, key string) string {
	d := strings.TrimRight(repoLine.ReplaceAllString(description, ""), " \t\n")
	if d == "" {
		return "Repo: " + key
	}
	return d + "\n\nRepo: " + key
}
