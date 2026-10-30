package snapshot

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/pkgtar"
)

func TestForeignArchiveSurvivesUnpackAndRepack(t *testing.T) {
	// No Unix filesystem operations are used to prepare the Linux package.
	// This test can run on Windows without symlink privileges.
	tree := pkgtar.NewTree(t.TempDir())
	must(t, tree.File(t.Context(), "bin/tool", 0755, strings.NewReader("ELF fixture")))
	must(t, tree.Link("bin/tool-alias", "bin/tool"))
	must(t, tree.Symlink("bin/shortcut", "tool"))
	// These names cannot safely share an ordinary Windows directory tree.
	// Retaining foreign packages must not materialize their paths there.
	for _, name := range []string{"share/Case", "share/case", "share/name:unix", "share/CON"} {
		must(t, tree.File(t.Context(), name, 0644, strings.NewReader(name)))
	}
	var canonical bytes.Buffer
	digest, _, err := tree.Write(t.Context(), &canonical)
	must(t, err)
	linuxTar := filepath.Join(t.TempDir(), "linux.tar")
	must(t, os.WriteFile(linuxTar, canonical.Bytes(), 0644))
	win := t.TempDir()
	must(t, os.WriteFile(filepath.Join(win, "tool.exe"), []byte("PE fixture"), 0644))
	project, files := makeProject(t)
	opt := Options{CreatedBy: "portable-test", Profile: manifest.Profile{ID: "rp2040", Version: "1"}, Platforms: []string{"linux-x64", "windows-x64"}, ProjectDir: project, ProjectFiles: files,
		Packages: []PackageSource{{Name: "tools", Platform: "linux-x64", Archive: linuxTar, ExpectedDigest: digest}, {Name: "tools", Platform: "windows-x64", Dir: win}}}
	snap := filepath.Join(t.TempDir(), "both.slepok")
	_, err = Pack(snap, opt)
	must(t, err)
	st := openStore(t)
	restored := filepath.Join(t.TempDir(), "restored")
	_, err = Unpack(snap, restored, st, "windows-x64")
	must(t, err)
	if !st.Has(digest) || st.HasTree(digest) {
		t.Fatal("Linux package extracted on Windows receiver")
	}
	archive, err := st.ArchivePath(digest)
	must(t, err)
	got, err := os.ReadFile(archive)
	must(t, err)
	if !bytes.Equal(got, canonical.Bytes()) {
		t.Fatal("foreign tar changed")
	}
	opt.ProjectDir = restored
	opt.Packages[0].Archive = archive
	m, err := Validate(snap)
	must(t, err)
	opt.Packages[1].Dir, err = st.Path(m.Packages[1].Digest)
	must(t, err)
	repacked := filepath.Join(t.TempDir(), "repacked.slepok")
	_, err = Pack(repacked, opt)
	must(t, err)
	_, err = Validate(repacked)
	must(t, err)
	// Materialize the deliberately case-sensitive Linux names only on Linux.
	// Windows/macOS receivers exercise retaining and repacking the opaque tar.
	if runtime.GOOS == "linux" {
		linux := openStore(t)
		_, err = Unpack(repacked, filepath.Join(t.TempDir(), "linux-project"), linux, "linux-x64")
		must(t, err)
		dir, err := linux.Path(digest)
		must(t, err)
		info, err := os.Stat(filepath.Join(dir, "bin", "tool"))
		must(t, err)
		if info.Mode().Perm()&0111 == 0 {
			t.Fatal("Linux executable bit lost")
		}
		link, err := os.Readlink(filepath.Join(dir, "bin", "shortcut"))
		must(t, err)
		if link != "tool" {
			t.Fatal("Linux symlink changed")
		}
		info2, err := os.Stat(filepath.Join(dir, "bin", "tool-alias"))
		must(t, err)
		if !os.SameFile(info, info2) {
			t.Fatal("Linux hardlink changed")
		}
		reexported, _, err := pkgtar.Write(io.Discard, dir)
		must(t, err)
		if reexported != digest {
			t.Fatal("Linux package digest changed")
		}
	}
	// Tampering must not silently replace the lock's pinned digest.
	opt.Packages[0].ExpectedDigest = "sha256:" + strings.Repeat("0", 64)
	_, err = Pack(filepath.Join(t.TempDir(), "bad.slepok"), opt)
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatal("accepted unpinned archive", err)
	}
}
