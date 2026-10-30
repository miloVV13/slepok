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
	cmakeName := "cmake"
	if runtime.GOOS == "windows" {
		cmakeName += ".exe"
	}
	must(t, os.WriteFile(filepath.Join(tool, "bin", cmakeName), []byte("#!/bin/sh\n"), 0o755))
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
	if filepath.Clean(e.Path[0]) != filepath.Join(dir, "bin") || e.Vars["TC_ROOT"] != dir || e.Configure[0] != "-DBOARD=pico" {
		t.Fatalf("Resolve = %+v", e)
	}
	if p, err := e.LookPath("cmake"); err != nil || p != filepath.Join(dir, "bin", cmakeName) {
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

func TestWindowsBuildPathKeepsPinnedToolsFirst(t *testing.T) {
	t.Setenv("PATH", "untrusted-host-tools")
	tools := []string{"pinned-cmake", "pinned-gcc"}
	root := t.TempDir()
	got := windowsBuildPath(tools, root)
	if strings.Join(got, "|") != strings.Join([]string{"pinned-cmake", "pinned-gcc", filepath.Join(root, "System32")}, "|") {
		t.Fatalf("build path = %v", got)
	}
	got[0] = "changed"
	if tools[0] != "pinned-cmake" {
		t.Fatal("mutated pinned paths")
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

func TestNinjaLeaks(t *testing.T) {
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "CMakeFiles"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "build.ninja"), []byte(`# /usr/bin/ignored-comment
command = /store/bin/cmake -S /opt/my$ project -B /build
flags = -I/usr/include -L/opt/vendor/lib --sysroot=/usr/sysroot
`), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "CMakeFiles", "rules.ninja"), []byte(`command = "/usr/bin/python3" gen.py
command = "/opt/my project/tool" '/store/bin/cc'
command = /store/bin/cc -isystem/usr/local/include
command = "C$:/Program$ Files/Python/python.exe" generate.py
command = "C$:/Store With Spaces/bin/cmake.exe"
`), 0o644))
	leaks, err := NinjaLeaks(dir, []string{"/store", "/opt/my project", "/build", `C:\Store With Spaces`})
	must(t, err)
	for _, want := range []string{"/usr/bin/python3", "/usr/include", "/opt/vendor/lib", "/usr/sysroot", "/usr/local/include", "C:/Program Files/Python/python.exe"} {
		if !strings.Contains(strings.Join(leaks, "\n"), want) {
			t.Errorf("missing %s in %v", want, leaks)
		}
	}
	if len(leaks) != 6 {
		t.Fatalf("unexpected Ninja leaks: %v", leaks)
	}
}

func TestWindowsHostPaths(t *testing.T) {
	allowed := []string{`C:\Users\User\Slepok\packages`, `D:\Project With Spaces`, `\\server\share\store`}
	for _, tt := range []struct {
		value string
		leak  bool
	}{
		{`c:/users/user/slepok/packages/hash/bin/cmake.exe`, false},
		{`D:\PROJECT WITH SPACES\build\app.elf`, false},
		{`\\SERVER\share\store\hash\python.exe`, false},
		{`C:\Program Files\Python\python.exe`, true},
		{`C:/Users/User/Slepok/packages-other/python.exe`, true},
		{`\\server\share\other\tool.exe`, true},
	} {
		if got := hostPath(tt.value, allowed); got != tt.leak {
			t.Errorf("hostPath(%q) = %v", tt.value, got)
		}
	}
}

