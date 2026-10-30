package store

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type pausedInstall struct {
	r     io.Reader
	left  int
	ready string
}

func (r *pausedInstall) Read(p []byte) (int, error) {
	if r.left == 0 {
		if err := os.WriteFile(r.ready, nil, 0o644); err != nil {
			return 0, err
		}
		time.Sleep(time.Minute)
	}
	if len(p) > r.left {
		p = p[:r.left]
	}
	n, err := r.r.Read(p)
	r.left -= n
	return n, err
}

func TestKilledInstallHelper(t *testing.T) {
	root := os.Getenv("SLEPOK_INSTALL_CRASH")
	if root == "" {
		t.Skip("subprocess helper")
	}
	s, err := Open(filepath.Join(root, "store"))
	if err != nil {
		t.Fatal(err)
	}
	guard, err := s.Acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	f, err := os.Open(filepath.Join(root, "package.tar"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = s.Install(os.Getenv("SLEPOK_INSTALL_DIGEST"), &pausedInstall{r: f, left: 1100, ready: filepath.Join(root, "ready")})
	t.Fatalf("install returned before SIGKILL: %v", err)
}

func TestKilledInstallRecovery(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { RemoveTree(root) })
	data, digest := newPackage(t)
	if err := os.WriteFile(filepath.Join(root, "package.tar"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestKilledInstallHelper$")
	cmd.Env = append(os.Environ(), "SLEPOK_INSTALL_CRASH="+root, "SLEPOK_INSTALL_DIGEST="+digest)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("installer did not reach partial extraction")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	s, err := Open(filepath.Join(root, "store"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Has(digest) {
		t.Fatal("partial install published a package")
	}
	left, err := os.ReadDir(filepath.Join(s.Root(), "packages"))
	if err != nil || len(left) != 1 {
		t.Fatalf("want one abandoned install: %v, %v", left, err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(s.Root(), "packages", left[0].Name()), old, old); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	guard, err := s.Acquire(ctx, true)
	if err != nil {
		t.Fatalf("SIGKILL did not release operation lock: %v", err)
	}
	items, err := s.PlanGC(nil, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Kind != "tmp" {
		t.Fatalf("unexpected GC plan: %v", items)
	}
	if err := s.Delete(items); err != nil {
		t.Fatal(err)
	}
	guard.Close()
	if err := s.InstallContext(ctx, digest, bytes.NewReader(data)); err != nil {
		t.Fatalf("install could not recover: %v", err)
	}
	if !s.Has(digest) {
		t.Fatal("retry did not publish package")
	}
}
