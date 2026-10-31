// Package runner runs builds and shells inside a project's environment.
package runner

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/miloVV13/slepok/internal/contextio"
	"github.com/miloVV13/slepok/internal/process"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/project"
	"github.com/miloVV13/slepok/internal/store"
)

// Env is a resolved environment: profile templates expanded with the store
// paths of the locked packages.
type Env struct {
	// Packages maps locked component names to their installed roots.
	Packages  map[string]string
	Path      []string
	Vars      map[string]string
	Configure []string
	Outputs   []string
	// StoreRoot is where the packages live; paths below it are not leaks.
	StoreRoot string
}

// Resolve builds the environment of a project for platform. Every locked
// package must already be in the store: resolving never downloads.
func Resolve(prof *profile.Profile, lock *project.Lock, st *store.Store, platform, board string) (*Env, error) {
	return ResolveContext(context.Background(), prof, lock, st, platform, board)
}

func ResolveContext(ctx context.Context, prof *profile.Profile, lock *project.Lock, st *store.Store, platform, board string) (*Env, error) {
	if lock == nil {
		return nil, fmt.Errorf("%s is missing; run slepok init", project.LockName)
	}
	prof = prof.ForHost(platform)
	vars := map[string]string{"board": board}
	for _, pkg := range lock.PackagesFor(platform) {
		if pkg.Source.Digest == "" {
			return nil, fmt.Errorf("%s has no digest for %s; run slepok init", project.LockName, pkg.Name)
		}
		if !st.HasTree(pkg.Source.Digest) {
			return nil, fmt.Errorf("package %s %s is not in the local store; run slepok init (needs network) or unpack a snapshot that contains it", pkg.Name, pkg.Version)
		}
		dir, err := st.Path(pkg.Source.Digest)
		if err != nil {
			return nil, err
		}
		vars[pkg.Name] = dir
	}
	for _, c := range prof.Components {
		if c.Supports(platform) && !c.BuildOptional && vars[c.Name] == "" {
			return nil, fmt.Errorf("%s has no package for %s on %s; run slepok init", project.LockName, c.Name, platform)
		}
	}
	if platform == "macos-x64" && vars["arm-gcc"] != "" && vars["zstd"] != "" {
		dir, err := gccRuntimeLaunchers(ctx, st, vars["arm-gcc"], filepath.Join(vars["zstd"], "lib"))
		if err != nil {
			return nil, err
		}
		vars["arm-gcc"] = dir
	}

	e := &Env{Vars: map[string]string{}, Packages: vars, Outputs: prof.Outputs, StoreRoot: st.Root()}
	// A SOURCE_DATE_EPOCH set by the user wins, as is the convention.
	if v := os.Getenv("SOURCE_DATE_EPOCH"); v != "" {
		e.Vars["SOURCE_DATE_EPOCH"] = v
	} else if lock.SourceDateEpoch != 0 {
		e.Vars["SOURCE_DATE_EPOCH"] = strconv.FormatInt(lock.SourceDateEpoch, 10)
	}
	expand := func(s string) (string, error) {
		out, err := profile.Expand(s, vars)
		if err != nil {
			return "", fmt.Errorf("%w (is %s complete for %s?)", err, project.LockName, platform)
		}
		return out, nil
	}
	for _, s := range prof.Path {
		p, err := expand(s)
		if err != nil {
			return nil, err
		}
		e.Path = append(e.Path, p)
	}
	for k, v := range prof.Env {
		x, err := expand(v)
		if err != nil {
			return nil, err
		}
		e.Vars[k] = x
	}
	for _, s := range prof.Configure {
		a, err := expand(s)
		if err != nil {
			return nil, err
		}
		e.Configure = append(e.Configure, a)
	}
	// Flashing tools may be absent in older locks. Expose them in shells
	// when installed without introducing required profile placeholders.
	if root := vars["openocd"]; root != "" {
		e.Path = append(e.Path, filepath.Join(root, "bin"))
		e.Vars["OPENOCD_SCRIPTS"] = filepath.Join(root, "openocd", "scripts")
	}
	return e, nil
}

