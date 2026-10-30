package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/miloVV13/slepok/internal/contextio"
	"github.com/miloVV13/slepok/internal/fetch"
	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/project"
	"github.com/miloVV13/slepok/internal/runner"
	"github.com/miloVV13/slepok/internal/snapshot"
	"github.com/miloVV13/slepok/internal/store"
)

func openStore() (*store.Store, error) {
	root, err := store.DefaultRoot()
	if err != nil {
		return nil, err
	}
	return store.Open(root)
}

func maxArgs(args []string, n int) error {
	if len(args) > n {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(args[n:], " "))
	}
	return nil
}

// --- init -------------------------------------------------------------

var initFlags struct{ profile, board *string }

var initCmd = &command{
	name:    "init",
	args:    "[flags] [dir]",
	summary: "create a project or set up the environment of an existing one (needs network)",
	flags: func(fs *flag.FlagSet) {
		initFlags.profile = fs.String("profile", "rp2040", "platform profile ("+strings.Join(profile.IDs(), ", ")+")")
		initFlags.board = fs.String("board", "", "board (default: the profile's first board)")
	},
	run: runInit,
}

func runInit(ctx context.Context, fs *flag.FlagSet, args []string) error {
	if err := maxArgs(args, 1); err != nil {
		return err
	}
	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	var cfg project.Config
	if p, err := project.Load(dir); err == nil {
		cfg = p.Config
		if (explicit["profile"] && *initFlags.profile != cfg.Profile) || (explicit["board"] && *initFlags.board != cfg.Board) {
			return fmt.Errorf("%s already sets profile %s, board %s; edit it to change them", project.ConfigName, cfg.Profile, cfg.Board)
		}
		fmt.Printf("Setting up the environment of %s\n", dir)
	} else if _, statErr := os.Stat(filepath.Join(dir, project.ConfigName)); statErr == nil {
		return err // slepok.toml exists but is invalid
	} else {
		prof, err := profile.Load(*initFlags.profile)
		if err != nil {
			return err
		}
		board := *initFlags.board
		if board == "" {
			board = prof.Boards[0]
		}
		cfg = project.Config{Profile: prof.ID, Board: board, Platforms: []string{manifest.HostPlatform()}, BuildDir: "build"}
		if err := cfg.Validate(); err != nil {
			return err
		}
		empty, err := isEmpty(dir)
		if err != nil {
			return err
		}
		if empty {
			if err := writeTemplate(dir, prof.ID); err != nil {
				return err
			}
			fmt.Printf("Created a %s blink project in %s\n", prof.ID, dir)
		} else {
			fmt.Printf("Adding slepok to the existing project in %s\n", dir)
		}
		if err := project.WriteConfig(dir, cfg); err != nil {
			return err
		}
	}

	prof, err := profile.Load(cfg.Profile)
	if err != nil {
		return err
	}
	lock, err := lockFor(dir, cfg, prof)
	if err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	f := fetch.New(st, os.Stdout)
	f.Context = ctx
	for i := range lock.Packages {
		pkg := &lock.Packages[i]
		fmt.Printf("%s %s (%s)\n", pkg.Name, pkg.Version, pkg.Platform)
		digest, err := f.Install(pkg.Name, pkg.Source)
		if err != nil {
			return err
		}
		pkg.Source.Digest = digest
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if lock.SourceDateEpoch == 0 {
		lock.SourceDateEpoch = time.Now().Unix()
	}
	if err := project.WriteLock(dir, *lock); err != nil {
		return err
	}
	register(st, dir)
	fmt.Printf("\nEnvironment ready: %d packages, pinned in %s.\nNext: slepok build\n", len(lock.Packages), project.LockName)
	return nil
}

// lockFor returns the existing lock of the project, so that every machine
// gets exactly the same packages, or a new one from the profile.
func lockFor(dir string, cfg project.Config, prof *profile.Profile) (*project.Lock, error) {
	if p, err := project.Load(dir); err == nil && p.Lock != nil {
		if p.Lock.Profile != cfg.Profile {
			return nil, fmt.Errorf("%s is for profile %s, %s says %s", project.LockName, p.Lock.Profile, project.ConfigName, cfg.Profile)
		}
		if p.Lock.ProfileVersion != prof.Version {
			fmt.Printf("note: %s was created with profile %s version %s, this slepok has version %s; keeping the locked packages\n",
				project.LockName, prof.ID, p.Lock.ProfileVersion, prof.Version)
		}
		return p.Lock, nil
	}
	lock := &project.Lock{Profile: prof.ID, ProfileVersion: prof.Version}
	seen := map[string]bool{}
	for _, platform := range cfg.Platforms {
		for _, c := range prof.Components {
			src, recorded, err := c.Source(platform)
			if err != nil {
				return nil, err
			}
			if key := c.Name + "/" + recorded; !seen[key] {
				seen[key] = true
				lock.Packages = append(lock.Packages, project.LockedPackage{
					Name: c.Name, Version: c.Version, Platform: recorded, License: c.License, Source: src,
				})
			}
		}
	}
	return lock, nil
}

func isEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Name() != ".git" {
			return false, nil
		}
	}
	return true, nil
}

