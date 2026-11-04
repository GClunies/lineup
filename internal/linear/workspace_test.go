package linear

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	acme   = Workspace{ID: "org-acme", Name: "Acme", URLKey: "acme"}
	globex = Workspace{ID: "org-globex", Name: "Globex", URLKey: "globex"}
)

func useStateDir(t *testing.T, dir string) {
	t.Helper()
	old := stateDir
	stateDir = func() string { return dir }
	t.Cleanup(func() { stateDir = old })
}

// fakeStores swaps the keychain for memory, one item per account, and answers
// whoami from the access token: "acme-…" is Acme, "dead-…" is revoked, and
// anything else is Globex.
func fakeStores(t *testing.T, initial map[string]*tokens) map[string]*tokens {
	t.Helper()
	useStateDir(t, t.TempDir())
	var mu sync.Mutex
	items := map[string]*tokens{}
	for k, v := range initial {
		items[k] = v
	}
	oldR, oldW, oldD, oldWho := readStore, writeStore, removeStore, whoami
	readStore = func(acct string) (*tokens, error) {
		mu.Lock()
		defer mu.Unlock()
		if items[acct] == nil {
			return nil, ErrSignedOut
		}
		c := *items[acct]
		return &c, nil
	}
	writeStore = func(acct string, nt *tokens) error {
		mu.Lock()
		defer mu.Unlock()
		c := *nt
		items[acct] = &c
		return nil
	}
	removeStore = func(acct string) error { mu.Lock(); defer mu.Unlock(); delete(items, acct); return nil }
	whoami = func(_ context.Context, token string) (Workspace, error) {
		switch {
		case strings.HasPrefix(token, "acme"):
			return acme, nil
		case strings.HasPrefix(token, "dead"):
			return Workspace{}, ErrSignedOut
		}
		return globex, nil
	}
	t.Cleanup(func() { readStore, writeStore, removeStore, whoami = oldR, oldW, oldD, oldWho })
	return items
}

func live() *tokens {
	return &tokens{AccessToken: "acme-a", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)}
}

func withIndex(t *testing.T, ix workspaceIndex) {
	t.Helper()
	unlock, err := lockTokens(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := updateIndexLocked(func(cur *workspaceIndex) { *cur = ix }); err != nil {
		t.Fatal(err)
	}
}

func TestLoginAddsTheWorkspace(t *testing.T) {
	items := fakeStores(t, map[string]*tokens{account(acme.ID): live()})
	withIndex(t, workspaceIndex{Workspaces: []Workspace{acme}})
	fakeTokenServer(t, func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "globex-a", "refresh_token": "globex-r", "expires_in": 3600}
	})
	fakeBrowser(t, func(u *url.URL) { callback(t, url.Values{"code": {"c"}, "state": {u.Query().Get("state")}}) })
	w, err := Login(context.Background(), UI{}, "", "")
	if err != nil || w.ID != globex.ID {
		t.Fatalf("%+v %v", w, err)
	}
	if items[account(globex.ID)] == nil || items[account(globex.ID)].AccessToken != "globex-a" || items[account(acme.ID)] == nil {
		t.Fatalf("stored %v", items)
	}
	if ws, _ := Workspaces(); len(ws) != 2 {
		t.Fatalf("workspaces %+v", ws)
	}
}

func TestLoginListsTheWorkspaceBeforeStoringItsTokens(t *testing.T) {
	fakeStores(t, nil)
	writeStore = func(string, *tokens) error { return errors.New("keychain locked") }
	fakeTokenServer(t, func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "acme-a", "refresh_token": "r", "expires_in": 3600}
	})
	fakeBrowser(t, func(u *url.URL) { callback(t, url.Values{"code": {"c"}, "state": {u.Query().Get("state")}}) })
	if _, err := Login(context.Background(), UI{}, "", ""); err == nil {
		t.Fatal("want the keychain error")
	}
	// Listed without tokens, it asks for a new sign-in. Nothing stays hidden.
	if ws, _ := Workspaces(); len(ws) != 1 {
		t.Fatalf("workspaces %+v", ws)
	}
}

