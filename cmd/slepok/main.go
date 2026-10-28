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

func main() {
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help" {
		usage()
		if len(os.Args) < 2 {
			os.Exit(2)
		}
		return
	}
	for _, c := range commands {
		if c.name != os.Args[1] {
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
		if err := fs.Parse(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			os.Exit(2)
		}
		if err := c.run(fs, fs.Args()); err != nil {
			fmt.Fprintf(os.Stderr, "slepok %s: %v\n", c.name, err)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "slepok: unknown command %q\n\n", os.Args[1])
	usage()
	os.Exit(2)
}

func hasFlags(fs *flag.FlagSet) bool {
	n := 0
	fs.VisitAll(func(*flag.Flag) { n++ })
	return n > 0
}
