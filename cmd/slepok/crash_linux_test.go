package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/snapshot"
	"github.com/miloVV13/slepok/internal/store"
)

func TestKilledUnpackHelper(t *testing.T) {
	dir := os.Getenv("SLEPOK_UNPACK_CRASH")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	os.Args = []string{"slepok", "unpack", filepath.Join(dir, "stream"), filepath.Join(dir, "restored")}
	main()
}

func TestKilledUnpackRecovery(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() { store.RemoveTree(dir) })
	source := filepath.Join(dir, "source")
	must(t, os.Mkdir(source, 0o755))
	must(t, os.WriteFile(filepath.Join(source, "main.c"), []byte("source"), 0o644))
	archive := filepath.Join(dir, "valid.slepok")
	_, err := snapshot.Pack(archive, snapshot.Options{ProjectDir: source, ProjectFiles: []string{"main.c"}, Platforms: []string{manifest.HostPlatform()}})
	must(t, err)
	// Unpack creates staging before opening the input stream. A FIFO keeps
	// that operation in flight without timing a large extraction.
	must(t, syscall.Mkfifo(filepath.Join(dir, "stream"), 0o600))
	cmd := exec.Command(os.Args[0], "-test.run=^TestKilledUnpackHelper$")
	cmd.Env = append(os.Environ(), "SLEPOK_UNPACK_CRASH="+dir, "SLEPOK_HOME="+filepath.Join(dir, "store"))
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	must(t, cmd.Start())
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	var staging []string
	deadline := time.Now().Add(5 * time.Second)
	for {
		staging, err = filepath.Glob(filepath.Join(dir, ".restored.slepok-tmp-*"))
		must(t, err)
		if len(staging) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unpack did not create staging")
		}
		time.Sleep(10 * time.Millisecond)
	}
	must(t, cmd.Process.Kill())
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed unpack reported success")
	}
	if _, err := os.Stat(filepath.Join(dir, "restored")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial project was published: %v", err)
	}
	t.Setenv("SLEPOK_HOME", filepath.Join(dir, "store"))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	must(t, executeContext(ctx, []string{"unpack", archive, filepath.Join(dir, "restored")}))
	data, err := os.ReadFile(filepath.Join(dir, "restored", "main.c"))
	must(t, err)
	if string(data) != "source" {
		t.Fatal("retry restored the wrong source")
	}
	// GC only touches the store. An abandoned sibling of the destination
	// is deliberately retained for manual inspection/removal.
	must(t, executeContext(ctx, []string{"gc", "-y"}))
	if _, err := os.Stat(staging[0]); err != nil {
		t.Fatalf("gc unexpectedly touched project-side scratch: %v", err)
	}
}
