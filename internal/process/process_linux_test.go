package process

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCancelStopsDescendants(t *testing.T) {
	dir := t.TempDir()
	ready, escaped := filepath.Join(dir, "ready"), filepath.Join(dir, "escaped")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The child would write after the shell exits unless the group is killed.
	cmd := Command(ctx, "/bin/sh", "-c", `(sleep 1; echo escaped > "$2") & echo ready > "$1"; wait`, "sh", ready, escaped)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.After(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("child did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled command succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled command did not exit")
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(escaped); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("descendant survived cancellation: %v", err)
	}
}