// LookPath finds a program in the environment only, never on the host.
func (e *Env) LookPath(name string) (string, error) {
	candidate := name
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(candidate), ".exe") {
		candidate += ".exe"
	}
	for _, dir := range e.Path {
		p := filepath.Join(dir, candidate)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() && (runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0) {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found in the environment", name)
}

// buildEnviron is the complete environment of a build: nothing is
// inherited from the host except temporary-directory locations and Windows
// OS command locations (System32, ComSpec and SYSTEMROOT).
func (e *Env) buildEnviron() []string {
	toolPath := e.Path
	if runtime.GOOS == "windows" {
		toolPath = windowsBuildPath(toolPath, os.Getenv("SystemRoot"))
	}
	env := []string{
		"PATH=" + strings.Join(toolPath, string(os.PathListSeparator)),
		"HOME=/nonexistent",
		"LANG=C.UTF-8",
	}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		env = append(env, "TMPDIR="+tmp)
	}
	if runtime.GOOS == "windows" {
		// Temporary files must remain usable with the rest of the host
		// environment cleared. os/exec supplies SYSTEMROOT itself.
		env = append(env, "TEMP="+os.TempDir(), "TMP="+os.TempDir())
		if root := os.Getenv("SystemRoot"); root != "" {
			env = append(env, "ComSpec="+filepath.Join(root, "System32", "cmd.exe"))
		}
	}
	for _, k := range slices.Sorted(mapKeys(e.Vars)) {
		env = append(env, k+"="+e.Vars[k])
	}
	return env
}

// Ninja/CMake use nested cmd.exe commands on Windows. Keep the pinned
// tools first and admit only the OS command directory, never the host PATH.
func windowsBuildPath(tools []string, systemRoot string) []string {
	if systemRoot == "" {
		return slices.Clone(tools)
	}
	return append(slices.Clone(tools), filepath.Join(systemRoot, "System32"))
}

