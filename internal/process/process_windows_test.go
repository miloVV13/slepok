package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsProcessHelper(t *testing.T) {
	switch os.Getenv("SLEPOK_PROCESS_TEST") {
	case "exit":
		os.Exit(7)
	case "leaf":
		file := os.Getenv("SLEPOK_PROCESS_MARKER")
		if err := os.WriteFile(file, nil, 0600); err != nil {
			os.Exit(9)
		}
		time.Sleep(2 * time.Second)
		_ = os.WriteFile(file+".escaped", nil, 0600)
		os.Exit(0)
	case "parent":
		cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsProcessHelper$")
		cmd.Env = append(os.Environ(), "SLEPOK_PROCESS_TEST=leaf")
		if err := cmd.Start(); err != nil {
			os.Exit(9)
		}
		_ = cmd.Wait()
		os.Exit(0)
	}
}

func TestWindowsProcessExit(t *testing.T) {
	for _, mode := range []string{"", "exit"} {
		cmd := Command(context.Background(), os.Args[0], "-test.run=^TestWindowsProcessHelper$")
		cmd.Env = append(os.Environ(), "SLEPOK_PROCESS_TEST="+mode)
		err := cmd.Run()
		if mode == "" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 7 {
			t.Fatalf("exit = %v", err)
		}
	}
}

func TestWindowsProcessCancellationKillsDescendant(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	file := filepath.Join(t.TempDir(), "ready")
	cmd := Command(ctx, os.Args[0], "-test.run=^TestWindowsProcessHelper$")
	cmd.Env = append(os.Environ(), "SLEPOK_PROCESS_TEST=parent", "SLEPOK_PROCESS_MARKER="+file)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(file); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("descendant did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := cmd.Wait(); err == nil {
		t.Fatal("cancelled process succeeded")
	}
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(file + ".escaped"); !os.IsNotExist(err) {
		t.Fatalf("descendant survived cancellation: %v", err)
	}
}