func TestWindowsSystemCommandIsAnExactException(t *testing.T) {
	dir := t.TempDir()
	cmd := `C:\Windows\System32\cmd.exe`
	cache := filepath.Join(dir, "CMakeCache.txt")
	must(t, os.WriteFile(cache, []byte("SHELL:FILEPATH=C:/WINDOWS/System32/cmd.exe\nPYTHON:FILEPATH=C:/Windows/System32/python.exe\n"), 0644))
	must(t, os.WriteFile(filepath.Join(dir, "build.ninja"), []byte("command = C:\\windows\\System32\\cmd.exe /C pinned-tool\ncommand = C:\\Windows\\System32\\python.exe generate.py\ncommand = D:\\Windows\\System32\\cmd.exe /C other\n"), 0644))
	leaks, err := Leaks(cache, nil, cmd)
	must(t, err)
	if len(leaks) != 1 || !strings.Contains(leaks[0], "python.exe") {
		t.Fatalf("cache leaks: %v", leaks)
	}
	leaks, err = NinjaLeaks(dir, nil, cmd)
	must(t, err)
	if len(leaks) != 2 || !strings.Contains(strings.Join(leaks, "\n"), "python.exe") || !strings.Contains(strings.Join(leaks, "\n"), `D:\Windows\System32\cmd.exe`) {
		t.Fatalf("Ninja leaks: %v", leaks)
	}
	for _, name := range []string{`C:\Windows\System32\cmd.exe.extra`, `C:\Windows\System32\cmd.exe\tool`, `C:\Windows\System32-other\cmd.exe`} {
		if !hostPath(name, nil, cmd) {
			t.Fatalf("overbroad exception: %s", name)
		}
	}
}

func TestResolveWindowsHostOverrides(t *testing.T) {
	st, err := store.Open(t.TempDir())
	must(t, err)
	t.Cleanup(func() { store.RemoveTree(st.Root()) })
	tools := t.TempDir()
	must(t, os.WriteFile(filepath.Join(tools, "python.exe"), []byte("fixture"), 0o644))
	digest, err := st.InstallDir(tools, "")
	must(t, err)
	prof := &profile.Profile{Path: []string{"{python}/bin"}, Env: map[string]string{"SHARED": "yes"}, Configure: []string{"-G", "Ninja"}, Hosts: map[string]profile.Host{"windows-x64": {Path: []string{"{python}"}, Configure: []string{"-DPython3_EXECUTABLE={python}/python.exe"}, Env: map[string]string{"PYTHONNOUSERSITE": "1"}}}}
	lock := &project.Lock{Packages: []project.LockedPackage{{Name: "python", Platform: "windows-x64", Source: profile.Source{Digest: digest}}}}
	env, err := Resolve(prof, lock, st, "windows-x64", "pico")
	must(t, err)
	dir, err := st.Path(digest)
	must(t, err)
	if len(env.Path) != 1 || env.Path[0] != dir || env.Vars["SHARED"] != "yes" || env.Vars["PYTHONNOUSERSITE"] != "1" {
		t.Fatalf("host settings were not resolved: %+v", env)
	}
	if !strings.Contains(strings.Join(env.Configure, " "), "-DPython3_EXECUTABLE="+dir+"/python.exe") {
		t.Fatalf("missing pinned Python argument: %v", env.Configure)
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

func TestResolveSourceDateEpoch(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { store.RemoveTree(root) })
	st, err := store.Open(root)
	must(t, err)
	prof := &profile.Profile{}
	lock := &project.Lock{SourceDateEpoch: 1577836800}

	t.Setenv("SOURCE_DATE_EPOCH", "")
	e, err := Resolve(prof, lock, st, "linux-x64", "pico")
	must(t, err)
	if got := e.Vars["SOURCE_DATE_EPOCH"]; got != "1577836800" {
		t.Fatalf("SOURCE_DATE_EPOCH = %q, want the lock's value", got)
	}
	t.Setenv("SOURCE_DATE_EPOCH", "42")
	e, err = Resolve(prof, lock, st, "linux-x64", "pico")
	must(t, err)
	if got := e.Vars["SOURCE_DATE_EPOCH"]; got != "42" {
		t.Fatalf("SOURCE_DATE_EPOCH = %q, want the user's value", got)
	}
}
