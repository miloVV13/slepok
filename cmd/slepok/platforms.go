package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/miloVV13/slepok/internal/manifest"
)

func selectedPlatforms(value string) ([]string, error) {
	if value == "" {
		return []string{manifest.HostPlatform()}, nil
	}
	var out []string
	for _, part := range strings.Split(value, ",") {
		p := strings.TrimSpace(part)
		if p != "linux-x64" && p != "windows-x64" {
			return nil, fmt.Errorf("unsupported platform %q (supported: linux-x64, windows-x64)", p)
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

func withBuildHost(targets []string) []string {
	out := slices.Clone(targets)
	if host := manifest.HostPlatform(); !slices.Contains(out, host) {
		out = append(out, host)
	}
	return out
}

func checkPreparationHost(host string, targets []string) error {
	if host == "windows-x64" && slices.Contains(targets, "linux-x64") {
		return fmt.Errorf("preparing Linux packages on Windows is not supported yet; prepare a Linux/Windows snapshot on Linux")
	}
	return nil
}