var nonIdent = regexp.MustCompile(`[^A-Za-z0-9_]`)

func writeTemplate(dir, profileID string) error {
	name := nonIdent.ReplaceAllString(filepath.Base(dir), "_")
	if name == "" || (name[0] >= '0' && name[0] <= '9') {
		name = "app_" + name
	}
	files, err := profile.Template(profileID, name)
	if err != nil {
		return err
	}
	for _, rel := range slices.Sorted(func(yield func(string) bool) {
		for k := range files {
			if !yield(k) {
				return
			}
		}
	}) {
		if err := os.WriteFile(filepath.Join(dir, rel), files[rel], 0o644); err != nil {
			return err
		}
	}
	return nil
}

// --- build ------------------------------------------------------------

var buildCmd = &command{
	name:    "build",
	args:    "",
	summary: "build the project in its environment, without network access",
	run: func(ctx context.Context, _ *flag.FlagSet, args []string) error {
		if err := maxArgs(args, 0); err != nil {
			return err
		}
		p, env, err := loadEnv()
		if err != nil {
			return err
		}
		res, err := runner.Build(env, p.Dir, p.BuildDir(), runner.Options{
			Context: ctx,
			Isolate: true, Stdout: os.Stdout, Stderr: os.Stderr, Warn: os.Stderr,
		})
		if err != nil {
			return err
		}
		reportLeaks(os.Stderr, res.Leaks)
		fmt.Printf("\nBuilt %s:\n", relTo(p.BuildDir()))
		for _, o := range res.Outputs {
			fmt.Printf("  %s\n", o.Path)
		}
		return nil
	},
}

func loadEnv() (*project.Project, *runner.Env, error) {
	p, err := project.Find(".")
	if err != nil {
		return nil, nil, err
	}
	prof, err := profile.Load(p.Config.Profile)
	if err != nil {
		return nil, nil, err
	}
	st, err := openStore()
	if err != nil {
		return nil, nil, err
	}
	register(st, p.Dir)
	env, err := runner.Resolve(prof, p.Lock, st, manifest.HostPlatform(), p.Config.Board)
	if err != nil {
		return nil, nil, err
	}
	return p, env, nil
}

func reportLeaks(w io.Writer, leaks []string) {
	if len(leaks) == 0 {
		return
	}
	fmt.Fprintf(w, "warning: the build configuration refers to %d host paths; it may not build on another machine:\n", len(leaks))
	for _, l := range leaks {
		fmt.Fprintf(w, "  %s\n", l)
	}
}

