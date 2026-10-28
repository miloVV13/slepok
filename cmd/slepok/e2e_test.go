package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/project"
	"github.com/miloVV13/slepok/internal/runner"
	"github.com/miloVV13/slepok/internal/snapshot"
	"github.com/miloVV13/slepok/internal/store"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustFail(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to mention %q", err, want)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// sdkRepo creates a git SDK whose submodule is pinned below the tip of its
// branch. It returns the repository URL and the pinned commit.
func sdkRepo(t *testing.T) (string, string) {
	base := t.TempDir()
	sub := filepath.Join(base, "sub")
	git(t, base, "init", "-q", sub)
	git(t, sub, "config", "uploadpack.allowReachableSHA1InWant", "true")
	must(t, os.WriteFile(filepath.Join(sub, "sub.txt"), []byte("sub v1\n"), 0o644))
	git(t, sub, "add", ".")
	git(t, sub, "commit", "-q", "-m", "v1")
	pinned := git(t, sub, "rev-parse", "HEAD")
	must(t, os.WriteFile(filepath.Join(sub, "sub.txt"), []byte("sub v2\n"), 0o644))
	git(t, sub, "commit", "-q", "-am", "v2")

	sdk := filepath.Join(base, "sdk")
	git(t, base, "init", "-q", sdk)
	must(t, os.WriteFile(filepath.Join(sdk, "version.txt"), []byte("sdk 2.0\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(sdk, ".gitmodules"), []byte("[submodule \"lib/sub\"]\n\tpath = lib/sub\n\turl = file://"+sub+"\n"), 0o644))
	git(t, sdk, "add", ".")
	git(t, sdk, "update-index", "--add", "--cacheinfo", "160000,"+pinned+",lib/sub")
	git(t, sdk, "commit", "-q", "-m", "sdk")
	git(t, sdk, "tag", "v2.0")
	return "file://" + sdk, git(t, sdk, "rev-parse", "HEAD")
}

// toolchainArchive packs the fake cmake as tc-1.0/bin/cmake.
func toolchainArchive(t *testing.T, cmake string) []byte {
	bin, err := os.ReadFile(cmake)
	must(t, err)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range []*tar.Header{
		{Name: "tc-1.0/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "tc-1.0/bin/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "tc-1.0/bin/cmake", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(bin))},
	} {
		must(t, tw.WriteHeader(h))
		if h.Typeflag == tar.TypeReg {
			_, err := tw.Write(bin)
			must(t, err)
		}
	}
	must(t, tw.Close())
	must(t, gz.Close())
	return buf.Bytes()
}

// fakeProfile installs a profile whose toolchain is served by srvURL and
// whose SDK comes from a local git repository.
func fakeProfile(t *testing.T, srvURL string, archive, source []byte, sdkURL, sdkCommit string) {
	sum := sha256.Sum256(archive)
	srcSum := sha256.Sum256(source)
	toml := `id = "fake"
version = "1"
description = "end-to-end test profile"
boards = ["board-a", "board-b"]
path = ["{tc}/bin"]
configure = ["-DBOARD={board}", "-DSDK={sdk}"]
outputs = ["*.uf2"]

[env]
SDK_PATH = "{sdk}"

[[components]]
name = "tc"
version = "1.0"
license = "GPL-3.0-or-later"
[components.source_code]
url = "` + srvURL + `/tc-src.tar.gz"
sha256 = "` + hex.EncodeToString(srcSum[:]) + `"
[components.sources.` + manifest.HostPlatform() + `]
url = "` + srvURL + `/tc.tar.gz"
sha256 = "` + hex.EncodeToString(sum[:]) + `"
format = "tar.gz"
strip = 1

[[components]]
name = "sdk"
version = "2.0"
license = "BSD-3-Clause"
notice = "test notice: non-commercial part inside"
[components.sources.any]
git = "` + sdkURL + `"
ref = "v2.0"
commit = "` + sdkCommit + `"
`
	saved := profile.FS
	profile.FS = fstest.MapFS{
		"profiles/fake.toml":            {Data: []byte(toml)},
		"templates/fake/CMakeLists.txt": {Data: []byte("project(@NAME@)\n")},
		"templates/fake/main.c":         {Data: []byte("int main(void) { return 0; } /* @NAME@ */\n")},
		"templates/fake/gitignore":      {Data: []byte("/build/\n")},
	}
	t.Cleanup(func() { profile.FS = saved })
}

// TestEndToEnd drives the CLI through the whole life of a project: create,
// build and pack it on machine A, then restore and build it on machine B
// without network. Real code paths throughout; only the profile, cmake and
// the download server are stand-ins.
func TestEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	fakeCmake := filepath.Join(tmp, "fakecmake")
	if out, err := exec.Command("go", "build", "-o", fakeCmake, "./testdata/fakecmake").CombinedOutput(); err != nil {
		t.Fatalf("build fakecmake: %v\n%s", err, out)
	}
	archive := toolchainArchive(t, fakeCmake)
	source := []byte("the corresponding source of tc")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tc.tar.gz":
			w.Write(archive)
		case "/tc-src.tar.gz":
			w.Write(source)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	sdkURL, sdkCommit := sdkRepo(t)
	fakeProfile(t, srv.URL, archive, source, sdkURL, sdkCommit)

	isolated := runner.IsolationAvailable()
	probeLog := filepath.Join(tmp, "probe.log")
	probe := strings.TrimPrefix(srv.URL, "http://") + "\n" + probeLog + "\n"
	homeA, homeB := filepath.Join(tmp, "homeA"), filepath.Join(tmp, "homeB")
	t.Cleanup(func() { store.RemoveTree(homeA); store.RemoveTree(homeB) })
	work := filepath.Join(tmp, "work")
	must(t, os.MkdirAll(work, 0o755))

	// --- machine A -----------------------------------------------------
	t.Setenv("SLEPOK_HOME", homeA)
	t.Chdir(work)
	must(t, execute([]string{"init", "--profile", "fake", "proj"}))
	proj := filepath.Join(work, "proj")
	for _, f := range []string{"CMakeLists.txt", "main.c", ".gitignore", project.ConfigName, project.LockName} {
		if _, err := os.Stat(filepath.Join(proj, f)); err != nil {
			t.Fatalf("init did not create %s", f)
		}
	}
	p, err := project.Load(proj)
	must(t, err)
	if p.Config.Board != "board-a" || len(p.Lock.Packages) != 2 {
		t.Fatalf("project after init: %+v, lock %+v", p.Config, p.Lock)
	}
	for _, pkg := range p.Lock.Packages {
		if pkg.Source.Digest == "" {
			t.Fatalf("lock has no digest for %s", pkg.Name)
		}
	}
	mustFail(t, execute([]string{"init", "--board", "board-b", "proj"}), "already sets")

	t.Chdir(proj)
	must(t, os.WriteFile("probe.txt", []byte(probe), 0o644))
	must(t, os.WriteFile("extra.c", []byte("/* extra */\n"), 0o644))
	must(t, execute([]string{"build"}))
	firmwareA, err := os.ReadFile(filepath.Join(proj, "build", "app.uf2"))
	must(t, err)
	if !bytes.Contains(firmwareA, []byte("extra")) || !bytes.Contains(firmwareA, []byte("sub v1")) {
		t.Fatalf("firmware %q lacks the sources or the pinned submodule", firmwareA)
	}

	// pack refuses secrets and builds that use host tools.
	must(t, os.WriteFile(".env", []byte("TOKEN=x\n"), 0o644))
	mustFail(t, execute([]string{"pack", "-o", "../proj.slepok"}), "secrets")
	must(t, os.Remove(".env"))
	must(t, os.WriteFile("leak", nil, 0o644))
	mustFail(t, execute([]string{"pack", "-o", "../proj.slepok"}), "depends on the host")
	must(t, os.Remove("leak"))

	must(t, os.WriteFile(probeLog, nil, 0o644))
	snap := filepath.Join(work, "proj.slepok")
	must(t, execute([]string{"pack", "-o", snap}))
	if log, _ := os.ReadFile(probeLog); isolated && strings.TrimSpace(string(log)) != "unreachable" {
		t.Fatalf("verification build reached the network: %q", log)
	}
	must(t, execute([]string{"validate", snap}))
	must(t, execute([]string{"inspect", snap}))
	checkLicenses(t, snap, srv.URL, nil)

	// -with-sources bundles the published source code of GPL components.
	withSrc := filepath.Join(work, "proj-src.slepok")
	must(t, execute([]string{"pack", "-with-sources", "-o", withSrc}))
	checkLicenses(t, withSrc, srv.URL, source)
	mustFail(t, execute([]string{"pack", "-o", snap}), "exists")

	corrupt, err := os.ReadFile(snap)
	must(t, err)
	corrupt[len(corrupt)/2] ^= 0xff
	bad := filepath.Join(work, "bad.slepok")
	must(t, os.WriteFile(bad, corrupt, 0o644))
	if err := execute([]string{"validate", bad}); err == nil {
		t.Fatal("validate accepted a corrupted snapshot")
	}

	// --- machine B: empty store, no download server --------------------
	srv.Close()
	t.Setenv("SLEPOK_HOME", homeB)
	t.Chdir(work)
	mustFail(t, execute([]string{"unpack", snap, "proj"}), "exists")
	must(t, execute([]string{"unpack", snap, "restored"}))
	t.Chdir(filepath.Join(work, "restored"))
	must(t, execute([]string{"build"}))
	firmwareB, err := os.ReadFile(filepath.Join(work, "restored", "build", "app.uf2"))
	must(t, err)
	if !bytes.Equal(firmwareA, firmwareB) {
		t.Fatalf("firmware differs between machines:\nA %q\nB %q", firmwareA, firmwareB)
	}
	// Every package came with the snapshot, so init needs no network.
	must(t, execute([]string{"init"}))
	must(t, execute([]string{"list"}))

	// gc keeps everything the restored project uses.
	must(t, execute([]string{"gc", "-y"}))
	must(t, execute([]string{"build"}))

	// --- back on A: a deleted project is forgotten, its young packages kept
	t.Setenv("SLEPOK_HOME", homeA)
	t.Chdir(work)
	must(t, os.RemoveAll(proj))
	must(t, execute([]string{"gc", "-y"}))
	st, err := store.Open(homeA)
	must(t, err)
	projects, err := st.Projects()
	must(t, err)
	if len(projects) != 0 {
		t.Fatalf("gc kept stale projects: %+v", projects)
	}
	pkgs, err := st.Packages()
	must(t, err)
	if len(pkgs) != 2 {
		t.Fatalf("gc removed packages younger than the grace period: %v", pkgs)
	}
}

