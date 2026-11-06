package fetch

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/miloVV13/slepok/internal/pkgtar"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/store"
)

// installPython assembles site-packages without pip, setup.py, generated
// launchers, or host permissions. Native and foreign preparation emit the
// same canonical archive. The complete dependency graph is pinned by profile.
func (f *Fetcher) installPython(name string, src profile.Source, portable bool) (string, error) {
	work, err := f.Store.TempDir("python-")
	if err != nil {
		return "", err
	}
	defer store.RemoveTree(work)
	tree := pkgtar.NewTree(work)
	for _, a := range src.Python {
		archive, err := f.download(name, profile.Source{URL: a.URL, SHA256: a.SHA256})
		if err != nil {
			return "", err
		}
		if name == "project-python" {
			u, err := url.Parse(a.URL)
			if err != nil {
				return "", err
			}
			filename := path.Base(u.Path)
			if !strings.HasSuffix(filename, ".whl") {
				return "", fmt.Errorf("project Python dependencies require wheels")
			}
			file, err := os.Open(archive)
			if err != nil {
				return "", err
			}
			err = tree.File(f.ctx(), ".slepok-wheels/"+filename, 0o644, file)
			file.Close()
			if err != nil {
				return "", err
			}
		}
		sink := &pythonSink{portableSink: portableSink{tree, f.ctx()}, archive: a, seen: map[string]bool{}}
		format := a.Format
		if format == "wheel" {
			format = "zip"
		}
		if err := extractArchive(f.ctx(), archive, format, a.Strip, sink); err != nil {
			return "", fmt.Errorf("%s: Python archive %s: %w", name, a.URL, err)
		}
		for _, inc := range a.Include {
			if !sink.seen[inc] {
				return "", fmt.Errorf("Python archive missing include path %s", inc)
			}
		}
	}
	// PYTHONPATH exposes the library root; sitecustomize also processes the
	// pinned wheels' .pth files as an ordinary site-packages installation does.
	startup := "import os, site\nsite.addsitedir(os.path.dirname(__file__))\n"
	if name == "project-python" {
		startup = "import os, site\nfor directory in os.environ.get('PYTHONPATH', '').split(os.pathsep):\n    if directory: site.addsitedir(directory)\n"
	}
	if err := tree.File(f.ctx(), "sitecustomize.py", 0o644, strings.NewReader(startup)); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(work, "canonical-")
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest, _, err := tree.Write(f.ctx(), file)
	if err != nil {
		return "", err
	}
	if src.Digest != "" && src.Digest != digest {
		return "", fmt.Errorf("%s: package digest mismatch: got %s, want %s", name, digest, src.Digest)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return "", err
	}
	if portable {
		err = f.Store.InstallArchiveContext(f.ctx(), digest, file)
	} else {
		err = f.Store.InstallContext(f.ctx(), digest, file)
	}
	return digest, err
}

type pythonSink struct {
	portableSink
	archive profile.PythonArchive
	seen    map[string]bool
}

func (s *pythonSink) target(name string) (string, error) {
	if s.archive.Format == "wheel" {
		root, rest, _ := strings.Cut(name, "/")
		if strings.HasSuffix(root, ".data") {
			scheme, rel, _ := strings.Cut(rest, "/")
			switch scheme {
			case "purelib", "platlib":
				return rel, nil
			case "scripts":
				return "", nil // Invoke SDK scripts / python -m; never absolute shebangs.
			case "":
				return "", nil
			default:
				return "", fmt.Errorf("unsupported wheel installation scheme %q", scheme)
			}
		}

		return name, nil
	}
	for _, inc := range s.archive.Include {
		if name == inc || strings.HasPrefix(name, inc+"/") {
			s.seen[inc] = true
			return name, nil
		}
	}
	return "", nil
}

func (s *pythonSink) dir(name string) error {
	name, err := s.target(name)
	if err != nil || name == "" {
		return err
	}
	return s.tree.Dir(name)
}
func (s *pythonSink) file(name string, _ fs.FileMode, r io.Reader) error {
	name, err := s.target(name)
	if err != nil || name == "" {
		return err
	}
	return s.tree.File(s.ctx, name, 0o644, r)
}
func (s *pythonSink) symlink(name, target string) error {
	return fmt.Errorf("Python archive symlink %s is unsupported", name)
}
func (s *pythonSink) link(name, target string) error {
	return fmt.Errorf("Python archive hardlink %s is unsupported", name)
}
func (s *pythonSink) zipSymlink(name string, _ *zip.File) error {
	return fmt.Errorf("Python wheel symlink %s is unsupported", name)
}
