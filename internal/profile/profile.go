// Package profile loads the platform profiles built into slepok (spec 7.4).
package profile

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/miloVV13/slepok/internal/manifest"
)

//go:embed profiles/*.toml all:templates
var builtin embed.FS

// FS holds the profiles (profiles/<id>.toml) and project templates
// (templates/<id>/). It is the built-in set; tests replace it.
var FS fs.FS = builtin

// Template returns the starter project of a profile: file names mapped to
// contents, with @NAME@ replaced by name. A stored "gitignore" becomes
// ".gitignore" (embedding dot files needs care).
func Template(id, name string) (map[string][]byte, error) {
	dir := path.Join("templates", id)
	entries, err := fs.ReadDir(FS, dir)
	if err != nil {
		return nil, fmt.Errorf("profile %s has no project template", id)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		data, err := fs.ReadFile(FS, path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		target := e.Name()
		if target == "gitignore" {
			target = ".gitignore"
		}
		out[target] = []byte(strings.ReplaceAll(string(data), "@NAME@", name))
	}
	return out, nil
}

type Profile struct {
	ID          string `toml:"id"`
	Version     string `toml:"version"`
	Description string `toml:"description"`
	// Boards lists the supported boards; the first one is the default.
	Boards []string `toml:"boards"`
	// Path is prepended to PATH; Env holds further variables; Configure
	// holds the CMake configure arguments. All may use placeholders.
	Path       []string          `toml:"path"`
	Env        map[string]string `toml:"env"`
	Configure  []string          `toml:"configure"`
	Outputs    []string          `toml:"outputs"`
	Components []Component       `toml:"components"`
}

type Component struct {
	Name    string `toml:"name"`
	Version string `toml:"version"`
	License string `toml:"license"`
	// Sources maps a platform, or "any", to where the component comes from.
	Sources map[string]Source `toml:"sources"`
}

// Source is either a downloadable archive or a git checkout.
type Source struct {
	URL    string `toml:"url,omitempty"`
	SHA256 string `toml:"sha256,omitempty"`
	Format string `toml:"format,omitempty"`
	Strip  int    `toml:"strip,omitempty"`

	Git    string `toml:"git,omitempty"`
	Ref    string `toml:"ref,omitempty"`
	Commit string `toml:"commit,omitempty"`

	// Digest is the expected package digest. Empty means not pinned yet.
	Digest string `toml:"digest,omitempty"`
}

// IDs returns the identifiers of the built-in profiles.
func IDs() []string {
	entries, _ := fs.ReadDir(FS, "profiles")
	var ids []string
	for _, e := range entries {
		ids = append(ids, strings.TrimSuffix(e.Name(), ".toml"))
	}
	return ids
}

// Load returns the built-in profile with the given id.
func Load(id string) (*Profile, error) {
	data, err := fs.ReadFile(FS, path.Join("profiles", id+".toml"))
	if err != nil {
		return nil, fmt.Errorf("unknown profile %q (available: %s)", id, strings.Join(IDs(), ", "))
	}
	var p Profile
	md, err := toml.Decode(string(data), &p)
	if err != nil {
		return nil, fmt.Errorf("profile %s: %w", id, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return nil, fmt.Errorf("profile %s: unknown keys %v", id, undecoded)
	}
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("profile %s: %w", id, err)
	}
	return &p, nil
}

var (
	sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)
	commitRe = regexp.MustCompile(`^[0-9a-f]{40}$`)
	formats  = []string{"tar.gz", "tar.xz", "zip"}
)

func (p *Profile) validate() error {
	if p.ID == "" || p.Version == "" || len(p.Boards) == 0 {
		return fmt.Errorf("id, version and boards are required")
	}
	names := map[string]bool{}
	for _, c := range p.Components {
		if c.Name == "" || names[c.Name] {
			return fmt.Errorf("component name %q missing or duplicated", c.Name)
		}
		names[c.Name] = true
		for platform, s := range c.Sources {
			where := c.Name + "/" + platform
			switch {
			case s.Git != "":
				if s.Ref == "" || !commitRe.MatchString(s.Commit) {
					return fmt.Errorf("%s: git source needs ref and a full commit hash", where)
				}
			case s.URL != "":
				if !sha256Re.MatchString(s.SHA256) || !slices.Contains(formats, s.Format) {
					return fmt.Errorf("%s: archive source needs sha256 and format (%s)", where, strings.Join(formats, ", "))
				}
			default:
				return fmt.Errorf("%s: source has neither url nor git", where)
			}
		}
	}
	// Placeholders may only name components or {board}.
	vars := map[string]string{"board": "x"}
	for n := range names {
		vars[n] = "x"
	}
	for _, s := range slices.Concat(p.Path, p.Configure, slices.Collect(mapValues(p.Env))) {
		if _, err := Expand(s, vars); err != nil {
			return err
		}
	}
	return nil
}

func mapValues(m map[string]string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for _, v := range m {
			if !yield(v) {
				return
			}
		}
	}
}

// Source returns the source of c for platform, falling back to "any". The
// second result is the platform the package is recorded under.
func (c Component) Source(platform string) (Source, string, error) {
	if s, ok := c.Sources[platform]; ok {
		return s, platform, nil
	}
	if s, ok := c.Sources[manifest.AnyPlatform]; ok {
		return s, manifest.AnyPlatform, nil
	}
	return Source{}, "", fmt.Errorf("component %s %s is not available for %s", c.Name, c.Version, platform)
}

var placeholderRe = regexp.MustCompile(`\{([a-z0-9_-]+)\}`)

// Expand replaces {name} placeholders with values from vars. Unknown
// names are an error.
func Expand(s string, vars map[string]string) (string, error) {
	var missing string
	out := placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		name := m[1 : len(m)-1]
		v, ok := vars[name]
		if !ok && missing == "" {
			missing = name
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("unknown placeholder {%s} in %q", missing, s)
	}
	return out, nil
}
