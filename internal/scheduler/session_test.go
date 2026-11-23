package scheduler

import "testing"

func TestTheBoardNamesARepoAfterItsFolder(t *testing.T) {
	repos := []string{"/r/acme/data", "/r/other/data", "/r/acme/web", "/r/gclunies/my-lineup"}
	for repo, want := range map[string]string{
		"/r/acme/web":           "web",
		"/r/acme/data":          "acme-data",
		"/r/gclunies/my-lineup": "my-lineup",
	} {
		if got := repoLabel(repo, repos); got != want {
			t.Errorf("repoLabel(%s) = %q, want %q", repo, got, want)
		}
	}
}
