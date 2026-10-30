package fetch

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/miloVV13/slepok/internal/pkgtar"
	"github.com/miloVV13/slepok/internal/profile"
)

func requireOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func fileURL(dir string) string {
	p := filepath.ToSlash(dir)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

func layoutFile(t *testing.T, dir, name, data string, mode fs.FileMode) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	requireOK(t, os.MkdirAll(filepath.Dir(p), 0o755))
	requireOK(t, os.WriteFile(p, []byte(data), mode))
}

func TestWindowsTreePlaceholders(t *testing.T) {
	dir := t.TempDir()
	layoutFile(t, dir, "bin/tool", "payload", 0o755)
	layoutFile(t, dir, "doc/link", "../bin/tool", 0o644)
	layoutFile(t, dir, "doc/chain", "link", 0o644)
	requireOK(t, windowsTree(context.Background(), dir, map[string]string{"doc/link": "../bin/tool", "doc/chain": "link"}))
	for _, name := range []string{"bin/tool", "doc/link", "doc/chain"} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		info, err := os.Lstat(p)
		requireOK(t, err)
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 != 0 {
			t.Fatalf("%s was not normalized: %v", name, info.Mode())
		}
		data, err := os.ReadFile(p)
		requireOK(t, err)
		if string(data) != "payload" {
			t.Fatalf("%s: wrong materialized data %q", name, data)
		}
	}
}

func TestWindowsTreeMatchesNativeLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix source representation; portable placeholder case tested separately")
	}
	a, b := t.TempDir(), t.TempDir()
	for _, dir := range []string{a, b} {
		layoutFile(t, dir, "bin/tool", "payload", 0o755)
		requireOK(t, os.Mkdir(filepath.Join(dir, "doc"), 0o755))
	}
	requireOK(t, os.Link(filepath.Join(a, "bin", "tool"), filepath.Join(a, "bin", "copy")))
	requireOK(t, os.Symlink("../bin/tool", filepath.Join(a, "doc", "link")))
	layoutFile(t, b, "bin/copy", "payload", 0o644)
	layoutFile(t, b, "doc/link", "../bin/tool", 0o644)
	requireOK(t, os.Chmod(filepath.Join(b, "bin", "tool"), 0o644))
	requireOK(t, windowsTree(context.Background(), a, nil))
	requireOK(t, windowsTree(context.Background(), b, map[string]string{"doc/link": "../bin/tool"}))
	var left, right bytes.Buffer
	digest, _, err := pkgtar.Write(&left, a)
	requireOK(t, err)
	again, _, err := pkgtar.Write(&right, b)
	requireOK(t, err)
	if digest != again || !bytes.Equal(left.Bytes(), right.Bytes()) {
		t.Fatal("canonical Windows packages depend on host link/mode representation")
	}
	tr := tar.NewReader(&left)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		requireOK(t, err)
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			t.Fatalf("Windows package contains link %s", h.Name)
		}
	}
}

func TestWindowsTreeRejectsUnsafeLinks(t *testing.T) {
	for name, links := range map[string]map[string]string{
		"escape":    {"a": "../outside"},
		"cycle":     {"a": "b", "b": "a"},
		"missing":   {"a": "absent"},
		"directory": {"a": "dir"},
		"device":    {"a": "NUL"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			requireOK(t, os.Mkdir(filepath.Join(dir, "dir"), 0o755))
			for p, target := range links {
				layoutFile(t, dir, p, target, 0o644)
			}
			if err := windowsTree(context.Background(), dir, links); err == nil {
				t.Fatal("unsafe link graph accepted")
			}
			for p, target := range links {
				data, err := os.ReadFile(filepath.Join(dir, p))
				requireOK(t, err)
				if string(data) != target {
					t.Fatal("layout changed placeholders before validating the graph")
				}
			}
		})
	}
}

func TestWindowsTreeRejectsCaseCollision(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("case-sensitive source filesystem required")
	}
	dir := t.TempDir()
	layoutFile(t, dir, "README", "first", 0o644)
	layoutFile(t, dir, "readme", "second", 0o644)
	if err := windowsTree(context.Background(), dir, nil); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("case collision = %v", err)
	}
}

func TestWindowsArchiveLayout(t *testing.T) {
	archive := tarGz(t,
		entry{name: "top/", typ: tar.TypeDir, mode: 0o755},
		entry{name: "top/a", typ: tar.TypeReg, mode: 0o755, body: "payload"},
		entry{name: "top/link", typ: tar.TypeSymlink, link: "a"},
	)
	dest := filepath.Join(t.TempDir(), "tree")
	links, err := extractLayout(context.Background(), writeTemp(t, archive), "tar.gz", 1, dest, true)
	requireOK(t, err)
	info, err := os.Lstat(filepath.Join(dest, "link"))
	requireOK(t, err)
	if !info.Mode().IsRegular() {
		t.Fatal("Windows extraction required a native symlink")
	}
	requireOK(t, windowsTree(context.Background(), dest, links))
	data, err := os.ReadFile(filepath.Join(dest, "link"))
	requireOK(t, err)
	if string(data) != "payload" {
		t.Fatal("archive symlink did not become a file copy")
	}
}

