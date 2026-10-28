package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/project"
	"github.com/miloVV13/slepok/internal/store"
)

// register records the project in the store's registry so that gc keeps
// its packages. Failing to register must not fail the command itself.
func register(st *store.Store, dir string) {
	if err := st.Register(dir); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not record the project in the store registry: %v\n", err)
	}
}

func projectExists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, project.ConfigName))
	return err == nil
}

// --- list -------------------------------------------------------------

var listCmd = &command{
	name:    "list",
	summary: "show known projects and how much space the store uses",
	run: func(_ *flag.FlagSet, args []string) error {
		if err := maxArgs(args, 0); err != nil {
			return err
		}
		st, err := openStore()
		if err != nil {
			return err
		}
		projects, err := st.Projects()
		if err != nil {
			return err
		}
		if len(projects) == 0 {
			fmt.Println("No projects yet. Create one with 'slepok init' or restore one with 'slepok unpack'.")
		} else {
			fmt.Printf("%-10s %-8s %-8s %-16s %s\n", "STATUS", "PROFILE", "BOARD", "LAST USED", "PATH")
			for _, pr := range projects {
				status, prof, board := projectStatus(st, pr.Path)
				fmt.Printf("%-10s %-8s %-8s %-16s %s\n", status, prof, board, pr.LastUsed.Local().Format("2006-01-02 15:04"), pr.Path)
			}
		}

		usage, err := st.Usage()
		if err != nil {
			return err
		}
		fmt.Printf("\nStore %s:\n", st.Root())
		var total int64
		for _, area := range []string{"packages", "downloads", "tmp", "logs"} {
			u := usage[area]
			total += u.Bytes
			fmt.Printf("  %-10s %4d  %10s\n", area, u.Count, humanSize(u.Bytes))
		}
		fmt.Printf("  %-10s       %10s\n", "total", humanSize(total))
		fmt.Printf("\n'slepok gc' shows what can be freed before deleting anything.\n")
		return nil
	},
}

func projectStatus(st *store.Store, dir string) (status, prof, board string) {
	if !projectExists(dir) {
		return "missing", "-", "-"
	}
	p, err := project.Load(dir)
	if err != nil {
		return "invalid", "-", "-"
	}
	prof, board = p.Config.Profile, p.Config.Board
	if p.Lock == nil {
		return "no-lock", prof, board
	}
	for _, pkg := range p.Lock.PackagesFor(manifest.HostPlatform()) {
		if !st.Has(pkg.Source.Digest) {
			return "incomplete", prof, board
		}
	}
	return "ready", prof, board
}

// --- gc ---------------------------------------------------------------

var gcFlags struct{ dryRun, yes *bool }

var gcCmd = &command{
	name:    "gc",
	args:    "[flags]",
	summary: "free space: remove packages no known project uses, stale downloads and leftovers",
	flags: func(fs *flag.FlagSet) {
		gcFlags.dryRun = fs.Bool("n", false, "only show what would be removed")
		gcFlags.yes = fs.Bool("y", false, "remove without asking")
	},
	run: runGC,
}

func runGC(_ *flag.FlagSet, args []string) error {
	if err := maxArgs(args, 0); err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	projects, err := st.Projects()
	if err != nil {
		return err
	}

	// Everything a living project's lock refers to is kept. A lock that
	// cannot be read stops gc: guessing could delete packages in use.
	keep, keepDownloads := map[string]bool{}, map[string]bool{}
	var stale []string
	for _, pr := range projects {
		if !projectExists(pr.Path) {
			stale = append(stale, pr.Path)
			continue
		}
		lock, err := project.ReadLock(pr.Path)
		if err != nil {
			return fmt.Errorf("cannot read the lock of %s (%v); fix it or delete the project before running gc", pr.Path, err)
		}
		if lock == nil {
			continue
		}
		for _, pkg := range lock.Packages {
			if pkg.Source.Digest != "" {
				keep[pkg.Source.Digest] = true
			}
			if pkg.Source.SHA256 != "" {
				keepDownloads[pkg.Source.SHA256] = true
			}
		}
	}

	items, err := st.PlanGC(keep, keepDownloads, time.Now())
	if err != nil {
		return err
	}
	if len(items) == 0 && len(stale) == 0 {
		fmt.Println("Nothing to remove.")
		return nil
	}

	fmt.Printf("Kept: %d packages used by %d known projects.\n", len(keep), len(projects)-len(stale))
	fmt.Println("To remove:")
	var total int64
	for _, it := range items {
		total += it.Bytes
		name := it.Name
		if it.Kind == "package" {
			name = name[:len("sha256:")+12] + "…"
		}
		fmt.Printf("  %-9s %-28s %10s\n", it.Kind, name, humanSize(it.Bytes))
	}
	for _, p := range stale {
		fmt.Printf("  %-9s %s (directory or %s is gone)\n", "project", p, project.ConfigName)
	}
	fmt.Printf("Total: %s. Project sources are never touched.\n", humanSize(total))
	fmt.Println("Note: a project moved to another directory and not used there since is not known; its packages count as unused.")

	if *gcFlags.dryRun {
		return nil
	}
	if !*gcFlags.yes {
		ok, err := confirm("Remove? [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Nothing removed.")
			return nil
		}
	}
	if err := st.Delete(items); err != nil {
		return err
	}
	if err := st.Forget(stale...); err != nil {
		return err
	}
	fmt.Printf("Removed %s.\n", humanSize(total))
	return nil
}

var errNotInteractive = errors.New("not running interactively; pass -y to confirm or -n to only show the plan")

func confirm(prompt string) (bool, error) {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&fs.ModeCharDevice == 0 {
		return false, errNotInteractive
	}
	fmt.Print(prompt)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}
