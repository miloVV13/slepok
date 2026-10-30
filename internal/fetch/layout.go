package fetch

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/miloVV13/slepok/internal/contextio"
	"github.com/miloVV13/slepok/internal/safepath"
)

// gitSymlinks gets link identities from the index, not the filesystem:
// core.symlinks=false checks them out as ordinary files on every host.
func (f *Fetcher) gitSymlinks(dir string) (map[string]string, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	links := map[string]string{}
	var visit func(string, string) error
	visit = func(repo, prefix string) error {
		listing, err := f.run(git, "-C", repo, "ls-files", "--stage", "-z")
		if err != nil {
			return err
		}
		for _, record := range strings.Split(listing, "\x00") {
			if record == "" {
				continue
			}
			header, name, ok := strings.Cut(record, "\t")
			fields := strings.Fields(header)
			if !ok || len(fields) != 3 || fields[2] != "0" {
				return fmt.Errorf("invalid git index record")
			}
			if err := safepath.Check(name); err != nil {
				return err
			}
			rel := path.Join(prefix, name)
			if removedByClean(rel) {
				continue
			}
			switch fields[0] {
			case "120000":
				p := filepath.Join(repo, filepath.FromSlash(name))
				info, err := os.Lstat(p)
				if err != nil {
					return err
				}
				if !info.Mode().IsRegular() {
					return fmt.Errorf("%s: git symlink was not checked out as a regular placeholder", rel)
				}
				if info.Size() > 4096 {
					return fmt.Errorf("%s: symlink target is too long", rel)
				}
				data, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				links[rel] = string(data)
			case "160000":
				if err := visit(filepath.Join(repo, filepath.FromSlash(name)), rel); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(dir, ""); err != nil {
		return nil, err
	}
	return links, nil
}

func removedByClean(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if part == ".git" || part == "__pycache__" {
			return true
		}
	}
	return false
}

// windowsTree defines windows-v1 on any preparing host: only directories
// and independent regular files, no execute bits, no device/alias names.
// File symlinks become copies of their ultimate target. Directory links,
// cycles, broken links and case collisions are rejected rather than guessed.
// This only mutates a private preparation tree, never an installed package.
func windowsTree(ctx context.Context, dir string, declared map[string]string) error {
	ctx = contextio.OrBackground(ctx)
	links := map[string]string{}
	for name, target := range declared {
		if !removedByClean(name) {
			links[name] = target
		}
	}
	entries := map[string]fs.FileMode{}
	var names []string
	folded := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if err := safepath.CheckWindows(rel); err != nil {
			return err
		}
		key := strings.ToLower(rel)
		if old, ok := folded[key]; ok {
			return fmt.Errorf("Windows name collision: %s and %s", old, rel)
		}
		folded[key] = rel
		info, err := d.Info()
		if err != nil {
			return err
		}
		entries[rel] = info.Mode()
		names = append(names, rel)
		switch {
		case info.Mode().IsDir(), info.Mode().IsRegular():
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			links[rel] = filepath.ToSlash(target)
		default:
			return fmt.Errorf("%s: unsupported file type", rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	resolved := map[string]string{}
	visiting := map[string]bool{}
	var resolve func(string) (string, error)
	resolve = func(name string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if p, ok := resolved[name]; ok {
			return p, nil
		}
		if visiting[name] {
			return "", fmt.Errorf("%s: symlink cycle", name)
		}
		if len(visiting) >= 256 {
			return "", fmt.Errorf("%s: symlink chain is too long", name)
		}
		mode, ok := entries[name]
		if !ok {
			return "", fmt.Errorf("%s: missing symlink target", name)
		}
		if target, isLink := links[name]; isLink {
			if err := safepath.CheckLinkTarget(name, target); err != nil {
				return "", err
			}
			to := path.Join(path.Dir(name), target)
			if err := safepath.CheckWindows(to); err != nil {
				return "", err
			}
			visiting[name] = true
			p, err := resolve(to)
			delete(visiting, name)
			if err != nil {
				return "", err
			}
			resolved[name] = p
			return p, nil
		}
		if !mode.IsRegular() {
			return "", fmt.Errorf("%s: directory symlinks are not supported by windows-v1", name)
		}
		return name, nil
	}
	linkNames := make([]string, 0, len(links))
	for name := range links {
		linkNames = append(linkNames, name)
	}
	sort.Strings(linkNames)
	// Check the whole graph before replacing any placeholders.
	for _, name := range linkNames {
		if _, err := resolve(name); err != nil {
			return err
		}
	}
	for _, name := range names {
		if entries[name].IsDir() {
			continue
		}
		if _, isLink := links[name]; isLink {
			continue
		}
		if err := replaceRegular(ctx, root, name, name); err != nil {
			return err
		}
	}
	for _, name := range linkNames {
		if err := replaceRegular(ctx, root, name, resolved[name]); err != nil {
			return err
		}
	}
	return nil
}

// Replacing rather than chmod-ing also breaks hardlinks. The source handle
// is closed before rename, which is required on Windows.
func replaceRegular(ctx context.Context, root *os.Root, name, source string) error {
	in, err := root.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := path.Join(path.Dir(name), ".slepok-layout-"+rand.Text())
	out, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { out.Close(); root.Remove(tmp) }()
	if _, err := io.Copy(out, contextio.Reader{Context: ctx, Reader: in}); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := in.Close(); err != nil {
		return err
	}
	if err := root.Chmod(tmp, 0o644); err != nil {
		return err
	}
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		if err := root.Chmod(name, 0o644); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return root.Rename(tmp, name)
}