func mapKeys(m map[string]string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

type Options struct {
	Context context.Context
	// RAM selects Pico SDK no_flash and validates the resulting ELF images.
	RAM bool
	// Isolate runs the build without network access where the host allows
	// it.
	Isolate bool
	Stdout  io.Writer
	Stderr  io.Writer
	// Warn receives warnings such as missing isolation.
	Warn io.Writer
}

type Output struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Result struct {
	// Isolated reports whether the build really ran without network.
	Isolated bool
	// Leaks lists cache entries and Ninja rules that point into the host system.
	Leaks   []string
	Outputs []Output
}

// Build configures src into build and builds it.
func Build(e *Env, src, build string, o Options) (*Result, error) {
	cmake, err := e.LookPath("cmake")
	if err != nil {
		return nil, err
	}
	r := &runner{env: e.buildEnviron(), dir: src, o: o, isolate: o.Isolate}
	configure := append([]string{"-S", src, "-B", build}, e.Configure...)
	if o.RAM {
		configure = append(configure, "-DPICO_DEFAULT_BINARY_TYPE=no_flash")
	}
	if err := r.run(cmake, configure...); err != nil {
		return nil, fmt.Errorf("configure failed: %w", err)
	}
	res := &Result{}
	allowed := []string{e.StoreRoot, src, build}
	var systemFiles []string
	if runtime.GOOS == "windows" {
		if root := os.Getenv("SystemRoot"); root != "" {
			systemFiles = []string{filepath.Join(root, "System32", "cmd.exe")}
		}
	}
	res.Leaks, err = Leaks(filepath.Join(build, "CMakeCache.txt"), allowed, systemFiles...)
	if err != nil {
		return nil, err
	}
	ninjaLeaks, err := NinjaLeaks(build, allowed, systemFiles...)
	if err != nil {
		return nil, err
	}
	res.Leaks = append(res.Leaks, ninjaLeaks...)
	if err := r.run(cmake, "--build", build); err != nil {
		return nil, fmt.Errorf("build failed: %w", err)
	}
	res.Isolated = r.isolate
	res.Outputs, err = outputs(o.Context, build, e.Outputs)
	if err == nil && o.RAM {
		images := 0
		for _, output := range res.Outputs {
			if strings.HasSuffix(output.Path, ".elf") {
				images++
				if _, err := InspectRP2040RAM(filepath.Join(build, output.Path)); err != nil {
					return nil, fmt.Errorf("%s: %w (the project may override PICO_DEFAULT_BINARY_TYPE)", output.Path, err)
				}
			}
		}
		if images == 0 {
			return nil, fmt.Errorf("RAM build produced no ELF images")
		}
	}
	return res, err
}

var errNoIsolation = errors.New("network isolation is only implemented on Linux")

// IsolationAvailable reports whether builds on this host can run without
// network access. It starts a program that does not exist with isolation:
// creating the namespaces happens before exec, so "not found" means they
// work, and no code runs either way.
func IsolationAvailable() bool {
	attr := isolation()
	if attr == nil {
		return false
	}
	cmd := exec.Command("/nonexistent/slepok-isolation-probe")
	cmd.SysProcAttr = attr
	err := cmd.Start()
	if err == nil {
		cmd.Process.Kill()
		cmd.Wait()
		return true
	}
	return errors.Is(err, fs.ErrNotExist)
}

type runner struct {
	env     []string
	dir     string
	o       Options
	isolate bool
}

func (r *runner) run(name string, args ...string) error {
	if r.isolate {
		attr := isolation()
		if attr == nil {
			r.noIsolation(errNoIsolation)
			return r.command(name, args...).Run()
		}
		cmd := r.command(name, args...)
		process.SetIsolation(cmd, attr)
		err := cmd.Start()
		if err == nil {
			return cmd.Wait()
		}
		if !isolationUnavailable(err) {
			return err
		}
		r.noIsolation(err)
	}
	return r.command(name, args...).Run()
}

func (r *runner) noIsolation(reason error) {
	r.isolate = false
	if r.o.Warn != nil {
		fmt.Fprintf(r.o.Warn, "warning: cannot isolate the build from the network on this host (%v); building with network access\n", reason)
	}
}

func (r *runner) command(name string, args ...string) *exec.Cmd {
	cmd := process.Command(r.o.Context, name, args...)
	cmd.Env, cmd.Dir = r.env, r.dir
	cmd.Stdout, cmd.Stderr = r.o.Stdout, r.o.Stderr
	return cmd
}

// systemPrefixes are where host tools live; a configured build must not
// refer to them.
var systemPrefixes = []string{"/usr/", "/bin/", "/sbin/", "/lib/", "/lib64/", "/opt/", "/etc/", "/snap/", "/nix/"}

// ignoredCacheKeys hold host paths by design and do not affect the build
// output: the install prefix is never used, and CMake looks up uname in
// hard-coded system directories only to detect the host OS.
var ignoredCacheKeys = []string{"CMAKE_INSTALL_PREFIX", "CMAKE_UNAME"}

// Leaks reports CMake cache entries that point into the host system rather
// than into the allowed directories (spec 17, stage 1 finding 1).
func Leaks(cache string, allowed []string, systemFiles ...string) ([]string, error) {
	f, err := os.Open(cache)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var leaks []string
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 1<<20)
	for s.Scan() {
		line := s.Text()
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		keyType, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, _, _ := strings.Cut(keyType, ":")
		if slices.Contains(ignoredCacheKeys, key) {
			continue
		}
		for _, v := range strings.Split(value, ";") {
			if hostPath(v, allowed, systemFiles...) {
				leaks = append(leaks, key+"="+v)
				break
			}
		}
	}
	return leaks, s.Err()
}

