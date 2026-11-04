package linear

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Tokens live in the login keychain, reached through `security`. The service
// is "lineup" and not herdr-linear's: Linear rotates refresh tokens, so two
// programs on one item with separate locks would invalidate each other.
const keychainService = "lineup"

// keyringWait bounds each keychain call, an unlock prompt included, so
// nothing waits forever while it holds the token lock.
var keyringWait = 90 * time.Second

const helperWait = 5 * time.Second

func runBounded(wait time.Duration, stdin string, combined bool, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out []byte
	var err error
	if combined {
		out, err = cmd.CombinedOutput()
	} else {
		out, err = cmd.Output()
	}
	if ctx.Err() != nil {
		return out, fmt.Errorf("%s didn't finish within %v", name, wait)
	}
	return out, err
}

func loadTokens(acct string) (*tokens, error) {
	out, err := runBounded(keyringWait, "", false, "security", "find-generic-password", "-s", keychainService, "-a", acct, "-w")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 44 { // errSecItemNotFound
			return nil, ErrSignedOut
		}
		return nil, fmt.Errorf("keychain read: %w", err)
	}
	var t tokens
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &t); err != nil || t.AccessToken == "" {
		return nil, ErrSignedOut
	}
	return &t, nil
}

// saveTokens writes through `security -i` so the secret travels over stdin,
// never argv, which every local user can read with ps.
func saveTokens(acct string, t *tokens) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	out, err := runBounded(keyringWait, fmt.Sprintf("add-generic-password -U -s %s -a %s -l \"lineup Linear\" -X %s\n",
		keychainService, acct, hex.EncodeToString(data)), true, "security", "-i")
	// `security -i` exits 0 when a command fails and reports it on output.
	if err != nil || strings.Contains(string(out), "returned") {
		msg := strings.TrimSpace(string(out))
		if err != nil && msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("keychain write failed: %s", msg)
	}
	return nil
}

func deleteTokens(acct string) error {
	_, err := runBounded(keyringWait, "", false, "security", "delete-generic-password", "-s", keychainService, "-a", acct)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 44 {
		return nil
	}
	return err
}

func openBrowser(u string) error {
	_, err := runBounded(helperWait, "", false, "open", u)
	return err
}