func TestLogoutEveryWorkspaceKeepsTheOnesThatFail(t *testing.T) {
	items := fakeStores(t, map[string]*tokens{
		account(acme.ID):   live(),
		account(globex.ID): {AccessToken: "globex-a", RefreshToken: "", ExpiresAt: time.Now()}, // can't refresh to revoke
	})
	withIndex(t, workspaceIndex{Workspaces: []Workspace{acme, globex}})
	fakeRevoke(t, 200)
	done, err := Logout(context.Background(), "", false)
	if len(done) != 1 || done[0] != "Acme" || err == nil || !strings.Contains(err.Error(), "Globex") {
		t.Fatalf("done %v err %v", done, err)
	}
	if items[account(acme.ID)] != nil || items[account(globex.ID)] == nil {
		t.Fatalf("stored %v", items)
	}
	if ws, _ := Workspaces(); len(ws) != 1 || ws[0].ID != globex.ID {
		t.Fatalf("workspaces %+v", ws)
	}
}

func TestLogoutOneWorkspaceDropsItsCache(t *testing.T) {
	items := fakeStores(t, map[string]*tokens{account(acme.ID): live(), account(globex.ID): live()})
	withIndex(t, workspaceIndex{Workspaces: []Workspace{acme, globex}})
	fakeRevoke(t, 200)
	WriteCache(acme.ID+"/my", []Issue{{Identifier: "A-1"}})
	WriteCache(globex.ID+"/my", []Issue{{Identifier: "G-1"}})
	if done, err := Logout(context.Background(), "globex", false); err != nil || len(done) != 1 {
		t.Fatalf("%v %v", done, err)
	}
	if items[account(acme.ID)] == nil || items[account(globex.ID)] != nil {
		t.Fatalf("stored %v", items)
	}
	if _, ok := ReadCached(globex.ID + "/my"); ok {
		t.Error("Globex's cache survived its sign-out")
	}
	if _, ok := ReadCached(acme.ID + "/my"); !ok {
		t.Error("Acme's cache went with Globex")
	}
	if _, err := Logout(context.Background(), "initech", false); err == nil {
		t.Fatal("an unknown workspace")
	}
}

func TestLogoutUnlistsAWorkspaceWithNoTokens(t *testing.T) {
	fakeStores(t, map[string]*tokens{account(acme.ID): live()})
	withIndex(t, workspaceIndex{Workspaces: []Workspace{acme, globex}})
	fakeRevoke(t, 200)
	done, err := Logout(context.Background(), "", false)
	if err != nil || len(done) != 1 {
		t.Fatalf("%v %v", done, err)
	}
	if ws, _ := Workspaces(); len(ws) != 0 {
		t.Fatalf("workspaces %+v", ws)
	}
}

func TestAClientUsesItsOwnWorkspacesTokens(t *testing.T) {
	fakeStores(t, map[string]*tokens{account(globex.ID): live()})
	if _, err := NewClient(acme).MyIssues(context.Background()); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("Acme's client got past with Globex's tokens: %v", err)
	}
}

func TestABrokenIndexIsSetAside(t *testing.T) {
	fakeStores(t, nil)
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath(), []byte(`{"workspaces": [`), 0o600); err != nil {
		t.Fatal(err)
	}
	if ws, err := Workspaces(); err != nil || len(ws) != 0 {
		t.Fatalf("%+v %v", ws, err)
	}
	if _, err := os.Stat(indexPath() + ".broken"); err != nil {
		t.Fatal("not kept for a look")
	}
	withIndex(t, workspaceIndex{Workspaces: []Workspace{acme}})
}

func TestFindByURLKeyOrUniqueName(t *testing.T) {
	ix := workspaceIndex{Workspaces: []Workspace{acme, globex, {ID: "d1", Name: "Dup", URLKey: "dup-1"}, {ID: "d2", Name: "Dup", URLKey: "dup-2"}}}
	if w, err := ix.find("GLOBEX"); err != nil || w.ID != globex.ID {
		t.Fatalf("%v %v", w, err)
	}
	if w, err := ix.find("dup-2"); err != nil || w.ID != "d2" {
		t.Fatalf("%v %v", w, err)
	}
	if _, err := ix.find("dup"); err == nil || !strings.Contains(err.Error(), "URL key") {
		t.Fatalf("two workspaces named Dup: %v", err)
	}
}
