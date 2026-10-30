package runner

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWindowsShellTarget(t *testing.T) {
	file := os.Getenv("SLEPOK_SHELL_TEST_OUTPUT")
	if file == "" {
		return
	}
	data, _ := json.Marshal(os.Args[1:])
	if err := os.WriteFile(file, data, 0600); err != nil {
		os.Exit(9)
	}
	os.Exit(7)
}

func TestWindowsShellProxy(t *testing.T) {
	bin := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	flags := []string{"-DVALUE=a space & 'quote' %var%!"}
	_, _, extra, err := prepareShell(bin, exe, flags)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"configure", "--build"} {
		file := filepath.Join(bin, mode+".json")
		args := []string{"-test.run=^TestWindowsShellTarget$"}
		if mode == "--build" {
			args = append(args, mode)
		}
		cmd := exec.Command(filepath.Join(bin, "cmake.exe"), args...)
		cmd.Env = append(os.Environ(), "SLEPOK_SHELL=1", "SLEPOK_SHELL_TEST_OUTPUT="+file)
		cmd.Env = append(cmd.Env, extra...)
		// The fake target is a Go test binary: terminate flag parsing before
		// passing CMake arguments to it.
		args = append(args[:1], append([]string{"--"}, args[1:]...)...)
		cmd.Args = append(cmd.Args[:1], args...)
		err := cmd.Run()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 7 {
			t.Fatalf("exit = %v", err)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		want := append([]string{}, args...)
		if mode == "configure" {
			want = append(want, flags...)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("args = %#v, want %#v", got, want)
		}
	}
}
