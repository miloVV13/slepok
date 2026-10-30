package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miloVV13/slepok/internal/store"
)

func TestConcurrentCLIHelper(t *testing.T) {
	action := os.Getenv("SLEPOK_CONCURRENT_ACTION")
	if action == "" {
		t.Skip("subprocess helper")
	}
	archive, err := os.ReadFile(os.Getenv("SLEPOK_TEST_ARCHIVE_PATH"))
	must(t, err)
	fakeProfile(t, os.Getenv("SLEPOK_TEST_SERVER"), archive, nil, os.Getenv("SLEPOK_TEST_SDK"), os.Getenv("SLEPOK_TEST_COMMIT"))
	if action == "init" {
		os.Args = []string{"slepok", "init", "--profile", "fake", os.Getenv("SLEPOK_TEST_PROJECT")}
	} else {
		os.Args = []string{"slepok", "gc", "-y"}
	}
	main()
	if action == "gc" {
		must(t, os.WriteFile(os.Getenv("SLEPOK_TEST_GC_FINISHED"), nil, 0o644))
	}
}

func TestConcurrentInitAndGC(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	t.Cleanup(func() { store.RemoveTree(dir) })
	tool := filepath.Join(dir, "cmake")
	must(t, os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	archive := toolchainArchive(t, tool)
	sdk, commit := sdkRepo(t)
	started, release := make(chan struct{}), make(chan struct{})
	var released atomic.Bool
	defer func() {
		if released.CompareAndSwap(false, true) {
			close(release)
		}
	}()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		w.Write(archive[:len(archive)/2])
		w.(http.Flusher).Flush()
		if n == 1 {
			close(started)
		}
		select {
		case <-release:
			w.Write(archive[len(archive)/2:])
		case <-r.Context().Done():
		}
	}))
	// Release handlers before closing the server on a failed assertion.
	defer func() {
		if released.CompareAndSwap(false, true) {
			close(release)
		}
		srv.Close()
	}()
	archivePath := filepath.Join(dir, "tc.tar.gz")
	must(t, os.WriteFile(archivePath, archive, 0o644))
	start := func(action, name string) (<-chan error, *bytes.Buffer) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestConcurrentCLIHelper$")
		cmd.Env = append(os.Environ(), "SLEPOK_HOME="+filepath.Join(dir, "store"), "SLEPOK_CONCURRENT_ACTION="+action,
			"SLEPOK_TEST_SERVER="+srv.URL, "SLEPOK_TEST_ARCHIVE_PATH="+archivePath,
			"SLEPOK_TEST_SDK="+sdk, "SLEPOK_TEST_COMMIT="+commit, "SLEPOK_TEST_PROJECT="+filepath.Join(dir, name), "SLEPOK_TEST_GC_FINISHED="+filepath.Join(dir, "gc-finished"))
		log := &bytes.Buffer{}
		cmd.Stdout, cmd.Stderr = log, log
		must(t, cmd.Start())
		t.Cleanup(func() { cmd.Process.Kill() })
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		return done, log
	}
	a, logA := start("init", "a")
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("first init did not start downloading")
	}
	b, logB := start("init", "b")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "b", "slepok.toml")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second init did not reach setup")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Even hour-old scratch data belongs to a live init until it releases
	// the operation lock. Age alone must not decide whether it is in use.
	sum := sha256.Sum256(archive)
	archiveSum := hex.EncodeToString(sum[:])
	old := time.Now().Add(-2 * time.Hour)
	must(t, os.Chtimes(filepath.Join(dir, "store", "downloads", archiveSum+".part"), old, old))
	scratch, err := os.ReadDir(filepath.Join(dir, "store", "tmp"))
	must(t, err)
	for _, entry := range scratch {
		must(t, os.Chtimes(filepath.Join(dir, "store", "tmp", entry.Name()), old, old))
	}
	gc, logGC := start("gc", "")
	select {
	case err := <-gc:
		t.Fatalf("gc ran during an active init: %v\n%s", err, logGC)
	case <-time.After(150 * time.Millisecond):
	}
	if _, err := os.Stat(filepath.Join(dir, "gc-finished")); err == nil {
		t.Fatal("gc completed while init was downloading")
	}
	if requests.Load() != 1 {
		t.Fatalf("concurrent writers to the same download: %d requests", requests.Load())
	}
	if released.CompareAndSwap(false, true) {
		close(release)
	}
	for _, child := range []struct {
		done <-chan error
		log  *bytes.Buffer
	}{{a, logA}, {b, logB}, {gc, logGC}} {
		select {
		case err := <-child.done:
			if err != nil {
				t.Fatalf("child failed: %v\n%s", err, child.log)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("concurrent command did not finish")
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("shared archive downloaded %d times, want 1", requests.Load())
	}
	st, err := store.Open(filepath.Join(dir, "store"))
	must(t, err)
	projects, err := st.Projects()
	must(t, err)
	if len(projects) != 2 {
		t.Fatalf("registry lost concurrent projects: %v", projects)
	}
	pkgs, err := st.Packages()
	must(t, err)
	if len(pkgs) != 2 {
		t.Fatalf("expected two intact shared packages, got %v", pkgs)
	}
}
