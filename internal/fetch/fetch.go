// Package fetch downloads profile components and installs them into the
// store as packages (spec 7.4). It is the only part of slepok that needs
// the network.
package fetch

import (
	"bytes"
	"context"
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
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/miloVV13/slepok/internal/contextio"
	"github.com/miloVV13/slepok/internal/process"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/safepath"
	"github.com/miloVV13/slepok/internal/store"
)

type Fetcher struct {
	Context context.Context
	Store   *store.Store
	Log     io.Writer
	Client  *http.Client
	// Retries is how many attempts in a row may fail without progress.
	Retries    int
	RetryDelay time.Duration
	// IdleTimeout aborts a download attempt that receives no data.
	IdleTimeout   time.Duration
	ProgressEvery time.Duration
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
		Retries:       8,
		RetryDelay:    3 * time.Second,
		IdleTimeout:   60 * time.Second,
		ProgressEvery: 10 * time.Second,
	}
}

// Install makes the component available in the store and returns its
// package digest. If src.Digest is set, the package must match it.
func (f *Fetcher) Install(name string, src profile.Source) (string, error) {
	if err := f.ctx().Err(); err != nil {
		return "", err
	}
	if src.Layout != "" && src.Layout != profile.LayoutWindows {
		return "", fmt.Errorf("unknown package layout %q", src.Layout)
	}
	if src.Digest != "" && f.Store.Has(src.Digest) {
		return src.Digest, nil
	}
	work, err := f.Store.TempDir("fetch-")
	if err != nil {
		return "", err
	}
	defer store.RemoveTree(work)

	tree := filepath.Join(work, "tree")
	var links map[string]string
	if src.Git != "" {
		err = f.clone(name, src, tree)
		if err == nil && src.Layout == profile.LayoutWindows {
			links, err = f.gitSymlinks(tree)
		}
	} else {
		var archive string
		if archive, err = f.download(name, src); err == nil {
			f.logf("  extracting %s\n", name)
			links, err = extractLayout(f.ctx(), archive, src.Format, src.Strip, tree, src.Layout == profile.LayoutWindows)
		}
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	if err := clean(tree); err != nil {
		return "", err
	}
	if src.Layout == profile.LayoutWindows {
		if err := windowsTree(f.ctx(), tree, links); err != nil {
			return "", fmt.Errorf("%s: Windows layout: %w", name, err)
		}
	}
	digest, err := f.Store.InstallDirContext(f.ctx(), tree, src.Digest)
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
		if err := f.ctx().Err(); err != nil {
			return err
		}
		if err = op(); err == nil {
			return nil
		}
		if attempt < f.Retries {
			f.logf("  %s: %s; retrying (%d/%d)\n", what, reason(err), attempt, f.Retries-1)
			if err := f.pause(); err != nil {
				return err
			}
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
func (f *Fetcher) Download(name, url, sha256 string, mirrors ...string) (string, error) {
	return f.download(name, profile.Source{URL: url, Mirrors: mirrors, SHA256: sha256})
}

// download returns the path of the verified archive in the download cache.
// It tries the URL and its mirrors in turn and keeps resuming as long as
// attempts make progress, so a slow, flaky network still gets there.
func (f *Fetcher) download(name string, src profile.Source) (string, error) {
	guard, err := f.Store.LockDownload(f.ctx(), src.SHA256)
	if err != nil {
		return "", err
	}
	defer guard.Close()
	dir := filepath.Join(f.Store.Root(), "downloads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	final := filepath.Join(dir, src.SHA256)
	if sum, err := fileSHA256Context(f.ctx(), final); err == nil && sum == src.SHA256 {
		return final, nil
	}
	urls := append([]string{src.URL}, src.Mirrors...)
	part := final + ".part"
	f.logf("  downloading %s\n", name)

	dead := map[string]bool{}
	var lastErr error
	for attempt, stuck := 0, 0; stuck < f.Retries && len(dead) < len(urls); attempt++ {
		url := urls[attempt%len(urls)]
		if dead[url] {
			continue
		}
		if err := f.ctx().Err(); err != nil {
			return "", err
		}
		before := fileSize(part)
		lastErr = f.downloadOnce(name, url, part)
		if lastErr == nil {
			break
		}
		if err := f.ctx().Err(); err != nil {
			return "", err
		}
		var pathErr *os.PathError
		if errors.As(lastErr, &pathErr) && pathErr.Op != "dial" {
			return "", lastErr
		}
		var status *statusError
		if errors.As(lastErr, &status) && status.permanent() {
			dead[url] = true
		}
		if fileSize(part) > before {
			stuck = 0
		} else {
			stuck++
		}
		f.logf("  download %s: %s; retrying\n", name, reason(lastErr))
		if err := f.pause(); err != nil {
			return "", err
		}
	}
	if lastErr != nil {
		return "", fmt.Errorf("%w\n%s", lastErr, manualDownload(urls, src.SHA256, final))
	}
	sum, err := fileSHA256Context(f.ctx(), part)
	if err != nil {
		return "", err
	}
	if sum != src.SHA256 {
		os.Remove(part)
		return "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", src.URL, sum, src.SHA256)
	}
	if err := f.ctx().Err(); err != nil {
		return "", err
	}
	return final, os.Rename(part, final)
}

// manualDownload explains how to provide a file slepok cannot download:
// the cache is addressed by checksum, so any copy with the right sha256
// will do and a wrong one is rejected.
func manualDownload(urls []string, sum, final string) string {
	return fmt.Sprintf("To get it another way (a browser, a VPN, another computer), download\n  %s\n"+
		"check that its sha256 is %s and save it as\n  %s\nthen run the command again.",
		strings.Join(urls, "\n  or "), sum, final)
}

func fileSize(p string) int64 {
	info, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return info.Size()
}

// statusError is an unexpected HTTP status.
type statusError struct {
	url  string
	code int
	text string
}

func (e *statusError) Error() string { return fmt.Sprintf("GET %s: %s", e.url, e.text) }

// permanent reports whether retrying the same URL is pointless.
func (e *statusError) permanent() bool {
	switch e.code {
	case http.StatusForbidden, http.StatusNotFound, http.StatusGone, http.StatusUnavailableForLegalReasons:
		return true
	}
	return false
}

// downloadOnce continues an interrupted download in part when the server
// supports range requests. It gives up when no data arrives for
// f.IdleTimeout, so a stalled connection is retried instead of hanging.
func (f *Fetcher) downloadOnce(name, url, part string) error {
	out, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	offset, err := out.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(f.ctx())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		offset = 0
	case http.StatusPartialContent:
	case http.StatusRequestedRangeNotSatisfiable:
		return nil // already complete; the checksum decides
	default:
		return &statusError{url: url, code: resp.StatusCode, text: resp.Status}
	}

	body := newIdleReader(resp.Body, f.IdleTimeout, cancel)
	defer body.stop()
	total := int64(-1)
	if resp.ContentLength >= 0 {
		total = offset + resp.ContentLength
	}
	progress := &progressWriter{f: f, name: name, done: offset, total: total, last: time.Now()}
	if _, err := io.Copy(io.MultiWriter(out, progress), body); err != nil {
		if body.stalled.Load() {
			return fmt.Errorf("no data received for %s", f.IdleTimeout)
		}
		return err
	}
	return out.Close()
}

// idleReader cancels the request when no data arrives for a while.
type idleReader struct {
	r       io.Reader
	d       time.Duration
	timer   *time.Timer
	stalled atomic.Bool
}

func newIdleReader(r io.Reader, d time.Duration, cancel func()) *idleReader {
	ir := &idleReader{r: r, d: d}
	ir.timer = time.AfterFunc(d, func() {
		ir.stalled.Store(true)
		cancel()
	})
	return ir
}

func (ir *idleReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if n > 0 {
		ir.timer.Reset(ir.d)
	}
	return n, err
}

func (ir *idleReader) stop() { ir.timer.Stop() }

// progressWriter reports download progress every few seconds.
type progressWriter struct {
	f           *Fetcher
	name        string
	done, total int64
	last        time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if time.Since(p.last) >= p.f.ProgressEvery {
		p.last = time.Now()
		if p.total > 0 {
			p.f.logf("  %s: %.1f of %.1f MiB (%d%%)\n", p.name, mib(p.done), mib(p.total), p.done*100/p.total)
		} else {
			p.f.logf("  %s: %.1f MiB\n", p.name, mib(p.done))
		}
	}
	return len(b), nil
}

func mib(n int64) float64 { return float64(n) / (1 << 20) }

func fileSHA256(p string) (string, error) { return fileSHA256Context(context.Background(), p) }

func fileSHA256Context(ctx context.Context, p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, contextio.Reader{Context: ctx, Reader: f}); err != nil {
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
		args := []string{"-c", "advice.detachedHead=false", "-c", "core.autocrlf=false", "-c", "core.eol=lf"}
		args = append(args, "-c", "core.symlinks="+strconv.FormatBool(src.Layout != profile.LayoutWindows))
		args = append(args, "clone", "--quiet", "--depth", "1", "--branch", src.Ref, src.Git, dest)
		_, err := f.runGit(git, args...)
		return err
	})
	if err != nil {
		return err
	}
	head, err := f.runGit(git, "-C", dest, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != src.Commit {
		return fmt.Errorf("%s %s is commit %s, expected %s; the upstream tag may have moved", src.Git, src.Ref, head, src.Commit)
	}
	return f.submodules(git, dest, src.Layout == profile.LayoutWindows)
}

// submodules checks out every submodule of the repository at dir at the
// commit the superproject records, recursively. Unlike
// "git submodule update --depth 1", which first clones the tip of the
// default branch and then fetches the wanted commit, this fetches exactly
// that commit in one request and retries each submodule on its own, so a
// flaky network does not restart the whole checkout.
func (f *Fetcher) submodules(git, dir string, windowsLayout bool) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := os.Stat(filepath.Join(dir, ".gitmodules")); err != nil {
		return nil
	}
	paths, err := f.runGit(git, "-C", dir, "config", "--file", ".gitmodules", "--get-regexp", `^submodule\..*\.path$`)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(paths, "\n") {
		key, sub, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if err := safepath.CheckForHost(sub); err != nil {
			return fmt.Errorf("submodule: %w", err)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "submodule."), ".path")
		url, err := f.runGit(git, "-C", dir, "config", "--file", ".gitmodules", "submodule."+name+".url")
		if err != nil {
			return err
		}
		tree, err := f.runGit(git, "-C", dir, "ls-tree", "HEAD", "--", sub)
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
			if err := root.RemoveAll(sub); err != nil {
				return err
			}
			if err := root.MkdirAll(sub, 0o755); err != nil {
				return err
			}
			checkout := []string{"-C", subdir, "-c", "advice.detachedHead=false", "-c", "core.autocrlf=false", "-c", "core.eol=lf"}
			checkout = append(checkout, "-c", "core.symlinks="+strconv.FormatBool(!windowsLayout))
			checkout = append(checkout, "checkout", "--quiet", "FETCH_HEAD")
			for _, args := range [][]string{
				{"-C", subdir, "init", "--quiet"},
				{"-C", subdir, "fetch", "--quiet", "--depth", "1", url, commit},
				checkout,
			} {
				if _, err := f.runGit(git, args...); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if head, err := f.runGit(git, "-C", subdir, "rev-parse", "HEAD"); err != nil || head != commit {
			return fmt.Errorf("submodule %s is at %s, expected %s (%v)", sub, head, commit, err)
		}
		if err := f.submodules(git, subdir, windowsLayout); err != nil {
			return err
		}
	}
	return nil
}

// runGit enables long paths per invocation on Windows, without changing
// the user's Git configuration. Store paths include a 64-character digest.
func (f *Fetcher) runGit(name string, args ...string) (string, error) {
	if runtime.GOOS == "windows" {
		args = append([]string{"-c", "core.longpaths=true"}, args...)
	}
	var stdout, stderr bytes.Buffer
	cmd := process.Command(f.ctx(), name, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if f.ctx().Err() != nil {
			return "", f.ctx().Err()
		}
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

func (f *Fetcher) ctx() context.Context { return contextio.OrBackground(f.Context) }
func (f *Fetcher) pause() error {
	timer := time.NewTimer(f.RetryDelay)
	defer timer.Stop()
	select {
	case <-f.ctx().Done():
		return f.ctx().Err()
	case <-timer.C:
		return nil
	}
}
