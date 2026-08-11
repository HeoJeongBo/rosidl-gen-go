// Package generate is the CLI's generation logic: load the config, resolve
// the interface set, emit, then write or byte-check the output.
package generate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/HeoJeongBo/rosidl-gen-go/cppnames"
	"github.com/HeoJeongBo/rosidl-gen-go/gogen"
)

// Options mirrors the CLI flags.
type Options struct {
	// ConfigPath locates the rosidl-gen.yaml document.
	ConfigPath string
	// Verbose lists the resolved interface set before emitting.
	Verbose bool
	// Check verifies the on-disk output byte-for-byte and writes nothing.
	Check bool
}

// Run executes one generation according to o.
func Run(o Options) error {
	cfg, err := gogen.LoadConfig(o.ConfigPath)
	if err != nil {
		return err
	}
	// Extension emitters claim their config sections here; whatever remains is
	// a typo or an emitter this build does not know.
	var extra []gogen.Emitter
	if names, ok, err := cppnames.FromConfig(cfg); err != nil {
		return fmt.Errorf("%s: %w", o.ConfigPath, err)
	} else if ok {
		extra = append(extra, names)
	}
	if unknown := cfg.UnclaimedSections(); len(unknown) > 0 {
		return fmt.Errorf("%s: unknown config section(s): %s", o.ConfigPath, strings.Join(unknown, ", "))
	}

	g, err := gogen.New(cfg)
	if err != nil {
		return err
	}
	if err := g.Resolve(); err != nil {
		return err
	}
	if o.Verbose {
		for _, n := range g.Selected() {
			ident, _ := g.GoName(n)
			fmt.Printf("%-60s -> %s\n", n, ident)
		}
	}

	out, err := g.Run(extra...)
	if err != nil {
		return err
	}

	if o.Check {
		if err := out.Check(); err != nil {
			var drift *gogen.DriftError
			if errors.As(err, &drift) {
				return fmt.Errorf("generated output does not match the interface definitions; re-run rosidl-gen\n\t%s",
					strings.Join(drift.Problems(), "\n\t"))
			}
			return err
		}
		fmt.Printf("rosidl-gen: %d files in %s are up to date\n", len(out.Paths()), out.OutDir())
		return nil
	}

	stats, err := out.Write()
	if err != nil {
		return err
	}
	fmt.Printf("rosidl-gen: %d interfaces -> %d files in %s (%d stale removed)\n",
		stats.Interfaces, stats.Files, stats.OutDir, stats.Pruned)
	return nil
}
