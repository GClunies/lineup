package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestSecondSchedulerIsRefusedWhileFirstHoldsTheLock(t *testing.T) {
	dir := t.TempDir()
	release, err := lockServe(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = lockServe(dir)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("another Scheduler is running (pid %d)", os.Getpid())) {
		t.Fatalf("second lock: %v", err)
	}
	release()
	again, err := lockServe(dir)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	again()
}
