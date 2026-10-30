// Command fakecmake stands in for cmake in the end-to-end test of slepok.
//
//	fakecmake -S src -B build args...   writes build/CMakeCache.txt
//	fakecmake --build build             writes build/app.uf2
//
// On configure it reads src/probe.txt (an address and a log file), tries to
// connect to the address and appends "reachable" or "unreachable" to the
// log, so the test can tell whether the build ran without network. A file
// named "leak" in the project makes the cache refer to a host program.
package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	args := os.Args[1:]
	var err error
	if len(args) == 2 && args[0] == "--build" {
		err = build(args[1])
	} else {
		err = configure(args)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakecmake:", err)
		os.Exit(1)
	}
}

func configure(args []string) error {
	var src, out string
	var rest []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-S" && i+1 < len(args):
			src, i = args[i+1], i+1
		case args[i] == "-B" && i+1 < len(args):
			out, i = args[i+1], i+1
		default:
			rest = append(rest, args[i])
		}
	}
	if src == "" || out == "" {
		return fmt.Errorf("need -S and -B")
	}
	if err := probe(src); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	cache := []string{
		"# fake CMake cache",
		"CMAKE_HOME_DIRECTORY:INTERNAL=" + src,
		"SDK_PATH:PATH=" + os.Getenv("SDK_PATH"),
		"FAKE_ARGS:STRING=" + strings.Join(rest, " "),
	}
	if _, err := os.Stat(filepath.Join(src, "leak")); err == nil {
		cache = append(cache, "LEAKY_TOOL:FILEPATH=/usr/bin/python3")
	}
	if _, err := os.Stat(filepath.Join(src, "ninja-leak")); err == nil {
		dir := filepath.Join(out, "CMakeFiles")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "rules.ninja"), []byte("command = /usr/bin/python3 generate.py\n"), 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(out, "CMakeCache.txt"), []byte(strings.Join(cache, "\n")+"\n"), 0o644)
}

func probe(src string) error {
	data, err := os.ReadFile(filepath.Join(src, "probe.txt"))
	if err != nil {
		return nil // no probe requested
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		return fmt.Errorf("probe.txt: want address and log path")
	}
	result := "unreachable"
	if conn, err := net.DialTimeout("tcp", lines[0], 2*time.Second); err == nil {
		conn.Close()
		result = "reachable"
	}
	f, err := os.OpenFile(lines[1], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, result)
	if err == nil && result == "unreachable" {
		if _, required := os.Stat(filepath.Join(src, "requires-network")); required == nil {
			return fmt.Errorf("required network dependency is unavailable")
		}
	}
	return err
}

// build writes a firmware image derived only from the sources and the SDK,
// so identical inputs give identical output on any machine.
func build(out string) error {
	data, err := os.ReadFile(filepath.Join(out, "CMakeCache.txt"))
	if err != nil {
		return err
	}
	var src, sdk string
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "CMAKE_HOME_DIRECTORY:INTERNAL="); ok {
			src = v
		}
		if v, ok := strings.CutPrefix(line, "SDK_PATH:PATH="); ok {
			sdk = v
		}
	}
	sources, err := filepath.Glob(filepath.Join(src, "*.c"))
	if err != nil {
		return err
	}
	sort.Strings(sources)
	var image []byte
	for _, s := range append(sources, filepath.Join(sdk, "version.txt"), filepath.Join(sdk, "lib", "sub", "sub.txt")) {
		b, err := os.ReadFile(s)
		if err != nil {
			return err
		}
		image = append(image, b...)
	}
	return os.WriteFile(filepath.Join(out, "app.uf2"), image, 0o644)
}
