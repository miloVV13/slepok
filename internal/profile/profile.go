// Package profile loads the platform profiles built into slepok (spec 7.4).
package profile

import (
	"embed"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/miloVV13/slepok/internal/manifest"
)

//go:embed profiles/*.toml components/*.toml all:templates
var builtin embed.FS

// FS holds profiles (profiles/<id>.toml), shared components
// (components/<name>.toml), and project templates (templates/<id>/).
// It is the built-in set; tests replace it.
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
	Path             []string          `toml:"path"`
	Env              map[string]string `toml:"env"`
	Configure        []string          `toml:"configure"`
	Outputs          []string          `toml:"outputs"`
	Components       []Component       `toml:"components"`
	SharedComponents []string          `toml:"shared_components,omitempty"`
	Hosts            map[string]Host   `toml:"hosts,omitempty"`
}

// Host customizes invocation details without changing the shared profile or
// its locked components. Configure adds flags; Path replaces the search path.
type Host struct {
	Path      []string          `toml:"path"`
	Env       map[string]string `toml:"env"`
	Configure []string          `toml:"configure"`
}

func (p *Profile) ForHost(platform string) *Profile {
	h, ok := p.Hosts[platform]
	if !ok {
		return p
	}
	out := *p
	if h.Path != nil {
		out.Path = slices.Clone(h.Path)
	}
	out.Configure = append(slices.Clone(p.Configure), h.Configure...)
	out.Env = maps.Clone(p.Env)
	if out.Env == nil {
		out.Env = map[string]string{}
	}
	for k, v := range h.Env {
		out.Env[k] = v
	}
	return &out
}

type Component struct {
	// BuildOptional permits older locks without a flashing-only tool.
	// Init still pins and installs it; flash checks it separately.
	BuildOptional bool `toml:"build_optional,omitempty"`
	// Platforms restricts a host-specific runtime dependency; empty means all.
	Platforms []string `toml:"platforms,omitempty"`
	Name      string   `toml:"name"`
	Version   string   `toml:"version"`
	License   string   `toml:"license"`
	// Sources maps a platform, or "any", to where the component comes from.
	Sources map[string]Source `toml:"sources"`
	// SourceCode is the corresponding source published by the vendor, for
	// licenses such as the GPL that oblige distributors to offer it.
	SourceCode *SourceArchive `toml:"source_code,omitempty"`
	// Notice warns about license conditions beyond keeping notices, such
	// as non-commercial terms. Shown by pack, unpack and inspect.
	Notice string `toml:"notice,omitempty"`
}

func (c Component) Supports(platform string) bool {
	return len(c.Platforms) == 0 || slices.Contains(c.Platforms, platform)
}

func (p *Profile) ComponentsFor(platform string) []Component {
	var out []Component
	for _, c := range p.Components {
		if c.Supports(platform) {
			out = append(out, c)
		}
	}
	return out
}

// SourceArchive is a downloadable source archive.
type SourceArchive struct {
	URL     string   `toml:"url"`
	Mirrors []string `toml:"mirrors,omitempty"`
	SHA256  string   `toml:"sha256"`
}

// Source is either a downloadable archive or a git checkout.
type Source struct {
	// Layout selects an explicit, versioned package representation, independent
	// of the machine preparing it. Empty retains the original native layout.
	Layout string `toml:"layout,omitempty"`
	URL    string `toml:"url,omitempty"`
	// Mirrors serve the same file; the sha256 decides, not the source.
	Mirrors []string `toml:"mirrors,omitempty"`
	SHA256  string   `toml:"sha256,omitempty"`
	Format  string   `toml:"format,omitempty"`
	Strip   int      `toml:"strip,omitempty"`

	Git    string `toml:"git,omitempty"`
	Ref    string `toml:"ref,omitempty"`
	Commit string `toml:"commit,omitempty"`

	// Digest is the expected package digest. Empty means not pinned yet.
	Digest string `toml:"digest,omitempty"`
}

const LayoutWindows = "windows-v1"

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
	var shared []Component
	for _, name := range p.SharedComponents {
		if !componentNameRe.MatchString(name) {
			return nil, fmt.Errorf("profile %s: invalid shared component %q", id, name)
		}
		data, err := fs.ReadFile(FS, path.Join("components", name+".toml"))
		if err != nil {
			return nil, fmt.Errorf("profile %s: shared component %s: %w", id, name, err)
		}
		var component Component
		md, err := toml.Decode(string(data), &component)
		if err != nil {
			return nil, fmt.Errorf("shared component %s: %w", name, err)
		}
		if len(md.Undecoded()) > 0 {
			return nil, fmt.Errorf("shared component %s: unknown keys %v", name, md.Undecoded())
		}
		if component.Name != name {
			return nil, fmt.Errorf("shared component %s declares name %q", name, component.Name)
		}
		shared = append(shared, component)
	}
	p.Components = append(shared, p.Components...)
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("profile %s: %w", id, err)
	}
	return &p, nil
}

var (
	sha256Re        = regexp.MustCompile(`^[0-9a-f]{64}$`)
	componentNameRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	commitRe        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	hostRe          = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)
	formats         = []string{"tar.gz", "tar.xz", "zip"}
)

func (p *Profile) validate() error {
	if p.ID == "" || p.Version == "" || len(p.Boards) == 0 {
		return fmt.Errorf("id, version and boards are required")
	}
	names := map[string]bool{}
	for _, c := range p.Components {
		for _, platform := range c.Platforms {
			if !hostRe.MatchString(platform) {
				return fmt.Errorf("%s: invalid platform %q", c.Name, platform)
			}
		}
		if c.Name == "" || names[c.Name] {
			return fmt.Errorf("component name %q missing or duplicated", c.Name)
		}
		names[c.Name] = true
		if sc := c.SourceCode; sc != nil && (sc.URL == "" || !sha256Re.MatchString(sc.SHA256)) {
			return fmt.Errorf("%s: source_code needs url and sha256", c.Name)
		}
		for platform, s := range c.Sources {
			where := c.Name + "/" + platform
			if s.Layout != "" && s.Layout != LayoutWindows {
				return fmt.Errorf("%s: unknown layout %q", where, s.Layout)
			}
			if s.Layout == LayoutWindows && !strings.HasPrefix(platform, "windows-") {
				return fmt.Errorf("%s: Windows layout needs a Windows platform", where)
			}
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
	for platform, h := range p.Hosts {
		if !hostRe.MatchString(platform) {
			return fmt.Errorf("invalid host %q", platform)
		}
		for _, s := range slices.Concat(h.Path, h.Configure, slices.Collect(mapValues(h.Env))) {
			if _, err := Expand(s, vars); err != nil {
				return fmt.Errorf("host %s: %w", platform, err)
			}
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