func relTo(p string) string {
	wd, err := os.Getwd()
	if err != nil {
		return p
	}
	if rel, err := filepath.Rel(wd, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

// --- shell ------------------------------------------------------------

var shellCmd = &command{
	name:    "shell",
	args:    "",
	summary: "open a shell with the project environment",
	run: func(ctx context.Context, _ *flag.FlagSet, args []string) error {
		if err := maxArgs(args, 0); err != nil {
			return err
		}
		p, env, err := loadEnv()
		if err != nil {
			return err
		}
		st, err := openStore()
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "slepok shell: %s environment, board %s.\n", p.Config.Profile, p.Config.Board)
		fmt.Fprintf(os.Stderr, "Environment tools come first in PATH; cmake configures with the profile's settings.\n")
		fmt.Fprintf(os.Stderr, "This shell is not isolated from the host or the network: 'slepok build' is the reference build.\n")
		fmt.Fprintf(os.Stderr, "Type 'exit' to leave.\n")
		return runner.Shell(env, p.Dir, st, os.Stdin, os.Stdout, os.Stderr)
	},
}

// --- pack -------------------------------------------------------------

var packFlags struct {
	output         *string
	allowSensitive *bool
	withSources    *bool
}

var packCmd = &command{
	name:    "pack",
	args:    "[flags]",
	summary: "build the project offline from a clean copy and write a snapshot",
	flags: func(fs *flag.FlagSet) {
		packFlags.output = fs.String("o", "", "snapshot file (default: <project>.slepok in the current directory)")
		packFlags.allowSensitive = fs.Bool("allow-sensitive", false, "include files that look like secrets")
		packFlags.withSources = fs.Bool("with-sources", false, "bundle the source code of GPL components (downloads it; needed to publish snapshots)")
	},
	run: runPack,
}

type report struct {
	CreatedBy       string          `json:"createdBy"`
	Profile         string          `json:"profile"`
	ProfileVersion  string          `json:"profileVersion"`
	Board           string          `json:"board"`
	Platform        string          `json:"platform"`
	NetworkIsolated bool            `json:"networkIsolated"`
	BuiltAt         time.Time       `json:"builtAt"`
	Duration        string          `json:"duration"`
	Outputs         []runner.Output `json:"outputs"`
}

func runPack(ctx context.Context, _ *flag.FlagSet, args []string) error {
	if err := maxArgs(args, 0); err != nil {
		return err
	}
	p, env, err := loadEnv()
	if err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	out := *packFlags.output
	if out == "" {
		out = filepath.Base(p.Dir) + ".slepok"
	}

	sel, err := p.Files()
	if err != nil {
		return err
	}
	if len(sel.Sensitive) > 0 && !*packFlags.allowSensitive {
		return fmt.Errorf("these files look like secrets and are not packed by default:\n  %s\nexclude them in %s or pass -allow-sensitive",
			strings.Join(sel.Sensitive, "\n  "), project.IgnoreName)
	}
	prof, err := profile.Load(p.Config.Profile)
	if err != nil {
		return err
	}
	packages, err := snapshotPackages(p, st, prof)
	if err != nil {
		return err
	}
	var sources []snapshot.Extra
	if *packFlags.withSources {
		if sources, err = sourceExtras(ctx, st, prof, packages); err != nil {
			return err
		}
	}
	extras := append([]snapshot.Extra{{Name: noticeEntry, Data: noticeText(packages, sources)}}, sources...)

	work, err := st.TempDir("pack-")
	if err != nil {
		return err
	}
	defer store.RemoveTree(work)
	copyDir := filepath.Join(work, filepath.Base(p.Dir))
	if err := copyFilesContext(ctx, p.Dir, copyDir, sel.Files); err != nil {
		return err
	}

	logPath, logFile, err := openLog(st, "pack")
	if err != nil {
		return err
	}
	defer logFile.Close()
	fmt.Printf("Verifying: offline build of a clean copy (%d files)...\n", len(sel.Files))
	start := time.Now()
	res, err := runner.Build(env, copyDir, filepath.Join(work, "build"), runner.Options{
		Context: ctx,
		Isolate: true, Stdout: logFile, Stderr: logFile, Warn: os.Stderr,
	})
	if err != nil {
		return fmt.Errorf("%w\nfull log: %s", err, logPath)
	}
	if len(res.Leaks) > 0 {
		reportLeaks(os.Stderr, res.Leaks)
		return fmt.Errorf("refusing to pack a build that depends on the host; full log: %s", logPath)
	}
	if !res.Isolated {
		fmt.Fprintf(os.Stderr, "warning: the verification build had network access, so offline builds are not proven\n")
	}
	rep, err := json.MarshalIndent(report{
		CreatedBy: "slepok " + version, Profile: p.Config.Profile, ProfileVersion: p.Lock.ProfileVersion,
		Board: p.Config.Board, Platform: manifest.HostPlatform(), NetworkIsolated: res.Isolated,
		BuiltAt: start.UTC().Truncate(time.Second), Duration: time.Since(start).Round(time.Second).String(), Outputs: res.Outputs,
	}, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Verified in %s. Writing %s...\n", time.Since(start).Round(time.Second), out)

	m, err := snapshot.Pack(out, snapshot.Options{
		Context:      ctx,
		CreatedBy:    "slepok " + version,
		Profile:      manifest.Profile{ID: p.Config.Profile, Version: p.Lock.ProfileVersion},
		Platforms:    p.Config.Platforms,
		ProjectDir:   copyDir,
		ProjectFiles: sel.Files,
		Packages:     packages,
		Report:       rep,
		Extras:       extras,
	})
	if err != nil {
		return err
	}
	info, err := os.Stat(out)
	if err != nil {
		return err
	}
	fmt.Printf("Wrote %s: %s, %d project files, %d packages.\n", out, humanSize(info.Size()), len(sel.Files), len(m.Packages))
	printNotices(os.Stdout, m, len(sources) > 0)
	return nil
}

// snapshotPackages lists the packages of every configured platform, each
// once. All of them must be in the store.
func snapshotPackages(p *project.Project, st *store.Store, prof *profile.Profile) ([]snapshot.PackageSource, error) {
	components := map[string]profile.Component{}
	for _, c := range prof.Components {
		components[c.Name] = c
	}
	seen := map[string]bool{}
	var out []snapshot.PackageSource
	for _, platform := range p.Config.Platforms {
		pkgs := p.Lock.PackagesFor(platform)
		if len(pkgs) == 0 {
			return nil, fmt.Errorf("%s has no packages for %s; run slepok init", project.LockName, platform)
		}
		for _, pkg := range pkgs {
			if seen[pkg.Source.Digest] {
				continue
			}
			seen[pkg.Source.Digest] = true
			if !st.Has(pkg.Source.Digest) {
				return nil, fmt.Errorf("package %s %s (%s) is not in the local store; run slepok init", pkg.Name, pkg.Version, pkg.Platform)
			}
			dir, err := st.Path(pkg.Source.Digest)
			if err != nil {
				return nil, err
			}
			ps := snapshot.PackageSource{
				Name: pkg.Name, Version: pkg.Version, Platform: pkg.Platform, License: pkg.License, Dir: dir,
			}
			if c, ok := components[pkg.Name]; ok && c.Version == pkg.Version {
				ps.Notice = c.Notice
				if c.SourceCode != nil {
					ps.SourceCode = c.SourceCode.URL
				}
			}
			out = append(out, ps)
		}
	}
	return out, nil
}

func copyFilesContext(ctx context.Context, src, dst string, files []string) error {
	for _, rel := range files {
		from := filepath.Join(src, filepath.FromSlash(rel))
		to := filepath.Join(dst, filepath.FromSlash(rel))
		info, err := os.Stat(from)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := copyFileContext(ctx, from, to, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func copyFileContext(ctx context.Context, from, to string, perm os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, contextio.Reader{Context: ctx, Reader: in}); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func openLog(st *store.Store, what string) (string, *os.File, error) {
	dir := filepath.Join(st.Root(), "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	p := filepath.Join(dir, what+"-"+time.Now().Format("20060102-150405")+".log")
	f, err := os.Create(p)
	return p, f, err
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// --- unpack, validate, inspect -----------------------------------------

var unpackCmd = &command{
	name:    "unpack",
	args:    "<file.slepok> [dir]",
	summary: "restore a snapshot into a new project directory (no network needed)",
	run: func(ctx context.Context, _ *flag.FlagSet, args []string) error {
		if len(args) < 1 {
			return fmt.Errorf("missing snapshot file")
		}
		if err := maxArgs(args, 2); err != nil {
			return err
		}
		dest := strings.TrimSuffix(filepath.Base(args[0]), ".slepok")
		if len(args) == 2 {
			dest = args[1]
		}
		st, err := openStore()
		if err != nil {
			return err
		}
		fmt.Printf("Verifying and restoring %s...\n", args[0])
		m, err := snapshot.UnpackContext(ctx, args[0], dest, st, manifest.HostPlatform())
		if err != nil {
			return err
		}
		register(st, dest)
		fmt.Printf("Restored %s (%s profile, %d packages).\n", dest, m.Profile.ID, len(m.Packages))
		printNotices(os.Stdout, m, hasSources(m))
		fmt.Printf("Next: cd %s && slepok build\n", dest)
		return nil
	},
}

var validateCmd = &command{
	name:    "validate",
	args:    "<file.slepok>",
	summary: "check the structure and checksums of a snapshot without extracting it",
	run: func(ctx context.Context, _ *flag.FlagSet, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("expected one snapshot file")
		}
		m, err := snapshot.ValidateContext(ctx, args[0])
		if err != nil {
			return err
		}
		fmt.Printf("%s is valid: %d files and %d packages match their checksums.\n", args[0], len(m.Files), len(m.Packages))
		return nil
	},
}

var inspectCmd = &command{
	name:    "inspect",
	args:    "<file.slepok>",
	summary: "show what a snapshot contains, reading only its manifest",
	run: func(ctx context.Context, _ *flag.FlagSet, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("expected one snapshot file")
		}
		m, err := snapshot.ReadManifest(args[0])
		if err != nil {
			return err
		}
		var size int64
		for _, f := range m.Files {
			size += f.Size
		}
		fmt.Printf("Snapshot:   %s\n", args[0])
		fmt.Printf("Format:     %d\n", m.FormatVersion)
		fmt.Printf("Created:    %s by %s\n", m.CreatedAt.Local().Format(time.DateTime), m.CreatedBy)
		fmt.Printf("Profile:    %s (version %s)\n", m.Profile.ID, m.Profile.Version)
		fmt.Printf("Platforms:  %s\n", strings.Join(m.Platforms, ", "))
		fmt.Printf("Files:      %d (%s)\n", len(m.Files), humanSize(size))
		fmt.Printf("Packages:\n")
		for _, pkg := range m.Packages {
			fmt.Printf("  %-10s %-18s %-10s %9s  %s\n", pkg.Name, pkg.Version, pkg.Platform, humanSize(pkg.Size), pkg.License)
		}
		for _, f := range m.Files {
			if strings.HasPrefix(f.Path, "sources/") {
				fmt.Printf("Source:     %s (%s)\n", f.Path, humanSize(f.Size))
			}
		}
		fmt.Println()
		printNotices(os.Stdout, m, hasSources(m))
		fmt.Printf("\nOnly the manifest was read; run 'slepok validate' to check the contents.\n")
		return nil
	},
}

var versionCmd = &command{
	name:    "version",
	summary: "print the slepok version",
	run: func(context.Context, *flag.FlagSet, []string) error {
		isolation := "available"
		if !runner.IsolationAvailable() {
			isolation = "not available: builds can reach the network"
		}
		fmt.Printf("slepok %s (snapshot format %d, %s)\nnetwork isolation: %s\n", version, manifest.FormatVersion, manifest.HostPlatform(), isolation)
		return nil
	},
}
