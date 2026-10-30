package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/miloVV13/slepok/internal/process"
)

const shellConfigEnv = "SLEPOK_SHELL_CMAKE_CONFIG"

// The temporary cmake.exe is a private copy of Slepok. Its entry point
// forwards argv directly to the pinned CMake, without cmd/PowerShell quoting.
func init() {
	if !strings.EqualFold(filepath.Base(os.Args[0]), "cmake.exe") || os.Getenv("SLEPOK_SHELL") != "1" {
		return
	}
	file := os.Getenv(shellConfigEnv)
	if file == "" {
		return
	}
	cfg, err := readShellConfig(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "slepok shell:", err)
		os.Exit(1)
	}
	cmd := process.Command(context.Background(), cfg.CMake, shellCMakeArgs(os.Args[1:], cfg.Configure)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "slepok shell:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func prepareShell(bin, cmake string, configure []string) (string, []string, []string, error) {
	file := filepath.Join(bin, "cmake.json")
	if err := writeShellConfig(file, cmake, configure); err != nil {
		return "", nil, nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", nil, nil, err
	}
	in, err := os.Open(exe)
	if err != nil {
		return "", nil, nil, err
	}
	defer in.Close()
	// Copy rather than hardlink: cleanup must never change the installed
	// executable's attributes, and source/store may be on different volumes.
	out, err := os.OpenFile(filepath.Join(bin, "cmake.exe"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return "", nil, nil, err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return "", nil, nil, err
	}
	if err := out.Close(); err != nil {
		return "", nil, nil, err
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		return "", nil, nil, fmt.Errorf("Windows system directory is unavailable")
	}
	sh := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(sh); err != nil {
		return "", nil, nil, fmt.Errorf("Windows PowerShell is unavailable; use slepok build: %w", err)
	}
	return sh, []string{"-NoLogo", "-NoProfile"}, []string{shellConfigEnv + "=" + file}, nil
}
