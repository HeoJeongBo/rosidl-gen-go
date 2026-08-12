package cmd

import (
	"fmt"
	"os"
	"runtime/debug"
)

// runVersion prints shell-sourceable KEY=value lines.
//
// The version is deliberately not recorded in generated files: `-check`
// compares bytes, so stamping it would make every tool upgrade rewrite every
// generated file in every consumer.
func runVersion(args []string) error {
	fs := newFlagSet("version", "version")
	if err := parse(fs, args, 0); err != nil {
		return err
	}

	version, revision, dirty := buildInfo()
	fmt.Fprintf(os.Stdout, "ROSIDL_GEN_VERSION=%s\n", version)
	fmt.Fprintf(os.Stdout, "ROSIDL_GEN_REVISION=%s\n", revision)
	fmt.Fprintf(os.Stdout, "ROSIDL_GEN_DIRTY=%t\n", dirty)
	return nil
}

// buildInfo reads what the Go toolchain recorded. A `go run` of a working tree
// has no module version, hence the fallbacks.
func buildInfo() (version, revision string, dirty bool) {
	version, revision = "(devel)", "unknown"

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version, revision, false
	}
	if v := info.Main.Version; v != "" {
		version = v
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return version, revision, dirty
}
