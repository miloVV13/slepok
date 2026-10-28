package runner

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/project"
	"github.com/miloVV13/slepok/internal/store"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestResolve(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { store.RemoveTree(root) })
	st, err := store.Open(root)
	must(t, err)

	tool := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(tool, "bin"), 0o755))
	must(t, os.WriteFile(filepath.Join(tool, "bin", "cmake"), []byte("#!/bin/sh\n"), 0o755))
	digest, err := st.InstallDir(tool, "")
	must(t, err)

	prof := &profile.Profile{
		Path:      []string{"{tc}/bin"},
		Env:       map[string]string{"TC_ROOT": "{tc}"},
		Configure: []string{"-DBOARD={board}", "-DTC={tc}"},
	}
	lock := &project.Lock{Packages: []project.LockedPackage{
		{Name: "tc", Platform: "any", Source: profile.Source{Digest: digest}},
	}}
	e, err := Resolve(prof, lock, st, "linux-x64", "pico")
	must(t, err)
	dir, _ := st.Path(digest)
	if e.Path[0] != filepath.Join(dir, "bin") || e.Vars["TC_ROOT"] != dir || e.Configure[0] != "-DBOARD=pico" {
		t.Fatalf("Resolve = %+v", e)
	}
	if p, err := e.LookPath("cmake"); err != nil || p != filepath.Join(dir, "bin", "cmake") {
		t.Fatalf("LookPath = %q, %v", p, err)
	}
	if _, err := e.LookPath("sh"); err == nil {
		t.Fatal("LookPath found a host program")
	}

	lock.Packages[0].Source.Digest = "sha256:" + strings.Repeat("0", 64)
	if _, err := Resolve(prof, lock, st, "linux-x64", "pico"); err == nil || !strings.Contains(err.Error(), "not in the local store") {
		t.Fatalf("err = %v, want missing package", err)
	}
}

func TestLeaks(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "CMakeCache.txt")
	must(t, os.WriteFile(cache, []byte(`# comment
//Help text /usr/bin/ignored
CMAKE_C_COMPILER:STRING=/store/packages/abc/bin/arm-none-eabi-gcc
CMAKE_INSTALL_PREFIX:PATH=/usr/local
_Python3_EXECUTABLE:INTERNAL=/usr/bin/python3.14
SOME_LIST:STRING=/proj/src;/opt/vendor/lib
CMAKE_HOME_DIRECTORY:INTERNAL=/proj/src
NOT_A_PATH:STRING=Release
`), 0o644))
	leaks, err := Leaks(cache, []string{"/store", "/proj/src", "/proj/build"})
	must(t, err)
	want := []string{"_Python3_EXECUTABLE=/usr/bin/python3.14", "SOME_LIST=/opt/vendor/lib"}
	if strings.Join(leaks, "|") != strings.Join(want, "|") {
		t.Fatalf("Leaks = %v, want %v", leaks, want)
	}
}

func TestCmakeWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell wrapper")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "real cmake")
	must(t, os.WriteFile(fake, []byte("#!/bin/sh\nfor a in \"$@\"; do echo \"[$a]\"; done\n"), 0o755))
	wrapper := filepath.Join(dir, "cmake")
	must(t, os.WriteFile(wrapper, []byte(cmakeWrapper(fake, []string{"-DX=it's here", "-G", "Ninja"})), 0o755))

	run := func(args ...string) string {
		out, err := exec.Command(wrapper, args...).Output()
		must(t, err)
		return strings.TrimSpace(string(out))
	}
	if got := run("-S", ".", "-B", "build"); got != "[-S]\n[.]\n[-B]\n[build]\n[-DX=it's here]\n[-G]\n[Ninja]" {
		t.Errorf("configure args:\n%s", got)
	}
	if got := run("--build", "build"); got != "[--build]\n[build]" {
		t.Errorf("--build must pass through unchanged:\n%s", got)
	}
}

// TestIsolationHasNoNetwork checks that an isolated command sees only the
// loopback interface and keeps the caller's uid.
func TestIsolationHasNoNetwork(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	var out bytes.Buffer
	r := &runner{env: []string{"PATH=/usr/bin:/bin"}, dir: "/", isolate: true, o: Options{Stdout: &out}}
	// /proc/net follows the network namespace of the reading process;
	// /sys/class/net would show the namespace sysfs was mounted in.
	must(t, r.run("/bin/sh", "-c", "tail -n +3 /proc/net/dev | cut -d: -f1 | tr -d ' '; id -u"))
	if !r.isolate {
		t.Skip("user namespaces are not available on this host")
	}
	fields := strings.Fields(out.String())
	if len(fields) != 2 || fields[0] != "lo" {
		t.Fatalf("isolated interfaces and uid = %q, want only lo", out.String())
	}
	if fields[1] != strconv.Itoa(os.Getuid()) {
		t.Fatalf("uid inside = %s, want %d (not namespace root)", fields[1], os.Getuid())
	}
}

func TestIsolationAvailable(t *testing.T) {
	got := IsolationAvailable()
	if runtime.GOOS != "linux" && got {
		t.Fatal("isolation reported outside Linux")
	}
	t.Logf("isolation available: %v", got)
}
