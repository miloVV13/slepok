package fetch

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/store"
)

type entry struct {
	name, link string
	typ        byte
	mode       int64
	body       string
}

func tarGz(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{Name: e.name, Linkname: e.link, Typeflag: e.typ, Mode: mode, Size: int64(len(e.body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(e.body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "archive")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

var goodTree = []entry{
	{name: "./top/", typ: tar.TypeDir},
	{name: "./top/bin/tool", typ: tar.TypeReg, mode: 0o755, body: "tool"},
	{name: "./top/bin/tool-1", typ: tar.TypeLink, link: "./top/bin/tool"},
	{name: "./top/bin/alias", typ: tar.TypeSymlink, link: "tool"},
	{name: "./top/lib/.git/config", typ: tar.TypeReg, body: "junk"},
	{name: "./top/lib/__pycache__/x.pyc", typ: tar.TypeReg, body: "junk"},
	{name: "./top/lib/sub/.git", typ: tar.TypeReg, body: "gitdir: ../../.git/modules/sub"},
	{name: "./top/lib/data", typ: tar.TypeReg, body: "data"},
}

func TestExtractStrip(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	if err := extract(writeTemp(t, tarGz(t, goodTree...)), "tar.gz", 1, dest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dest, "bin", "tool"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("bin/tool: %v, %v", info, err)
	}
	linked, _ := os.Stat(filepath.Join(dest, "bin", "tool-1"))
	if !os.SameFile(info, linked) {
		t.Error("hardlink not preserved")
	}
	if target, _ := os.Readlink(filepath.Join(dest, "bin", "alias")); target != "tool" {
		t.Errorf("symlink target %q", target)
	}
	if err := clean(dest); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"lib/.git", "lib/__pycache__", "lib/sub/.git"} {
		if _, err := os.Lstat(filepath.Join(dest, gone)); err == nil {
			t.Errorf("%s not cleaned", gone)
		}
	}
}

func TestExtractRejectsMalicious(t *testing.T) {
	tests := map[string][]entry{
		"dotdot":          {{name: "top/../../evil", typ: tar.TypeReg, body: "x"}},
		"absolute":        {{name: "/etc/evil", typ: tar.TypeReg, body: "x"}},
		"symlink escape":  {{name: "top/l", typ: tar.TypeSymlink, link: "../../outside"}},
		"through symlink": {{name: "top/d/", typ: tar.TypeDir}, {name: "top/l", typ: tar.TypeSymlink, link: "d"}, {name: "top/l/f", typ: tar.TypeReg, body: "x"}},
		"bad hardlink":    {{name: "top/h", typ: tar.TypeLink, link: "top/missing"}},
		"device":          {{name: "top/dev", typ: tar.TypeChar}},
	}
	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			if err := extract(writeTemp(t, tarGz(t, entries...)), "tar.gz", 1, filepath.Join(parent, "out")); err == nil {
				t.Fatal("extract accepted a malicious archive")
			}
			got, _ := os.ReadDir(parent)
			for _, e := range got {
				if e.Name() != "out" {
					t.Errorf("wrote %q outside destination", e.Name())
				}
			}
		})
	}
}

func TestExtractZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	h := &zip.FileHeader{Name: "ninja", Method: zip.Deflate}
	h.SetMode(0o755)
	w, _ := zw.CreateHeader(h)
	w.Write([]byte("ninja"))
	zw.Close()
	dest := filepath.Join(t.TempDir(), "out")
	if err := extract(writeTemp(t, buf.Bytes()), "zip", 0, dest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dest, "ninja"))
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("ninja: %v, %v", info, err)
	}
}

// flakyServer serves data, cutting the first response off halfway, and
// honours range requests like a CDN.
func flakyServer(t *testing.T, data []byte) (*httptest.Server, *atomic.Int32) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		start := 0
		if rng := r.Header.Get("Range"); rng != "" {
			start, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-"))
			w.Header().Set("Content-Range", "bytes "+strconv.Itoa(start)+"-"+strconv.Itoa(len(data)-1)+"/"+strconv.Itoa(len(data)))
			w.WriteHeader(http.StatusPartialContent)
		}
		if n == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.Write(data[:len(data)/2])
			panic(http.ErrAbortHandler)
		}
		w.Write(data[start:])
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

