package scheduler

import "testing"

func TestARepoSessionIsNamedAfterItsFolder(t *testing.T) {
	repos := []string{"/r/acme/data", "/r/other/data", "/r/acme/web", "/r/gclunies/my-lineup"}
	for repo, want := range map[string]string{
		"/r/acme/web":           "web",
		"/r/acme/data":          "acme-data",
		"/r/gclunies/my-lineup": "gclunies-my-lineup",
	} {
		if got := sessionLabel(repo, repos); got != want {
			t.Errorf("sessionLabel(%s) = %q, want %q", repo, got, want)
		}
	}
}
