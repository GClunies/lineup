package linear

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gclunies/lineup/internal/termsafe"
)

const (
	authorizeURL = "https://linear.app/oauth/authorize"
	// defaultClientID is the OAuth client (PKCE, no secret) registered in
	// Linear as "lineup", public so any workspace can sign in. Linear matches
	// the redirect exactly, so the port is fixed.
	defaultClientID = "99bd4fc40a03d825270c9a99593136df"
	callbackPort    = 47821
	redirectURI     = "http://localhost:47821/callback"
	scopes          = "read,write"
)

// Seams for tests: the endpoints, the browser, and the secret store.
var (
	tokenURL    = "https://api.linear.app/oauth/token"
	revokeURL   = "https://api.linear.app/oauth/revoke"
	browse      = openBrowser
	noBrowser   = overSSH
	readStore   = loadTokens
	writeStore  = saveTokens
	removeStore = deleteTokens
)

// httpClient's timeout bounds a stalled connection, proxy or TLS handshake.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// ErrSignedOut means there are no usable credentials: run `lineup login`.
var ErrSignedOut = errors.New("not signed in to Linear")

// ErrNotSignedIn: Logout found nothing to sign out of.
var ErrNotSignedIn = errors.New("not signed in")

type tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	// ClientID is the OAuth app that issued the tokens. Linear refreshes them
	// only for that app.
	ClientID string `json:"client_id,omitempty"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func account(workspaceID string) string { return "oauth:" + workspaceID }

func fresh(t *tokens) bool { return time.Until(t.ExpiresAt) > 5*time.Minute }

func accessToken(ctx context.Context, acct string, forceRefresh bool) (string, error) {
	t, err := readStore(acct)
	if err != nil {
		return "", err
	}
	if !forceRefresh && fresh(t) {
		return t.AccessToken, nil
	}
	// Linear rotates the refresh token on every use. Two processes refreshing
	// at once could each store a generation, and the older one written last
	// would be dead a day later. So refreshes take turns, and whoever waited
	// re-reads first: tokens that changed meanwhile are the ones to use.
	unlock, err := lockTokens(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	cur, err := readStore(acct)
	if err != nil {
		return "", err
	}
	if (cur.AccessToken != t.AccessToken || !forceRefresh) && fresh(cur) {
		return cur.AccessToken, nil
	}
	nt, err := refreshGrant(ctx, cur)
	if err != nil {
		return "", err
	}
	if err := writeStore(acct, nt); err != nil {
		return "", err
	}
	return nt.AccessToken, nil
}

// refreshGrant spends cur's refresh token for a new pair, without storing it.
func refreshGrant(ctx context.Context, cur *tokens) (*tokens, error) {
	if cur.RefreshToken == "" {
		return nil, ErrSignedOut
	}
	client := cur.ClientID
	if client == "" { // tokens from before ClientID
		client = defaultClientID
	}
	resp, err := postToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {cur.RefreshToken},
		"client_id":     {client},
	})
	if err != nil {
		return nil, err
	}
	return resp.toTokens(cur.RefreshToken, client), nil
}

func (r *tokenResponse) toTokens(previousRefresh, client string) *tokens {
	refresh := r.RefreshToken
	if refresh == "" {
		refresh = previousRefresh
	}
	expires := r.ExpiresIn
	if expires <= 0 {
		expires = 24 * 3600
	}
	return &tokens{AccessToken: r.AccessToken, RefreshToken: refresh, ExpiresAt: time.Now().Add(time.Duration(expires) * time.Second), ClientID: client}
}

type oauthError struct {
	Status int
	Code   string
	Desc   string
}

func (e *oauthError) Error() string {
	if e.Desc != "" {
		return fmt.Sprintf("Linear OAuth %d: %s (%s)", e.Status, e.Code, e.Desc)
	}
	return fmt.Sprintf("Linear OAuth %d: %s", e.Status, e.Code)
}

func postToken(ctx context.Context, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
			Desc  string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error == "invalid_grant" {
			return nil, ErrSignedOut // revoked or expired: only a new sign-in helps
		}
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(body))
		}
		return nil, &oauthError{res.StatusCode, termsafe.Clean(e.Error, false), termsafe.Clean(e.Desc, false)}
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil || tr.AccessToken == "" {
		return nil, errors.New("Linear OAuth: unexpected token response")
	}
	return &tr, nil
}

// ── sign in: authorization code + PKCE through a loopback callback ────────────

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// UI is whoever watches a sign-in, for example the `lineup login` command.
type UI struct {
	// Status reports a progress line.
	Status func(string)
	// Link hands over the approval page once, and says whether a browser was
	// opened on it. Over SSH no browser opens, so the user must open the link.
	Link func(authURL string, opened bool)
}

func (ui UI) say(s string) {
	if ui.Status != nil {
		ui.Status(s)
	}
}

// callbackResult reads Linear's answer from the callback's query. ours is
// false when the request does not answer this sign-in (another state, a stale
// tab): ignore it, do not fail on it.
func callbackResult(q url.Values, state string) (code string, ours bool, err error) {
	switch {
	case q.Get("state") != state:
		return "", false, nil
	case q.Get("error") != "":
		return "", true, fmt.Errorf("Linear declined the sign-in: %s", termsafe.Clean(q.Get("error"), false))
	case q.Get("code") == "":
		return "", true, errors.New("Linear sent no authorization code")
	}
	return q.Get("code"), true, nil
}

// Login runs the browser sign-in through the OAuth app client, or lineup's own
// app when client is empty, and stores the result under its workspace. When
// want is set, the user must pick the workspace with that URL key.
func Login(ctx context.Context, ui UI, want, client string) (Workspace, error) {
	if client == "" {
		client = defaultClientID
	}
	verifier := randomToken(48)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state := randomToken(24)

	// "localhost" may resolve to either loopback family: IPv4 is required,
	// IPv6 best-effort.
	v4, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", callbackPort))
	if err != nil {
		return Workspace{}, fmt.Errorf("can't listen on port %d for the sign-in callback (another sign-in open?): %w", callbackPort, err)
	}
	listeners := []net.Listener{v4}
	if v6, err := net.Listen("tcp", fmt.Sprintf("[::1]:%d", callbackPort)); err == nil {
		listeners = append(listeners, v6)
	}

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		var res result
		var ours bool
		res.code, ours, res.err = callbackResult(r.URL.Query(), state)
		if !ours {
			http.Error(w, "Unknown sign-in request. Start again from lineup.", http.StatusBadRequest)
			return
		}
		// lineup finishes the sign-in after this page, so it claims no more
		// than Linear's approval.
		msg := "Linear approved access. You can close this tab: lineup finishes signing in, and says so if anything goes wrong."
		if res.err != nil {
			msg = "Sign-in failed: " + res.err.Error()
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>lineup · Linear</title>`+
			`<body style="font:16px system-ui;margin:4rem auto;max-width:32rem">%s</body>`, html.EscapeString(msg))
		// Flushed before the result is handed over: once it is, sign-in may
		// stop the server before the browser has its page.
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case done <- res:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	for _, l := range listeners {
		go func(l net.Listener) { _ = srv.Serve(l) }(l)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if srv.Shutdown(sctx) != nil {
			srv.Close()
		}
	}()

	q := url.Values{
		"client_id":             {client},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {scopes},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		// The consent screen is where the user chooses which workspace to sign in to.
		"prompt": {"consent"},
	}
	authURL := authorizeURL + "?" + q.Encode()
	opened := false
	if !noBrowser() {
		ui.say("Opening Linear in your browser…")
		opened = browse(authURL) == nil
	}
	if ui.Link != nil {
		ui.Link(authURL, opened)
	}
	ui.say("Waiting for you to approve access in the browser…")

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		return Workspace{}, errors.New("sign-in timed out or was cancelled")
	}
	if res.err != nil {
		return Workspace{}, res.err
	}

	tr, err := postToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {res.code},
		"redirect_uri":  {redirectURI},
		"client_id":     {client},
		"code_verifier": {verifier},
	})
	if err != nil {
		return Workspace{}, err
	}
	w, err := whoami(ctx, tr.AccessToken)
	if err != nil {
		return Workspace{}, fmt.Errorf("signed in, but couldn't tell which workspace: %w", err)
	}
	if want != "" && !strings.EqualFold(w.URLKey, want) {
		return Workspace{}, fmt.Errorf("you picked %s (%s) in Linear, not %s. Nothing changed: run `lineup login %s` again and pick %s", w.Name, w.URLKey, want, want, want)
	}
	unlock, err := lockTokens(ctx)
	if err != nil {
		return Workspace{}, err
	}
	defer unlock()
	// Listed first: a workspace listed without tokens only asks for a new
	// sign-in, but tokens that no list names could never be signed out of.
	if err := updateIndexLocked(func(ix *workspaceIndex) { ix.add(w) }); err != nil {
		return Workspace{}, err
	}
	return w, writeStore(account(w.ID), tr.toTokens("", client))
}

