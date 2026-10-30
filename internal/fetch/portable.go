package fetch

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/miloVV13/slepok/internal/pkgtar"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/safepath"
	"github.com/miloVV13/slepok/internal/store"
)

// InstallPortable prepares the Unix representation without creating Unix
// permissions or links on the host. The resulting tar is retained for pack,
// not exposed as a runnable environment.
func (f *Fetcher) InstallPortable(name string, src profile.Source) (string, error) {
	if err := f.ctx().Err(); err != nil {
		return "", err
	}
	if src.Layout != "" {
		return "", fmt.Errorf("portable Unix preparation requires the default layout")
	}
	if src.Digest != "" && f.Store.Has(src.Digest) {
		return src.Digest, nil
	}
	work, err := f.Store.TempDir("fetch-portable-")
	if err != nil {
		return "", err
	}
	defer store.RemoveTree(work)
	tree := pkgtar.NewTree(work)
	if src.Git != "" {
		dir := filepath.Join(work, "checkout")
		if err := f.cloneMode(name, src, dir, true); err != nil {
			return "", err
		}
		if err := f.portableGit(tree, dir); err != nil {
			return "", err
		}
	} else {
		archive, err := f.download(name, src)
		if err != nil {
			return "", err
		}
		if err := extractArchive(f.ctx(), archive, src.Format, src.Strip, portableSink{tree, f.ctx()}); err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
	}
	tree.Clean(func(part string) bool { return part == ".git" || part == "__pycache__" })
	file, err := os.CreateTemp(work, "canonical-")
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest, _, err := tree.Write(f.ctx(), file)
	if err != nil {
		return "", err
	}
	if src.Digest != "" && digest != src.Digest {
		return "", fmt.Errorf("%s: package digest mismatch: got %s, want %s", name, digest, src.Digest)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return "", err
	}
	if err := f.Store.InstallArchiveContext(f.ctx(), digest, file); err != nil {
		return "", err
	}
	return digest, nil
}

type portableSink struct {
	tree *pkgtar.Tree
	ctx  context.Context
}

func (w portableSink) dir(name string) error { return w.tree.Dir(name) }
func (w portableSink) file(name string, mode fs.FileMode, r io.Reader) error {
	return w.tree.File(w.ctx, name, int64(mode.Perm()), r)
}
func (w portableSink) symlink(name, target string) error { return w.tree.Symlink(name, target) }
func (w portableSink) link(name, target string) error    { return w.tree.Link(name, target) }
func (w portableSink) zipSymlink(name string, z *zip.File) error {
	r, err := z.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, 4097))
	if err != nil {
		return err
	}
	if len(data) > 4096 {
		return fmt.Errorf("%s: link target too long", name)
	}
	return w.symlink(name, string(data))
}

// Git records executable bits and symlink identity even with
// core.filemode=false/core.symlinks=false. Worktree attributes are irrelevant.
func (f *Fetcher) portableGit(tree *pkgtar.Tree, dir string) error {
	git, err := exec.LookPath("git")
	if err != nil {
		return err
	}
	modes := map[string]string{}
	var visit func(string, string) error
	visit = func(repo, prefix string) error {
		listing, err := f.runGit(git, "-C", repo, "ls-files", "--stage", "-z")
		if err != nil {
			return err
		}
		for _, rec := range strings.Split(listing, "\x00") {
			if rec == "" {
				continue
			}
			header, name, ok := strings.Cut(rec, "\t")
			fields := strings.Fields(header)
			if !ok || len(fields) != 3 || fields[2] != "0" {
				return fmt.Errorf("invalid git index record")
			}
			if err := safepath.CheckForHost(name); err != nil {
				return err
			}
			rel := path.Join(prefix, name)
			if removedByClean(rel) {
				continue
			}
			if fields[0] == "160000" {
				if err := visit(filepath.Join(repo, filepath.FromSlash(name)), rel); err != nil {
					return err
				}
				continue
			}
			modes[rel] = fields[0]
		}
		return nil
	}
	if err := visit(dir, ""); err != nil {
		return err
	}
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := f.ctx().Err(); err != nil {
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
		if removedByClean(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return tree.Dir(rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s: unexpected Git worktree type", rel)
		}
		switch modes[rel] {
		case "120000":
			if info.Size() > 4096 {
				return fmt.Errorf("%s: symlink target too long", rel)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return tree.Symlink(rel, string(data))
		case "100644", "100755":
			r, err := os.Open(p)
			if err != nil {
				return err
			}
			defer r.Close()
			mode := int64(0644)
			if modes[rel] == "100755" {
				mode = 0755
			}
			return tree.File(f.ctx(), rel, mode, r)
		default:
			return fmt.Errorf("%s: missing or unsupported Git index mode %q", rel, modes[rel])
		}
	})
}