func hostPath(v string, allowed []string, systemFiles ...string) bool {
	if windowsAbsolute(v) {
		norm := func(p string) string { return strings.ToLower(path.Clean(strings.ReplaceAll(p, `\`, "/"))) }
		value := norm(v)
		// OS command interpreters are runtime dependencies, like /bin/sh on
		// Linux. Exempt the exact selected file, never its containing directory.
		for _, file := range systemFiles {
			if windowsAbsolute(file) && value == norm(file) {
				return false
			}
		}
		for _, a := range allowed {
			if windowsAbsolute(a) {
				base := norm(a)
				if value == base || strings.HasPrefix(value, strings.TrimSuffix(base, "/")+"/") {
					return false
				}
			}
		}
		return true
	}
	if !strings.HasPrefix(v, "/") {
		return false
	}
	for _, a := range allowed {
		if a != "" && (v == a || strings.HasPrefix(v, strings.TrimSuffix(a, "/")+"/")) {
			return false
		}
	}
	for _, p := range systemPrefixes {
		if strings.HasPrefix(v+"/", p) {
			return true
		}
	}
	return false
}

func windowsAbsolute(p string) bool {
	if strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//") {
		return true
	}
	return len(p) >= 3 && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) && p[1] == ':' && (p[2] == '/' || p[2] == '\\')
}

// Keep quoted paths and Ninja's escaped spaces together. This checks literal
// paths in generated rules; it does not interpret shell scripts or variables.
var ninjaWords = regexp.MustCompile(`"[^"]*"|'[^']*'|(?:\$ |[^\s"';|&<>])+`)
var ninjaUnescape = strings.NewReplacer("$ ", " ", "$:", ":", "$$", "$")

// NinjaLeaks checks the generated graph and included rule files, where custom
// commands can refer to host tools without adding anything to CMakeCache.txt.
func NinjaLeaks(build string, allowed []string, systemFiles ...string) ([]string, error) {
	var leaks []string
	err := filepath.WalkDir(build, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() || filepath.Ext(p) != ".ninja" {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		rel, err := filepath.Rel(build, p)
		if err != nil {
			return err
		}
		s := bufio.NewScanner(f)
		s.Buffer(make([]byte, 64<<10), 1<<20)
		seen := map[string]bool{}
		for line := 1; s.Scan(); line++ {
			if strings.HasPrefix(strings.TrimSpace(s.Text()), "#") {
				continue
			}
			for _, word := range ninjaWords.FindAllString(s.Text(), -1) {
				word = strings.Trim(word, `"'`)
				word = ninjaUnescape.Replace(word)
				for _, value := range strings.Split(word, "=") {
					for _, prefix := range []string{"-isystem", "-I", "-L"} {
						value = strings.TrimPrefix(value, prefix)
					}
					if hostPath(value, allowed, systemFiles...) && !seen[value] {
						seen[value] = true
						leaks = append(leaks, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), line, value))
					}
				}
			}
		}
		return s.Err()
	})
	return leaks, err
}

func outputs(ctx context.Context, build string, patterns []string) ([]Output, error) {
	var out []Output
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(build, pattern))
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			sum, err := fileSHA256(ctx, m)
			if err != nil {
				return nil, err
			}
			out = append(out, Output{Path: filepath.Base(m), SHA256: sum})
		}
	}
	return out, nil
}

func fileSHA256(ctx context.Context, p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, contextio.Reader{Context: ctx, Reader: f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Shell starts an interactive shell in dir. The environment's tools come
// first in PATH, followed by the host PATH so ordinary utilities still
// work. A cmake wrapper adds the profile's configure arguments, so running
// cmake by hand configures the project the same way slepok build does.
func Shell(e *Env, dir string, st *store.Store, stdin io.Reader, stdout, stderr io.Writer) error {
	cmake, err := e.LookPath("cmake")
	if err != nil {
		return err
	}
	bin, err := st.TempDir("shell-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(bin)
	sh, args, extra, err := prepareShell(bin, cmake, e.Configure)
	if err != nil {
		return err
	}
	path := slices.Concat([]string{bin}, e.Path, filepath.SplitList(os.Getenv("PATH")))
	env := []string{"PATH=" + strings.Join(path, string(os.PathListSeparator)), "SLEPOK_SHELL=1"}
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if !strings.EqualFold(key, "PATH") && !strings.EqualFold(key, "SLEPOK_SHELL") {
			env = append(env, kv)
		}
	}
	env = append(env, extra...)
	for _, k := range slices.Sorted(mapKeys(e.Vars)) {
		env = append(env, k+"="+e.Vars[k])
	}
	cmd := exec.Command(sh, args...)
	cmd.Env, cmd.Dir = env, dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	err = cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return nil // the shell's own exit status is the user's business
	}
	return err
}

// cmakeWrapper returns a POSIX sh script that runs cmake, adding args
// unless cmake is invoked in a mode that does not configure a project.
func cmakeWrapper(cmake string, args []string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# Generated by slepok shell: configure with the profile's arguments.\n")
	b.WriteString("for a in \"$@\"; do\n  case \"$a\" in\n    --build|--install|--open|-E|-P|--version|--help|-h|--system-information|--workflow) exec " + shellQuote(cmake) + " \"$@\" ;;\n  esac\ndone\n")
	b.WriteString("exec " + shellQuote(cmake) + " \"$@\"")
	for _, a := range args {
		b.WriteString(" " + shellQuote(a))
	}
	b.WriteString("\n")
	return b.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