// overSSH says a browser opened here would not be in front of the user.
func overSSH() bool {
	return os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != ""
}

// Logout signs out of the workspace named by which (its URL key or name), or
// of every one when which is empty. It returns the names signed out. A
// workspace whose sign-out fails stays signed in; the others still go. local
// forgets the tokens without revoking them at Linear.
//
// It holds the token lock throughout, so a sign-in or refresh cannot write
// tokens back between the revoke and the forget.
func Logout(ctx context.Context, which string, local bool) ([]string, error) {
	unlock, err := lockTokens(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	ix, err := readIndexLocked()
	if err != nil {
		return nil, err
	}
	var targets []Workspace
	if which != "" {
		w, err := ix.find(which)
		if err != nil {
			return nil, err
		}
		targets = append(targets, *w)
	} else {
		targets = append(targets, ix.Workspaces...)
	}
	var done []string
	var errs []error
	for _, w := range targets {
		switch err := logoutLocked(ctx, w, local); {
		case errors.Is(err, ErrNotSignedIn):
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", w.Name, err))
		default:
			done = append(done, w.Name)
		}
	}
	if len(done) == 0 && len(errs) == 0 {
		return nil, ErrNotSignedIn
	}
	return done, errors.Join(errs...)
}

// logoutLocked revokes the grant at Linear, then forgets the tokens and the
// workspace. If Linear cannot be reached or refuses, nothing is forgotten, so
// the user can retry instead of keeping a live grant with no way to end it.
func logoutLocked(ctx context.Context, w Workspace, local bool) error {
	acct := account(w.ID)
	forget := func() error {
		DropCache(w.ID)
		return updateIndexLocked(func(ix *workspaceIndex) { ix.remove(w.ID) })
	}
	t, err := readStore(acct)
	switch {
	case errors.Is(err, ErrSignedOut):
		// Listed but with no tokens: nothing to revoke, so just unlist it.
		if err := forget(); err != nil {
			return err
		}
		return ErrNotSignedIn
	case err != nil && !local:
		return err
	}
	if err == nil && !local {
		save := func(nt *tokens) error { return writeStore(acct, nt) }
		if err := revokeLocked(ctx, t, save); err != nil {
			return fmt.Errorf("couldn't revoke access at Linear (%w). You're still signed in; try again, or `lineup logout --local` to only forget the tokens here", err)
		}
	}
	if err := removeStore(acct); err != nil {
		return err
	}
	return forget()
}

// revokeLocked ends the grant: revoking the refresh token ends all of it,
// authorized by an access token (refreshed first if due). Only a 2xx from
// Linear counts as revoked. save stores a refreshed pair.
func revokeLocked(ctx context.Context, t *tokens, save func(*tokens) error) error {
	access := t.AccessToken
	if !fresh(t) {
		nt, err := refreshGrant(ctx, t)
		if err == nil {
			// Stored as soon as it is spent: if the revoke then fails, the
			// sign-in still works and Logout can be retried.
			err = save(nt)
		}
		if errors.Is(err, ErrSignedOut) {
			// invalid_grant does not prove the grant is over: the token may
			// belong to another OAuth app.
			return errors.New("Linear wouldn't refresh the sign-in (invalid_grant), so revoking it can't be confirmed")
		} else if err != nil {
			return err
		}
		t, access = nt, nt.AccessToken
	}
	token, hint := t.RefreshToken, "refresh_token"
	if token == "" {
		token, hint = access, "access_token"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL,
		strings.NewReader(url.Values{"token": {token}, "token_type_hint": {hint}}.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+access)
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	// 400 ("unable to revoke") and 401 do not say the grant is gone.
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("Linear answered HTTP %d", res.StatusCode)
}
