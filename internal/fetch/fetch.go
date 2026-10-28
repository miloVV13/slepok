// Package fetch downloads profile components and installs them into the
// store as packages (spec 7.4). It is the only part of slepok that needs
// the network.
package fetch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/store"
)

type Fetcher struct {
	Store      *store.Store
	Log        io.Writer
	Client     *http.Client
	Retries    int
	RetryDelay time.Duration
}

func New(st *store.Store, log io.Writer) *Fetcher {
	return &Fetcher{
		Store: st,
		Log:   log,
		Client: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSHandshakeTimeout:   30 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
		}},
		Retries:    8,
		RetryDelay: 3 * time.Second,
	}
}

// Install makes the component available in the store and returns its
// package digest. If src.Digest is set, the package must match it.
func (f *Fetcher) Install(name string, src profile.Source) (string, error) {
	if src.Digest != "" && f.Store.Has(src.Digest) {
		return src.Digest, nil
	}
	work, err := f.Store.TempDir("fetch-")
	if err != nil {
		return "", err
	}
	defer store.RemoveTree(work)

	tree := filepath.Join(work, "tree")
	if src.Git != "" {
		err = f.clone(name, src, tree)
	} else {
		var archive string
		if archive, err = f.download(name, src); err == nil {
			f.logf("  extracting %s\n", name)
			err = extract(archive, src.Format, src.Strip, tree)
		}
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	if err := clean(tree); err != nil {
		return "", err
	}
	digest, err := f.Store.InstallDir(tree, src.Digest)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return digest, nil
}

// clean removes version-control metadata and Python caches, which differ
// between machines and are not needed to build. In git submodules .git is
// a file pointing into the parent repository.
func clean(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch d.Name() {
		case ".git", "__pycache__":
			if err := os.RemoveAll(p); err != nil {
				return err
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
}

func (f *Fetcher) logf(format string, args ...any) {
	if f.Log != nil {
		fmt.Fprintf(f.Log, format, args...)
	}
}

func (f *Fetcher) retry(what string, op func() error) error {
	var err error
	for attempt := 1; attempt <= f.Retries; attempt++ {
		if err = op(); err == nil {
			return nil
		}
		if attempt < f.Retries {
			f.logf("  %s: %s; retrying (%d/%d)\n", what, reason(err), attempt, f.Retries-1)
			time.Sleep(f.RetryDelay)
		}
	}
	return err
}

// reason shortens an error for progress output: git and the network wrap
// the useful part in long command lines.
func reason(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, "fatal: "); i >= 0 {
		msg = msg[i+len("fatal: "):]
	}
	if i := strings.LastIndex(msg, ": "); i >= 0 && len(msg)-i < 80 {
		msg = msg[i+2:]
	}
	return msg
}

// Download fetches a file pinned by its sha256 into the download cache and
// returns its path. It is used for archives that are bundled as they are,
// such as the source code of GPL components.
func (f *Fetcher) Download(name, url, sha256 string) (string, error) {
	return f.download(name, profile.Source{URL: url, SHA256: sha256})
}

// download returns the path of the verified archive in the download cache.
func (f *Fetcher) download(name string, src profile.Source) (string, error) {
	dir := filepath.Join(f.Store.Root(), "downloads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	final := filepath.Join(dir, src.SHA256)
	if sum, err := fileSHA256(final); err == nil && sum == src.SHA256 {
		return final, nil
	}

	part := final + ".part"
	f.logf("  downloading %s\n", name)
	if err := f.retry("download "+name, func() error { return f.downloadOnce(src.URL, part) }); err != nil {
		return "", err
	}
	sum, err := fileSHA256(part)
	if err != nil {
		return "", err
	}
	if sum != src.SHA256 {
		os.Remove(part)
		return "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", src.URL, sum, src.SHA256)
	}
	return final, os.Rename(part, final)
}

// downloadOnce continues an interrupted download in part when the server
// supports range requests.
func (f *Fetcher) downloadOnce(url, part string) error {
	out, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	offset, err := out.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// The server sent the whole file.
		if err := out.Truncate(0); err != nil {
			return err
		}
		if _, err := out.Seek(0, io.SeekStart); err != nil {
			return err
		}
	case http.StatusPartialContent:
	case http.StatusRequestedRangeNotSatisfiable:
		return nil // already complete; the checksum decides
	default:
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		return err
	}
	return out.Close()
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// clone checks out src.Ref with all submodules and verifies that it is the
// pinned commit. It uses the system git, which only init needs.
func (f *Fetcher) clone(name string, src profile.Source, dest string) error {
	git, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git is required to download %s; install git and retry", name)
	}
	f.logf("  cloning %s %s\n", name, src.Ref)
	err = f.retry("clone "+name, func() error {
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
		_, err := run(git, "-c", "advice.detachedHead=false", "-c", "core.autocrlf=false", "-c", "core.eol=lf", "clone", "--quiet", "--depth", "1", "--branch", src.Ref, src.Git, dest)
		return err
	})
	if err != nil {
		return err
	}
	head, err := run(git, "-C", dest, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != src.Commit {
		return fmt.Errorf("%s %s is commit %s, expected %s; the upstream tag may have moved", src.Git, src.Ref, head, src.Commit)
	}
	return f.submodules(git, dest)
}

// submodules checks out every submodule of the repository at dir at the
// commit the superproject records, recursively. Unlike
// "git submodule update --depth 1", which first clones the tip of the
// default branch and then fetches the wanted commit, this fetches exactly
// that commit in one request and retries each submodule on its own, so a
// flaky network does not restart the whole checkout.
func (f *Fetcher) submodules(git, dir string) error {
	if _, err := os.Stat(filepath.Join(dir, ".gitmodules")); err != nil {
		return nil
	}
	paths, err := run(git, "-C", dir, "config", "--file", ".gitmodules", "--get-regexp", `^submodule\..*\.path$`)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(paths, "\n") {
		key, sub, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "submodule."), ".path")
		url, err := run(git, "-C", dir, "config", "--file", ".gitmodules", "submodule."+name+".url")
		if err != nil {
			return err
		}
		tree, err := run(git, "-C", dir, "ls-tree", "HEAD", sub)
		if err != nil {
			return err
		}
		fields := strings.Fields(tree)
		if len(fields) < 3 || fields[1] != "commit" {
			return fmt.Errorf("submodule %s: no commit recorded in the superproject", sub)
		}
		commit := fields[2]
		subdir := filepath.Join(dir, filepath.FromSlash(sub))

		f.logf("  fetching submodule %s\n", sub)
		err = f.retry("submodule "+sub, func() error {
			if err := os.RemoveAll(subdir); err != nil {
				return err
			}
			if err := os.MkdirAll(subdir, 0o755); err != nil {
				return err
			}
			for _, args := range [][]string{
				{"-C", subdir, "init", "--quiet"},
				{"-C", subdir, "fetch", "--quiet", "--depth", "1", url, commit},
				{"-C", subdir, "-c", "advice.detachedHead=false", "-c", "core.autocrlf=false", "-c", "core.eol=lf", "checkout", "--quiet", "FETCH_HEAD"},
			} {
				if _, err := run(git, args...); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if head, err := run(git, "-C", subdir, "rev-parse", "HEAD"); err != nil || head != commit {
			return fmt.Errorf("submodule %s is at %s, expected %s (%v)", sub, head, commit, err)
		}
		if err := f.submodules(git, subdir); err != nil {
			return err
		}
	}
	return nil
}

func run(name string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		cmdline := filepath.Base(name) + " " + strings.Join(args, " ")
		var exit *exec.ExitError
		if errors.As(err, &exit) && msg != "" {
			return "", fmt.Errorf("%s: %s", cmdline, msg)
		}
		return "", fmt.Errorf("%s: %w", cmdline, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}
