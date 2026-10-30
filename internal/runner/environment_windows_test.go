package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The outer cmd can be found by an absolute path even with an empty PATH;
// the nested bare cmd.exe reproduces the Ninja command from windows-log.txt.
func TestBuildEnvironmentFindsNestedWindowsCommand(t *testing.T) {
	root := os.Getenv("SystemRoot")
	if root == "" {
		t.Fatal("SystemRoot missing")
	}
	t.Setenv("PATH", filepath.Join(t.TempDir(), "host-tools-must-not-leak"))
	e := &Env{Path: []string{filepath.Join(t.TempDir(), "pinned-tools")}}
	cmd := exec.Command(filepath.Join(root, "System32", "cmd.exe"), "/D", "/C", "cmd.exe /D /C echo slepok-nested-command-ok")
	cmd.Env = e.buildEnviron()
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "slepok-nested-command-ok") {
		t.Fatalf("nested command: %v\n%s", err, output)
	}
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "PATH=") && strings.Contains(kv, "host-tools-must-not-leak") {
			t.Fatal("inherited host PATH")
		}
	}
}