// checkLicenses verifies the license information of a snapshot: the NOTICE,
// the per-package source code and notice fields, and, when source is not
// nil, the bundled source archive.
func checkLicenses(t *testing.T, snap, srvURL string, source []byte) {
	t.Helper()
	m, err := snapshot.Validate(snap)
	must(t, err)
	files := map[string]bool{}
	for _, f := range m.Files {
		files[f.Path] = true
	}
	if !files[noticeEntry] {
		t.Fatalf("%s has no %s", snap, noticeEntry)
	}
	for _, p := range m.Packages {
		switch p.Name {
		case "tc":
			if p.SourceCode != srvURL+"/tc-src.tar.gz" {
				t.Errorf("tc source code = %q", p.SourceCode)
			}
		case "sdk":
			if !strings.Contains(p.Notice, "non-commercial") {
				t.Errorf("sdk notice = %q", p.Notice)
			}
		}
	}
	bundled := files["sources/tc-src.tar.gz"]
	if (source != nil) != bundled {
		t.Fatalf("source bundled = %v, want %v", bundled, source != nil)
	}
	if source == nil {
		return
	}
	out := t.TempDir()
	if b, err := exec.Command("tar", "--zstd", "-xf", snap, "-C", out, "sources/").CombinedOutput(); err != nil {
		t.Skipf("tar cannot extract zstd here: %v %s", err, b)
	}
	got, err := os.ReadFile(filepath.Join(out, "sources", "tc-src.tar.gz"))
	must(t, err)
	if !bytes.Equal(got, source) {
		t.Fatal("bundled source differs from the published one")
	}
}
