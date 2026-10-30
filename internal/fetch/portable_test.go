package fetch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/miloVV13/slepok/internal/pkgtar"
	"github.com/miloVV13/slepok/internal/profile"
)

func TestPortableArchivePreservesUnixMetadata(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range []struct {
		name, body, link string
		kind             byte
		mode             int64
	}{
		{"root/z", "tool", "", tar.TypeReg, 0755},
		{"root/a/first", "", "root/z", tar.TypeLink, 0755},
		{"root/a/link", "", "../z", tar.TypeSymlink, 0777},
		{"root/a.txt", "data", "", tar.TypeReg, 0644},
		{"root/__pycache__/cache", "discard", "", tar.TypeReg, 0644},
	} {
		requireOK(t, tw.WriteHeader(&tar.Header{Name: e.name, Typeflag: e.kind, Linkname: e.link, Mode: e.mode, Size: int64(len(e.body))}))
		_, err := io.WriteString(tw, e.body)
		requireOK(t, err)
	}
	requireOK(t, tw.Close())
	requireOK(t, gz.Close())
	sum := sha256.Sum256(buf.Bytes())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(buf.Bytes()) }))
	defer srv.Close()
	src := profile.Source{URL: srv.URL, SHA256: hex.EncodeToString(sum[:]), Format: "tar.gz", Strip: 1}
	if runtime.GOOS != "windows" {
		var err error
		src.Digest, err = newFetcher(t).Install("test", src)
		requireOK(t, err)
	}
	f := newFetcher(t)
	digest, err := f.InstallPortable("test", src)
	requireOK(t, err)
	file, err := f.Store.ArchivePath(digest)
	requireOK(t, err)
	in, err := os.Open(file)
	requireOK(t, err)
	defer in.Close()
	tr := tar.NewReader(in)
	seen := map[string]*tar.Header{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		requireOK(t, err)
		seen[h.Name] = h
	}
	if seen["a/first"].Mode != 0755 || seen["a/first"].Typeflag != tar.TypeReg || seen["z"].Typeflag != tar.TypeLink || seen["z"].Linkname != "a/first" || seen["a/link"].Linkname != "../z" {
		t.Fatal("Unix modes or links changed", seen)
	}
	if seen["__pycache__"] != nil {
		t.Fatal("cache was retained")
	}
	if runtime.GOOS != "windows" {
		// Promoting an archived package for execution needs no network.
		src.Digest = digest
		srv.Close()
		_, err = f.Install("test", src)
		requireOK(t, err)
		if !f.Store.HasTree(digest) {
			t.Fatal("archive not promoted")
		}
	}
}

// Uses cached vendor archives only; no network and no Linux executables run.
// The exact preparation path used by Windows must retain all pinned digests.
func TestRealPortableArchives(t *testing.T) {
	dir := os.Getenv("SLEPOK_LINUX_ARCHIVES")
	if dir == "" {
		t.Skip("SLEPOK_LINUX_ARCHIVES not set")
	}
	names := map[string]string{"arm-gcc": "arm-gcc.tar.xz", "cmake": "cmake.tar.gz", "ninja": "ninja.zip", "python": "python.tar.gz", "pioasm": "pico-sdk-tools.tar.gz", "picotool": "picotool.tar.gz"}
	p, err := profile.Load("rp2040")
	requireOK(t, err)
	for _, c := range p.Components {
		name, ok := names[c.Name]
		if !ok {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			src, _, err := c.Source("linux-x64")
			requireOK(t, err)
			file := filepath.Join(dir, name)
			sum, err := fileSHA256(file)
			requireOK(t, err)
			if sum != src.SHA256 {
				t.Fatalf("vendor checksum mismatch for %s", file)
			}
			tree := pkgtar.NewTree(t.TempDir())
			requireOK(t, extractArchive(t.Context(), file, src.Format, src.Strip, portableSink{tree, t.Context()}))
			tree.Clean(func(s string) bool { return s == ".git" || s == "__pycache__" })
			digest, n, err := tree.Write(t.Context(), io.Discard)
			requireOK(t, err)
			if digest != src.Digest {
				t.Fatalf("digest = %s, want %s", digest, src.Digest)
			}
			t.Logf("%s: %s, %d bytes", c.Name, digest, n)
		})
	}
}

func TestPortableArchiveRejectsWritesThroughLinks(t *testing.T) {
	tree := pkgtar.NewTree(t.TempDir())
	requireOK(t, tree.Symlink("link", "directory"))
	if err := tree.File(t.Context(), "link/file", 0644, strings.NewReader("escape")); err == nil {
		t.Fatal("accepted write through link")
	}
}
