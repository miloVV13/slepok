package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/project"
	"github.com/miloVV13/slepok/internal/store"
)

// Exercise main's signal handler and actual pack cleanup in a subprocess.
func TestSignalDuringPack(t *testing.T) {
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			dir := t.TempDir()
			t.Cleanup(func() { store.RemoveTree(dir) })
			cmd := exec.Command(os.Args[0], "-test.run=^TestSignalPackHelper$")
			cmd.Env = append(os.Environ(), "SLEPOK_SIGNAL_HELPER="+dir)
			var log bytes.Buffer
			cmd.Stdout, cmd.Stderr = &log, &log
			must(t, cmd.Start())
			defer cmd.Process.Kill()
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			deadline := time.After(10 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("helper exited before build: %v\n%s", err, &log)
				case <-deadline:
					t.Fatal("helper did not reach configure")
				case <-time.After(10 * time.Millisecond):
				}
			}
			must(t, cmd.Process.Signal(sig))
			select {
			case err := <-done:
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 130 {
					t.Fatalf("exit = %v\n%s", err, &log)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("pack did not stop after signal")
			}
			entries, err := os.ReadDir(filepath.Join(dir, "store", "tmp"))
			must(t, err)
			if len(entries) != 0 {
				t.Fatalf("pack left scratch files: %v", entries)
			}
			if _, err := os.Stat(filepath.Join(dir, "snapshot.slepok")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("cancelled pack published output: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(dir, "project", "main.c"))
			must(t, err)
			if string(data) != "original source" {
				t.Fatal("pack modified source")
			}
		})
	}
}

func TestSignalPackHelper(t *testing.T) {
	dir := os.Getenv("SLEPOK_SIGNAL_HELPER")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	fakeProfile(t, "http://unused.invalid", nil, nil, "file:///unused", strings.Repeat("a", 40))
	st, err := store.Open(filepath.Join(dir, "store"))
	must(t, err)
	tool := filepath.Join(dir, "tool")
	must(t, os.MkdirAll(filepath.Join(tool, "bin"), 0o755))
	// Configure stays alive until the parent sends a signal.
	script := "#!/bin/sh\necho ready > " + ("'" + strings.ReplaceAll(filepath.Join(dir, "ready"), "'", "'\"'\"'") + "'") + "\nexec /bin/sleep 60\n"
	must(t, os.WriteFile(filepath.Join(tool, "bin", "cmake"), []byte(script), 0o755))
	digest, err := st.InstallDir(tool, "")
	must(t, err)
	proj := filepath.Join(dir, "project")
	must(t, os.Mkdir(proj, 0o755))
	must(t, project.WriteConfig(proj, project.Config{Profile: "fake", Board: "board-a", Platforms: []string{manifest.HostPlatform()}, BuildDir: "build"}))
	must(t, project.WriteLock(proj, project.Lock{Profile: "fake", ProfileVersion: "1", Packages: []project.LockedPackage{
		{Name: "tc", Version: "1.0", Platform: manifest.HostPlatform(), Source: profile.Source{Digest: digest}},
		{Name: "sdk", Version: "2.0", Platform: "any", Source: profile.Source{Digest: digest}},
	}}))
	must(t, os.WriteFile(filepath.Join(proj, "main.c"), []byte("original source"), 0o644))
	t.Setenv("SLEPOK_HOME", st.Root())
	t.Chdir(proj)
	os.Args = []string{"slepok", "pack", "-o", filepath.Join(dir, "snapshot.slepok")}
	main()
	t.Fatal("pack unexpectedly returned")
}
