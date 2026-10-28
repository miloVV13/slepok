package project

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	gitignore "github.com/sabhiram/go-gitignore"
)

// defaultIgnore lists what never belongs in a snapshot.
var defaultIgnore = []string{
	".git/",
	"*.slepok",
	".*.slepok.tmp-*",
	"__pycache__/",
	".cache/",
	"compile_commands.json",
	".DS_Store",
	"*.swp",
	"*~",
}

// sensitivePatterns flag files that may hold secrets (spec 8.2).
var sensitivePatterns = []string{
	".env", ".env.*", "*.pem", "*.key", "id_rsa*", "id_dsa*", "id_ecdsa*",
	"id_ed25519*", "*.p12", "*.pfx", "*.keystore", ".netrc", ".npmrc", ".pypirc",
}

// Selection is the set of project files that go into a snapshot.
type Selection struct {
	// Files are slash-separated paths relative to the project directory.
	Files []string
	// Sensitive is the subset of Files that looks like it holds secrets.
	Sensitive []string
}

// Files selects the files of the project for a snapshot: everything except
// the defaults, the build directory and the patterns in .slepokignore.
func (p *Project) Files() (*Selection, error) {
	lines := append([]string{"/" + p.Config.BuildDir + "/"}, defaultIgnore...)
	extra, err := readLines(filepath.Join(p.Dir, IgnoreName))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	ignore := gitignore.CompileIgnoreLines(append(lines, extra...)...)
	sensitive := gitignore.CompileIgnoreLines(sensitivePatterns...)

	sel := &Selection{}
	err = filepath.WalkDir(p.Dir, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if abs == p.Dir {
			return nil
		}
		rel, err := filepath.Rel(p.Dir, abs)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if ignore.MatchesPath(rel + "/") {
				return filepath.SkipDir
			}
			return nil
		}
		if ignore.MatchesPath(rel) {
			return nil
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symlink; snapshots support only regular files (exclude it in %s)", rel, IgnoreName)
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file (exclude it in %s)", rel, IgnoreName)
		}
		sel.Files = append(sel.Files, rel)
		if sensitive.MatchesPath(path.Base(rel)) {
			sel.Sensitive = append(sel.Sensitive, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sel, nil
}

func readLines(p string) ([]string, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		lines = append(lines, s.Text())
	}
	return lines, s.Err()
}
