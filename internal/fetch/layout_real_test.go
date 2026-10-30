package fetch

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/miloVV13/slepok/internal/pkgtar"
	"github.com/miloVV13/slepok/internal/profile"
)

// SLEPOK_WINDOWS_SDK_SOURCE=.../pico-sdk go test -v ./internal/fetch -run TestRealWindowsSDK
// Uses a private copy; never changes the supplied Linux SDK tree.
func TestRealWindowsSDK(t *testing.T) {
	source := os.Getenv("SLEPOK_WINDOWS_SDK_SOURCE")
	if source == "" {
		t.Skip("SLEPOK_WINDOWS_SDK_SOURCE not set")
	}
	if runtime.GOOS == "windows" {
		t.Skip("requires the original Unix SDK representation")
	}
	dir := t.TempDir()
	err := filepath.WalkDir(source, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == source {
			return nil
		}
		rel, err := filepath.Rel(source, p)
		if err != nil {
			return err
		}
		if removedByClean(filepath.ToSlash(rel)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		to := filepath.Join(dir, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			return os.Mkdir(to, 0o755)
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(target, to)
		case info.Mode().IsRegular():
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
			if err != nil {
				return err
			}
			_, err = io.Copy(out, in)
			closeErr := out.Close()
			if err != nil {
				return err
			}
			return closeErr
		default:
			t.Fatalf("unexpected SDK node %s", rel)
			return nil
		}
	})
	requireOK(t, err)
	before, _, err := pkgtar.Write(io.Discard, dir)
	requireOK(t, err)
	prof, err := profile.Load("rp2040")
	requireOK(t, err)
	var sdk profile.Component
	for _, c := range prof.Components {
		if c.Name == "pico-sdk" {
			sdk = c
		}
	}
	if before != sdk.Sources["any"].Digest {
		t.Fatalf("Linux SDK was modified: got %s, want %s", before, sdk.Sources["any"].Digest)
	}
	requireOK(t, windowsTree(context.Background(), dir, nil))
	archive, err := os.Create(filepath.Join(t.TempDir(), "sdk.tar"))
	requireOK(t, err)
	digest, size, err := pkgtar.Write(archive, dir)
	requireOK(t, err)
	requireOK(t, archive.Close())
	t.Logf("Windows SDK: digest=%s, canonical tar=%d bytes; Linux digest unchanged=%s", digest, size, before)
	if src, ok := sdk.Sources["windows-x64"]; ok && src.Digest != digest {
		t.Fatalf("Windows SDK digest differs from profile: %s != %s", digest, src.Digest)
	}
	input, err := os.Open(archive.Name())
	requireOK(t, err)
	defer input.Close()
	restored := filepath.Join(t.TempDir(), "restored")
	requireOK(t, pkgtar.Extract(input, restored))
	// Simulate the permission information exposed by the Windows filesystem.
	requireOK(t, filepath.WalkDir(restored, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			t.Fatalf("Windows SDK still contains a link: %s", p)
		}
		if d.IsDir() {
			return os.Chmod(p, 0o777)
		}
		return os.Chmod(p, 0o666)
	}))
	again, _, err := pkgtar.Write(io.Discard, restored)
	requireOK(t, err)
	if again != digest {
		t.Fatalf("round trip lost Windows SDK identity: %s != %s", again, digest)
	}
}
