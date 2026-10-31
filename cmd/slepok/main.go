// Command slepok packs an embedded project together with its build
// environment into a single snapshot file and restores it elsewhere.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "0.1.0-dev"

type command struct {
	name    string
	args    string
	summary string
	run     func(ctx context.Context, fs *flag.FlagSet, args []string) error
	flags   func(fs *flag.FlagSet)
}

var commands = []*command{
	initCmd, buildCmd, flashCmd, runCmd, shellCmd, packCmd, unpackCmd, validateCmd, inspectCmd, listCmd, gcCmd, versionCmd,
}

func usage() {
	fmt.Fprintf(os.Stderr, "Slepok packs an embedded project and its build environment into one file.\n\n")
	fmt.Fprintf(os.Stderr, "Usage: slepok <command> [flags]\n\nCommands:\n")
	for _, c := range commands {
		fmt.Fprintf(os.Stderr, "  %-9s %s\n", c.name, c.summary)
	}
	fmt.Fprintf(os.Stderr, "\nRun 'slepok <command> -h' for the flags of a command.\n")
}

// usageError is a mistake in how slepok was invoked (exit status 2).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// execute runs one slepok invocation; args excludes the program name.
func execute(args []string) error { return executeContext(context.Background(), args) }

func executeContext(ctx context.Context, args []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(args) == 0 {
		usage()
		return &usageError{"no command given"}
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage()
		return nil
	}
	for _, c := range commands {
		if c.name != args[0] {
			continue
		}
		fs := flag.NewFlagSet("slepok "+c.name, flag.ContinueOnError)
		fs.Usage = func() {
			fmt.Fprintf(os.Stderr, "Usage: slepok %s %s\n\n%s\n", c.name, c.args, c.summary)
			if hasFlags(fs) {
				fmt.Fprintf(os.Stderr, "\nFlags:\n")
				fs.PrintDefaults()
			}
		}
		if c.flags != nil {
			c.flags(fs)
		}
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return &usageError{err.Error()}
		}
		// Hold the store lock through package use, project lock publication
		// and registration. GC must plan and delete without concurrent users.
		switch c.name {
		case "init", "build", "flash", "run", "shell", "pack", "unpack", "list", "gc":
			st, err := openStore()
			if err != nil {
				return err
			}
			guard, err := st.Acquire(ctx, c.name == "gc")
			if err != nil {
				return fmt.Errorf("%s: waiting for store: %w", c.name, err)
			}
			defer guard.Close()
		}
		if err := c.run(ctx, fs, fs.Args()); err != nil {
			return fmt.Errorf("%s: %w", c.name, err)
		}
		return nil
	}
	usage()
	return &usageError{fmt.Sprintf("unknown command %q", args[0])}
}

func main() {
	ctx := context.Background()
	stop := func() {}
	// Only long-running commands with cancellable work intercept signals.
	if len(os.Args) >= 2 && (os.Args[1] == "init" || os.Args[1] == "build" || os.Args[1] == "flash" || os.Args[1] == "run" || os.Args[1] == "pack" || os.Args[1] == "unpack" || os.Args[1] == "validate") {
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	}
	err := executeContext(ctx, os.Args[1:])
	interrupted := ctx.Err() != nil
	stop()
	if interrupted {
		if len(os.Args) >= 2 && os.Args[1] == "flash" {
			fmt.Fprintln(os.Stderr, "slepok: interrupted; target programming may be incomplete, rerun flash to program and verify")
		} else if len(os.Args) >= 2 && os.Args[1] == "run" {
			fmt.Fprintln(os.Stderr, "slepok: interrupted; RAM loading may be incomplete, rerun run --ram to reload and verify")
		} else {
			fmt.Fprintln(os.Stderr, "slepok: interrupted (verified packages and partial downloads kept for reuse)")
		}
		os.Exit(130)
	}
	if err == nil {
		return
	}
	var ue *usageError
	if errors.As(err, &ue) {
		fmt.Fprintf(os.Stderr, "slepok: %v\n", err)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "slepok %v\n", err)
	os.Exit(1)
}

func hasFlags(fs *flag.FlagSet) bool {
	n := 0
	fs.VisitAll(func(*flag.Flag) { n++ })
	return n > 0
}
