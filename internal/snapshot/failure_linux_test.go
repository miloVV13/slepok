package snapshot

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/miloVV13/slepok/internal/fetch"
	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/pkgtar"
	"github.com/miloVV13/slepok/internal/project"
	"github.com/miloVV13/slepok/internal/store"
)

// Run in a disposable user/mount namespace so no host mount is modified.
func TestDiskFullFilesystem(t *testing.T) {
	if os.Getenv("SLEPOK_DISK_FULL_HELPER") == "1" {
		target := os.Getenv("SLEPOK_DISK_FULL_DIR")
		if err := syscall.Mount("tmpfs", target, "tmpfs", 0, "size=1m"); err != nil {
			t.Fatal(err)
		}
		defer syscall.Unmount(target, 0)
		testDiskFullOperations(t, target)
		return
	}
	probe := exec.Command("unshare", "--user", "--map-root-user", "--mount", "true")
	if out, err := probe.CombinedOutput(); err != nil {
		if os.Getenv("SLEPOK_REQUIRE_FILESYSTEM_TESTS") == "1" {
			t.Fatalf("mount namespace required: %v: %s", err, out)
		}
		t.Skipf("mount namespace unavailable: %v: %s", err, out)
	}
	dir := t.TempDir()
	cmd := exec.Command("unshare", "--user", "--map-root-user", "--mount", os.Args[0], "-test.run=^TestDiskFullFilesystem$", "-test.v")
	cmd.Env = append(os.Environ(), "SLEPOK_DISK_FULL_HELPER=1", "SLEPOK_DISK_FULL_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("disk-full test: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
}

func testDiskFullOperations(t *testing.T, full string) {
	src := t.TempDir()
	data := make([]byte, 2<<20)
	_, err := rand.Read(data)
	must(t, err)
	must(t, os.WriteFile(filepath.Join(src, "main.c"), data, 0o644))
	opt := Options{ProjectDir: src, ProjectFiles: []string{"main.c"}, Platforms: []string{"linux-x64"}}

	_, err = Pack(filepath.Join(full, "failed.slepok"), opt)
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("Pack = %v, want ENOSPC", err)
	}
	if got := listDir(t, full); len(got) != 0 {
		t.Fatalf("Pack left files: %v", got)
	}

	archive := filepath.Join(t.TempDir(), "valid.slepok")
	_, err = Pack(archive, opt)
	must(t, err)
	_, err = Unpack(archive, filepath.Join(full, "restored"), openStore(t), "linux-x64")
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("Unpack = %v, want ENOSPC", err)
	}
	if got := listDir(t, full); len(got) != 0 {
		t.Fatalf("Unpack left files: %v", got)
	}

	st, err := store.Open(filepath.Join(full, "store"))
	must(t, err)
	var pkg bytes.Buffer
	digest, _, err := pkgtar.Write(&pkg, src)
	must(t, err)
	err = st.Install(digest, bytes.NewReader(pkg.Bytes()))
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("Install = %v, want ENOSPC", err)
	}
	if got := listDir(t, filepath.Join(st.Root(), "packages")); len(got) != 0 {
		t.Fatalf("Install left files: %v", got)
	}

	// Updating a lock must preserve the last usable version on write failure.
	old := []byte("original lock\n")
	must(t, os.WriteFile(filepath.Join(full, project.LockName), old, 0o644))
	err = project.WriteLock(full, project.Lock{Profile: strings.Repeat("x", 2<<20)})
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("WriteLock = %v, want ENOSPC", err)
	}
	got, err := os.ReadFile(filepath.Join(full, project.LockName))
	must(t, err)
	if !bytes.Equal(got, old) {
		t.Fatal("disk-full overwrote the old lock")
	}
	if got := listDir(t, full); len(got) != 2 {
		t.Fatalf("WriteLock left temporary files: %v", got)
	}

	// A local write failure is not a network outage and must not be retried.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	f := fetch.New(st, nil)
	f.Context = ctx
	f.RetryDelay = time.Hour
	_, err = f.Download("test", srv.URL, strings.Repeat("a", 64))
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("Download = %v, want ENOSPC without retry", err)
	}
	if _, err := os.Stat(filepath.Join(st.Root(), "downloads", strings.Repeat("a", 64))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Download published an incomplete cache entry: %v", err)
	}
}

type cancellingWriter struct{ cancel context.CancelFunc }

func (w cancellingWriter) Write([]byte) (int, error) { w.cancel(); return 0, context.Canceled }

func TestCancellationDuringArchiveWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opt := options(t)
	opt.Context = ctx
	m, err := plan(opt)
	must(t, err)
	data, err := m.Marshal()
	must(t, err)
	if err := write(cancellingWriter{cancel}, m, data, opt); !errors.Is(err, context.Canceled) {
		t.Fatalf("write = %v", err)
	}
}

type cancelOnFile struct{ cancel context.CancelFunc }

func (h cancelOnFile) file(manifest.File, io.Reader) error   { h.cancel(); return nil }
func (h cancelOnFile) pkg(manifest.Package, io.Reader) error { return nil }

func TestCancellationDuringArchiveRead(t *testing.T) {
	p := filepath.Join(t.TempDir(), "snapshot.slepok")
	_, err := Pack(p, options(t))
	must(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = read(ctx, p, nil, cancelOnFile{cancel})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("read = %v", err)
	}
}
