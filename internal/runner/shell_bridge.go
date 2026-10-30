package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type shellConfig struct {
	Version   int      `json:"version"`
	CMake     string   `json:"cmake"`
	Configure []string `json:"configure"`
}

func writeShellConfig(file, cmake string, args []string) error {
	data, err := json.Marshal(shellConfig{Version: 1, CMake: cmake, Configure: args})
	if err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o600)
}

func readShellConfig(file string) (*shellConfig, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var cfg shellConfig
	if err := json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&cfg); err != nil {
		return nil, err
	}
	if cfg.Version != 1 || !filepath.IsAbs(cfg.CMake) {
		return nil, fmt.Errorf("invalid Slepok shell configuration")
	}
	return &cfg, nil
}

// Keep arguments as an argv array: never interpolate paths/profile flags
// into a batch file or PowerShell expression.
func shellCMakeArgs(args, configure []string) []string {
	for _, a := range args {
		switch a {
		case "--build", "--install", "--open", "-E", "-P", "--version", "-h", "-N", "--system-information", "--workflow", "--find-package":
			return slices.Clone(args)
		}
		if strings.HasPrefix(a, "--help") || strings.HasPrefix(a, "--list-presets") || strings.HasPrefix(a, "-P") || strings.HasPrefix(a, "--build=") || strings.HasPrefix(a, "--install=") {
			return slices.Clone(args)
		}
	}
	return append(slices.Clone(args), configure...)
}
