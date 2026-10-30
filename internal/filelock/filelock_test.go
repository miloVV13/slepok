//go:build linux || windows || darwin

package filelock

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLockHelper(t *testing.T) {
	path := os.Getenv("SLEPOK_LOCK_HELPER")
	if path == "" {
		t.Skip("subprocess helper")
	}
	f, err := Acquire(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := os.WriteFile(path+".ready", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// The parent kills this process: no deferred cleanup can run.
	time.Sleep(time.Minute)
}

func TestSharedExclusiveAndSIGKILL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockHelper$")
	cmd.Env = append(os.Environ(), "SLEPOK_LOCK_HELPER="+path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path + ".ready"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lock holder did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shared, err := Acquire(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	shared.Close()
	blocked, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	if f, err := Acquire(blocked, path, false); !errors.Is(err, context.DeadlineExceeded) {
		if f != nil {
			f.Close()
		}
		t.Fatalf("exclusive lock was not blocked: %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	// The persistent lock file remains, but the kernel lock is gone.
	recovered, err := Acquire(ctx, path, false)
	if err != nil {
		t.Fatalf("lock did not recover after SIGKILL: %v", err)
	}
	recovered.Close()
}
