package fetch

import (
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/miloVV13/slepok/internal/pkgtar"
	"github.com/miloVV13/slepok/internal/profile"
)

func TestRealPortableSDK(t *testing.T) {
	source := os.Getenv("SLEPOK_PORTABLE_SDK_SOURCE")
	if source == "" {
		t.Skip("SLEPOK_PORTABLE_SDK_SOURCE not set")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix reference fixture")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	p, err := profile.Load("rp2040")
	requireOK(t, err)
	var want string
	for _, c := range p.Components {
		if c.Name == "pico-sdk" {
			want = c.Sources["any"].Digest
		}
	}
	file, err := os.CreateTemp(t.TempDir(), "reference-")
	requireOK(t, err)
	defer file.Close()
	digest, _, err := pkgtar.Write(file, source)
	requireOK(t, err)
	if digest != want {
		t.Fatalf("reference SDK differs from profile: %s", digest)
	}
	_, err = file.Seek(0, 0)
	requireOK(t, err)
	dir := filepath.Join(t.TempDir(), "checkout")
	requireOK(t, pkgtar.Extract(file, dir))
	gitCmd(t, dir, "init", "-q")
	gitCmd(t, dir, "add", "-f", ".")
	// Emulate the only metadata exposed by a normal Windows checkout.
	requireOK(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if err := os.Remove(p); err != nil {
				return err
			}
			return os.WriteFile(p, []byte(target), 0666)
		}
		if d.Type().IsRegular() {
			return os.Chmod(p, 0666)
		}
		return nil
	}))
	tree := pkgtar.NewTree(t.TempDir())
	f := newFetcher(t)
	requireOK(t, f.portableGit(tree, dir))
	got, n, err := tree.Write(t.Context(), io.Discard)
	requireOK(t, err)
	if got != want {
		t.Fatalf("portable SDK = %s, want %s", got, want)
	}
	t.Logf("SDK with placeholder links and no Unix execute bits: %s, %d bytes", got, n)
}