func newFetcher(t *testing.T) *Fetcher {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(func() { store.RemoveTree(root) })
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	f := New(st, nil)
	f.RetryDelay = 0
	return f
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestInstallResumesDownload(t *testing.T) {
	// Large enough that the cut-off response leaves a partial file.
	big := strings.Repeat("0123456789abcdef", 64<<10)
	data := tarGz(t, append(goodTree, entry{name: "./top/big", typ: tar.TypeReg, body: big})...)
	srv, requests := flakyServer(t, data)
	f := newFetcher(t)

	src := profile.Source{URL: srv.URL + "/a.tar.gz", SHA256: sha(data), Format: "tar.gz", Strip: 1}
	digest, err := f.Install("tool", src)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() < 2 {
		t.Fatalf("server saw %d requests, want a retry", requests.Load())
	}
	if !f.Store.Has(digest) {
		t.Fatal("package not installed")
	}
	p, _ := f.Store.Path(digest)
	if _, err := os.Lstat(filepath.Join(p, "lib", "__pycache__")); err == nil {
		t.Error("package was not cleaned")
	}

	// Pinning the digest makes a second install a no-op, even offline.
	src.Digest = digest
	src.URL = "http://127.0.0.1:1/unreachable"
	if again, err := f.Install("tool", src); err != nil || again != digest {
		t.Fatalf("pinned reinstall = %s, %v", again, err)
	}
}

func TestInstallRejectsBadChecksum(t *testing.T) {
	data := tarGz(t, goodTree...)
	srv, _ := flakyServer(t, data)
	f := newFetcher(t)
	f.Retries = 3
	src := profile.Source{URL: srv.URL + "/a.tar.gz", SHA256: strings.Repeat("0", 64), Format: "tar.gz", Strip: 1}
	if _, err := f.Install("tool", src); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
	downloads, _ := os.ReadDir(filepath.Join(f.Store.Root(), "downloads"))
	if len(downloads) != 0 {
		t.Fatalf("bad download kept in cache: %v", downloads)
	}
}

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestCloneSubmodules pins a submodule to a commit that is not the tip of
// its branch, the case "git submodule update --depth 1" handles badly.
func TestCloneSubmodules(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	sub := filepath.Join(base, "sub")
	gitCmd(t, base, "init", "-q", sub)
	gitCmd(t, sub, "config", "uploadpack.allowReachableSHA1InWant", "true")
	os.WriteFile(filepath.Join(sub, "a.txt"), []byte("v1"), 0o644)
	gitCmd(t, sub, "add", "a.txt")
	gitCmd(t, sub, "commit", "-q", "-m", "v1")
	pinned := gitCmd(t, sub, "rev-parse", "HEAD")
	os.WriteFile(filepath.Join(sub, "a.txt"), []byte("v2"), 0o644)
	gitCmd(t, sub, "commit", "-q", "-am", "v2")

	super := filepath.Join(base, "super")
	gitCmd(t, base, "init", "-q", super)
	os.WriteFile(filepath.Join(super, ".gitmodules"), []byte("[submodule \"lib/sub\"]\n\tpath = lib/sub\n\turl = "+fileURL(sub)+"\n"), 0o644)
	gitCmd(t, super, "add", ".gitmodules")
	gitCmd(t, super, "update-index", "--add", "--cacheinfo", "160000,"+pinned+",lib/sub")
	gitCmd(t, super, "commit", "-q", "-m", "super")
	gitCmd(t, super, "tag", "v1.0")
	commit := gitCmd(t, super, "rev-parse", "HEAD")

	f := newFetcher(t)
	dest := filepath.Join(t.TempDir(), "sdk")
	if err := f.clone("sdk", profile.Source{Git: fileURL(super), Ref: "v1.0", Commit: commit}, dest); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "lib", "sub", "a.txt")); err != nil || string(data) != "v1" {
		t.Fatalf("submodule content = %q, %v; want the pinned v1", data, err)
	}

	wrong := strings.Repeat("0", 40)
	if err := f.clone("sdk", profile.Source{Git: fileURL(super), Ref: "v1.0", Commit: wrong}, filepath.Join(t.TempDir(), "x")); err == nil || !strings.Contains(err.Error(), "moved") {
		t.Fatalf("clone with wrong pinned commit: err = %v", err)
	}
}