func TestWindowsGitLayoutIncludesSubmoduleLinks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	sub := filepath.Join(base, "sub")
	gitCmd(t, base, "init", "-q", sub)
	layoutFile(t, sub, "run.sh", "payload", 0o755)
	layoutFile(t, sub, "shortcut", "run.sh", 0o644)
	gitCmd(t, sub, "add", "run.sh")
	gitCmd(t, sub, "update-index", "--chmod=+x", "run.sh")
	blob := gitCmd(t, sub, "hash-object", "-w", "shortcut")
	gitCmd(t, sub, "update-index", "--add", "--cacheinfo", "120000,"+blob+",shortcut")
	gitCmd(t, sub, "commit", "-q", "-m", "sub")
	commit := gitCmd(t, sub, "rev-parse", "HEAD")
	super := filepath.Join(base, "super")
	gitCmd(t, base, "init", "-q", super)
	layoutFile(t, super, ".gitmodules", "[submodule \"lib/sub\"]\n path = lib/sub\n url = "+fileURL(sub)+"\n", 0o644)
	layoutFile(t, super, "alias", "lib/sub/run.sh", 0o644)
	blob = gitCmd(t, super, "hash-object", "-w", "alias")
	gitCmd(t, super, "add", ".gitmodules")
	gitCmd(t, super, "update-index", "--add", "--cacheinfo", "120000,"+blob+",alias")
	gitCmd(t, super, "update-index", "--add", "--cacheinfo", "160000,"+commit+",lib/sub")
	gitCmd(t, super, "commit", "-q", "-m", "super")
	gitCmd(t, super, "tag", "v1")
	src := profile.Source{Git: fileURL(super), Ref: "v1", Commit: gitCmd(t, super, "rev-parse", "HEAD"), Layout: profile.LayoutWindows}
	f := newFetcher(t)
	tree := filepath.Join(t.TempDir(), "tree")
	requireOK(t, f.clone("sdk", src, tree))
	links, err := f.gitSymlinks(tree)
	requireOK(t, err)
	if len(links) != 2 {
		t.Fatalf("submodule symlinks lost: %v", links)
	}
	requireOK(t, clean(tree))
	requireOK(t, windowsTree(context.Background(), tree, links))
	want, _, err := pkgtar.Write(io.Discard, tree)
	requireOK(t, err)
	digest, err := f.Install("sdk", src)
	requireOK(t, err)
	if digest != want {
		t.Fatal("installed Windows Git package differs from normalized preparation")
	}
	dir, err := f.Store.Path(digest)
	requireOK(t, err)
	for _, p := range []string{"alias", "lib/sub/shortcut", "lib/sub/run.sh"} {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		requireOK(t, err)
		if string(data) != "payload" {
			t.Fatalf("%s lost symlink target bytes", p)
		}
	}
	// The same repository can be prepared as a Unix tar even with only
	// placeholder links on disk. Native Unix preparation pins the reference.
	src.Layout = ""
	if runtime.GOOS != "windows" {
		src.Digest, err = newFetcher(t).Install("sdk", src)
		requireOK(t, err)
	}
	portable := newFetcher(t)
	digest, err = portable.InstallPortable("sdk", src)
	requireOK(t, err)
	if !portable.Store.Has(digest) || portable.Store.HasTree(digest) {
		t.Fatal("portable Git package must be archived without extraction")
	}
}

func TestSubmodulePathCannotEscapePreparation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	repo, outside := filepath.Join(base, "repo"), filepath.Join(base, "outside")
	gitCmd(t, base, "init", "-q", repo)
	layoutFile(t, outside, "keep", "untouched", 0o644)
	layoutFile(t, repo, ".gitmodules", "[submodule \"bad\"]\n path = ../outside\n url = file:///unused\n", 0o644)
	f := newFetcher(t)
	if err := f.submodules("git", repo, false); err == nil {
		t.Fatal("submodule path escaped the preparation tree")
	}
	data, err := os.ReadFile(filepath.Join(outside, "keep"))
	requireOK(t, err)
	if string(data) != "untouched" {
		t.Fatal("submodule preparation modified external files")
	}
}

func TestSubmoduleParentLinkCannotEscapePreparation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory symlink fixture")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	repo, outside := filepath.Join(base, "repo"), filepath.Join(base, "outside")
	gitCmd(t, base, "init", "-q", repo)
	layoutFile(t, outside, "sub/keep", "untouched", 0o644)
	layoutFile(t, repo, ".gitmodules", "[submodule \"bad\"]\n path = lib/sub\n url = file:///unused\n", 0o644)
	gitCmd(t, repo, "add", ".gitmodules")
	gitCmd(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("1", 40)+",lib/sub")
	gitCmd(t, repo, "commit", "-q", "-m", "bad parent")
	requireOK(t, os.Symlink(outside, filepath.Join(repo, "lib")))
	if err := newFetcher(t).submodules("git", repo, false); err == nil {
		t.Fatal("submodule parent link escaped the preparation tree")
	}
	data, err := os.ReadFile(filepath.Join(outside, "sub", "keep"))
	requireOK(t, err)
	if string(data) != "untouched" {
		t.Fatal("submodule preparation deleted external files")
	}
}
