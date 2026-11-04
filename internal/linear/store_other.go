//go:build !darwin

package linear

import "errors"

var errNoKeychain = errors.New("lineup keeps the Linear sign-in in the macOS keychain only")

func loadTokens(string) (*tokens, error) { return nil, errNoKeychain }
func saveTokens(string, *tokens) error   { return errNoKeychain }
func deleteTokens(string) error          { return errNoKeychain }
func openBrowser(string) error           { return errNoKeychain }