func TestInstallRejectsWrongDigest(t *testing.T) {
	data := tarGz(t, goodTree...)
	srv, _ := flakyServer(t, data)
	f := newFetcher(t)
	src := profile.Source{URL: srv.URL + "/a.tar.gz", SHA256: sha(data), Format: "tar.gz", Strip: 1,
		Digest: "sha256:" + strings.Repeat("1", 64)}
	if _, err := f.Install("tool", src); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("err = %v, want digest mismatch", err)
	}
	entries, _ := os.ReadDir(filepath.Join(f.Store.Root(), "packages"))
	for _, e := range entries {
		t.Errorf("package dir %s left behind", e.Name())
	}
}

func TestDownloadSurvivesStalledConnection(t *testing.T) {
	data := []byte(strings.Repeat("x", 1<<20))
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := 0
		if rng := r.Header.Get("Range"); rng != "" {
			start, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-"))
			w.WriteHeader(http.StatusPartialContent)
		}
		if requests.Add(1) == 1 {
			// Send half, then go silent without closing the connection.
			w.Write(data[:len(data)/2])
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		w.Write(data[start:])
	}))
	defer srv.Close()
	f := newFetcher(t)
	f.IdleTimeout = 200 * time.Millisecond
	path, err := f.Download("big", srv.URL+"/big", sha(data))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, data) {
		t.Fatal("downloaded data differs")
	}
}

func TestDownloadFallsBackToMirror(t *testing.T) {
	data := []byte("from the mirror")
	var deadHits atomic.Int32
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadHits.Add(1)
		http.Error(w, "blocked", http.StatusForbidden)
	}))
	defer dead.Close()
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer mirror.Close()

	f := newFetcher(t)
	path, err := f.download("file", profile.Source{URL: dead.URL + "/f", Mirrors: []string{mirror.URL + "/f"}, SHA256: sha(data)})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, data) {
		t.Fatal("wrong content")
	}
	if deadHits.Load() != 1 {
		t.Fatalf("a permanently failing URL was tried %d times", deadHits.Load())
	}
}

// TestDownloadKeepsGoingWhileProgressing cuts every response short: more
// attempts than Retries are needed, which is fine as long as each one
// brings new data.
func TestDownloadKeepsGoingWhileProgressing(t *testing.T) {
	data := []byte(strings.Repeat("0123456789", 10000))
	const chunk = 7000
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := 0
		if rng := r.Header.Get("Range"); rng != "" {
			start, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-"))
		}
		end := min(start+chunk, len(data))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)-start))
		if start > 0 {
			w.WriteHeader(http.StatusPartialContent)
		}
		w.Write(data[start:end])
		if end < len(data) {
			panic(http.ErrAbortHandler)
		}
	}))
	defer srv.Close()
	f := newFetcher(t)
	f.Retries = 2
	if _, err := f.Download("chunked", srv.URL+"/c", sha(data)); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadFailureExplainsManualDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable in your region", http.StatusUnavailableForLegalReasons)
	}))
	defer srv.Close()
	f := newFetcher(t)
	want := strings.Repeat("ab", 32)
	_, err := f.Download("tool", srv.URL+"/tool.tar.xz", want)
	if err == nil {
		t.Fatal("download succeeded")
	}
	for _, s := range []string{"451", srv.URL + "/tool.tar.xz", want, filepath.Join(f.Store.Root(), "downloads", want)} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error does not mention %q:\n%v", s, err)
		}
	}
}

func TestCancelStalledDownload(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("partial download"))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := New(st, nil)
	f.Context = ctx
	f.RetryDelay = time.Hour
	done := make(chan error, 1)
	go func() { _, err := f.Download("test", srv.URL, strings.Repeat("a", 64)); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("download = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled download stuck in request or retry delay")
	}
	if _, err := os.Stat(filepath.Join(st.Root(), "downloads", strings.Repeat("a", 64))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled download published cache entry: %v", err)
	}
}
