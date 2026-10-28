// Command slepok packs an embedded project together with its build
// environment into a single snapshot file and restores it elsewhere.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "0.1.0-dev"

type command struct {
	name    string
	args    string
	summary string
	run     func(fs *flag.FlagSet, args []string) error
	flags   func(fs *flag.FlagSet)
}

var commands = []*command{
	initCmd, buildCmd, shellCmd, packCmd, unpackCmd, validateCmd, inspectCmd, listCmd, gcCmd, versionCmd,
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
func execute(args []string) error {
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
		if err := c.run(fs, fs.Args()); err != nil {
			return fmt.Errorf("%s: %w", c.name, err)
		}
		return nil
	}
	usage()
	return &usageError{fmt.Sprintf("unknown command %q", args[0])}
}

func main() {
	err := execute(os.Args[1:])
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
