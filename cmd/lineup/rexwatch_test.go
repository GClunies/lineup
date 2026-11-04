package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func watch(ctx context.Context, ids ...any) error {
	i := 0
	read := func(context.Context) (string, error) {
		v := ids[min(i, len(ids)-1)]
		i++
		if err, ok := v.(error); ok {
			return "", err
		}
		return v.(string), nil
	}
	return watchRexServer(ctx, time.Millisecond, "a", read, func(string, ...any) {})
}

func TestWatchReturnsWhenRexServerRestarts(t *testing.T) {
	if err := watch(context.Background(), "a", "a", "b"); !errors.Is(err, errRexRestarted) {
		t.Fatalf("got %v", err)
	}
}

func TestWatchKeepsRunningWhenInstanceIsSame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := watch(ctx, "a"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestWatchIgnoresAFailedRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := watch(ctx, errors.New("down"), "a"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}
