// Package cmd is the rosidl-gen command line: flag wiring and subcommand
// dispatch. The work itself lives in cmd/generate.
package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/HeoJeongBo/rosidl-gen-go/cmd/wirelock"
	"github.com/HeoJeongBo/rosidl-gen-go/gogen"
)

// Exit codes. Drift gets its own so a CI job can tell "the committed output is
// stale" apart from "the config is broken"; 2 is left to the flag package,
// which uses it for a usage error.
const (
	exitOK    = 0
	exitError = 1
	exitDrift = 3
)

type command struct {
	name  string
	brief string
	run   func(args []string) error
}

func commands() []command {
	return []command{
		{"generate", "emit the configured interfaces (default)", runGenerate},
		{"wirelock", "write or verify the CDR wire layout lock", runWirelock},
		{"list", "list the interfaces the search paths contain", runList},
		{"explain", "trace why an interface is in the output", runExplain},
		{"init", "print a commented config skeleton", runInit},
		{"version", "print version information", runVersion},
	}
}

// Main runs the CLI and exits the process.
func Main() { os.Exit(main(os.Args[1:])) }

func main(args []string) int {
	// A leading non-flag word selects a subcommand; anything else is the
	// default generate path, which keeps `rosidl-gen -config x -check`
	// working exactly as before.
	name := "generate"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}

	var cmd *command
	for _, c := range commands() {
		if c.name == name {
			cmd = &c
			break
		}
	}
	if cmd == nil {
		fmt.Fprintf(os.Stderr, "rosidl-gen: unknown subcommand %q\n", name)
		// The most likely way to land here is passing the config positionally,
		// which used to be ignored in favour of the default path.
		if looksLikePath(name) {
			fmt.Fprintf(os.Stderr, "\tto use it as the config, write: rosidl-gen -config %s\n\n", name)
		}
		usage(os.Stderr)
		return exitError
	}

	switch err := cmd.run(args); {
	case err == nil:
		return exitOK
	case errors.Is(err, flagParseError):
		return exitError
	default:
		fmt.Fprintf(os.Stderr, "rosidl-gen: %v\n", err)
		var drift *gogen.DriftError
		if errors.As(err, &drift) {
			return exitDrift
		}
		var moved *wirelock.Drift
		if errors.As(err, &moved) {
			return exitDrift
		}
		return exitError
	}
}

// flagParseError marks a failure the flag package already reported, so Main
// does not print it a second time.
var flagParseError = errors.New("flag parse error")

func looksLikePath(s string) bool {
	if strings.ContainsAny(s, "/\\") || strings.HasSuffix(s, ".yaml") || strings.HasSuffix(s, ".yml") {
		return true
	}
	_, err := os.Stat(s)
	return err == nil
}

func usage(w *os.File) {
	fmt.Fprint(w, `rosidl-gen generates Go types from ROS 2 interface definitions.

Usage:
    rosidl-gen [flags]              emit the configured interfaces
    rosidl-gen <command> [flags]

Commands:
`)
	for _, c := range commands() {
		fmt.Fprintf(w, "    %-10s %s\n", c.name, c.brief)
	}
	fmt.Fprint(w, `
Run "rosidl-gen <command> -h" for a command's flags.
The config format is documented at https://github.com/HeoJeongBo/rosidl-gen-go
`)
}
