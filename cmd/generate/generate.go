// Package generate holds the config-driven operations behind the CLI: emit
// the output, verify it, list what the search paths contain, and explain why
// an interface was selected.
package generate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/HeoJeongBo/rosidl-gen-go/cppnames"
	"github.com/HeoJeongBo/rosidl-gen-go/gogen"
	"github.com/HeoJeongBo/rosidl-gen-go/rosidl"
	"github.com/HeoJeongBo/rosidl-gen-go/wirelock"
)

// Options mirrors the CLI flags.
type Options struct {
	// ConfigPath locates the rosidl-gen.yaml document.
	ConfigPath string
	// Verbose reports the resolved search paths and interface set.
	Verbose bool
	// Check verifies the on-disk output byte-for-byte and writes nothing.
	Check bool
	// DryRun reports what a write would do, and writes nothing.
	DryRun bool
	// Out receives progress; errors are returned, not printed.
	Out io.Writer
}

func (o Options) out() io.Writer {
	if o.Out == nil {
		return os.Stdout
	}
	return o.Out
}

// load reads the config and registers the built-in emitters that claim a
// section in it.
//
// strict rejects any section no emitter took, because when output is about to
// be produced an unclaimed section means an emitter silently did not run. The
// read-only commands pass strict=false: a config written for a program that
// registers its own emitters still describes an interface set worth listing,
// and refusing to read it would make those commands useless exactly where they
// help most.
func load(path string, strict bool) (*gogen.Config, []gogen.Emitter, error) {
	cfg, err := gogen.LoadConfig(path)
	if err != nil {
		return nil, nil, err
	}

	var extra []gogen.Emitter
	if names, ok, err := cppnames.FromConfig(cfg); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	} else if ok {
		extra = append(extra, names)
	}
	// Claimed, never acted on. The lock belongs to `rosidl-gen wirelock`: a lock
	// rewritten by every generation records whatever the definitions now say
	// rather than what someone decided to accept. Claiming it here keeps strict
	// mode from rejecting a config that declares one, and validates the section
	// at generation time so a bad `out` surfaces now instead of at check time.
	if _, _, err := wirelock.FromConfig(cfg); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if unknown := cfg.UnclaimedSections(); strict && len(unknown) > 0 {
		return nil, nil, fmt.Errorf("%s: unknown config section(s): %s\n\tknown sections: %s",
			path, strings.Join(unknown, ", "), strings.Join(gogen.KnownSections(), ", "))
	}
	return cfg, extra, nil
}

// resolve builds a Generator and expands the interface set, reporting the
// search-path resolution first when asked. That ordering matters: a dropped
// search path is the usual cause of a pattern matching nothing, and this is
// where the user can see it.
func resolve(cfg *gogen.Config, o Options) (*gogen.Generator, error) {
	if o.Verbose {
		printSearchPaths(o.out(), cfg)
	}
	g, err := gogen.New(cfg)
	if err != nil {
		return nil, err
	}
	if err := g.Resolve(); err != nil {
		return nil, err
	}
	return g, nil
}

func printSearchPaths(w io.Writer, cfg *gogen.Config) {
	paths, dropped := cfg.SearchPathReport()
	for _, p := range paths {
		fmt.Fprintf(w, "search path: %s\n", p)
	}
	for _, d := range dropped {
		fmt.Fprintf(w, "search path: %s (skipped: %s)\n", d.Path, d.Reason)
	}
}

// Run executes one generation according to o.
func Run(o Options) error {
	cfg, extra, err := load(o.ConfigPath, true)
	if err != nil {
		return err
	}
	g, err := resolve(cfg, o)
	if err != nil {
		return err
	}
	if o.Verbose {
		for _, n := range g.Selected() {
			ident, _ := g.GoName(n)
			fmt.Fprintf(o.out(), "%-60s -> %s\n", n, ident)
		}
	}

	out, err := g.Run(extra...)
	if err != nil {
		return err
	}

	switch {
	case o.Check:
		return check(out, o)
	case o.DryRun:
		return dryRun(out, o)
	default:
		stats, err := out.Write()
		if err != nil {
			return err
		}
		fmt.Fprintf(o.out(), "rosidl-gen: %d interfaces -> %d files in %s (%d stale removed)\n",
			stats.Interfaces, stats.Files, stats.OutDir, stats.Pruned)
		return nil
	}
}

// StaleOutput is returned by a failing check. It presents the human-facing
// report as its message while still unwrapping to the [gogen.DriftError], so a
// caller can tell stale output apart from a broken config — which is what the
// CLI's distinct exit code rests on.
type StaleOutput struct {
	Drift *gogen.DriftError
	text  string
}

func (e *StaleOutput) Error() string { return e.text }
func (e *StaleOutput) Unwrap() error { return e.Drift }

func check(out *gogen.Output, o Options) error {
	err := out.Check()
	var drift *gogen.DriftError
	if errors.As(err, &drift) {
		return &StaleOutput{
			Drift: drift,
			text: fmt.Sprintf("generated output does not match the interface definitions; re-run rosidl-gen\n%s",
				report(out, drift)),
		}
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(o.out(), "rosidl-gen: %d files are up to date\n", len(out.Paths()))
	return nil
}

// report renders each drifted path, and for an outdated one the lines that
// differ — the whole point of the check is to tell a comment reflow apart from
// a field reorder.
func report(out *gogen.Output, drift *gogen.DriftError) string {
	var b strings.Builder
	want := out.Files()
	for _, p := range drift.Missing {
		fmt.Fprintf(&b, "\tmissing:  %s\n", p)
	}
	for _, p := range drift.Outdated {
		fmt.Fprintf(&b, "\toutdated: %s\n", p)
		got, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		b.WriteString(diffLines(got, want[p]))
	}
	for _, p := range drift.Stale {
		fmt.Fprintf(&b, "\tstale:    %s\n", p)
	}
	return b.String()
}

func dryRun(out *gogen.Output, o Options) error {
	for _, p := range out.Paths() {
		fmt.Fprintf(o.out(), "write: %s\n", p)
	}
	stale, err := out.Stale()
	if err != nil {
		return err
	}
	for _, p := range stale {
		fmt.Fprintf(o.out(), "remove: %s\n", p)
	}
	fmt.Fprintf(o.out(), "rosidl-gen: %d files would be written, %d removed (nothing was changed)\n",
		len(out.Paths()), len(stale))
	return nil
}

// List reports every interface the search paths contain, which is what you
// need in order to write a `generate` pattern. Selected interfaces are marked.
//
// The search paths are always reported here, not only under -v: when the list
// comes back empty, the paths are the answer.
func List(o Options) error {
	cfg, _, err := load(o.ConfigPath, false)
	if err != nil {
		return err
	}
	printSearchPaths(o.out(), cfg)

	g, err := gogen.New(cfg)
	if err != nil {
		return err
	}
	// A `generate` pattern that matches nothing must not stop listing —
	// listing is how you find out what to write instead. Resolution failing
	// only costs the selection markers.
	selected := map[rosidl.Name]bool{}
	if err := g.Resolve(); err == nil {
		for _, n := range g.Selected() {
			selected[n] = true
		}
	} else {
		fmt.Fprintf(o.out(), "note: could not resolve the selection (%v)\n", err)
	}

	names := g.Index().Names()
	sort.Slice(names, func(i, j int) bool { return names[i].String() < names[j].String() })
	for _, n := range names {
		mark := " "
		if selected[n] {
			mark = "*"
		}
		fmt.Fprintf(o.out(), "%s %s\n", mark, n)
	}
	fmt.Fprintf(o.out(), "\n%d interfaces found, %d selected (*)\n", len(names), len(selected))
	return nil
}

// Explain traces why an interface is in the output: the chain of field
// references back to the `generate` pattern that asked for it.
func Explain(o Options, target string) error {
	cfg, _, err := load(o.ConfigPath, false)
	if err != nil {
		return err
	}
	g, err := resolve(cfg, o)
	if err != nil {
		return err
	}

	n, err := rosidl.ParseName(target, "")
	if err != nil {
		return err
	}
	if _, ok := g.Provenance(n); !ok {
		return fmt.Errorf("%s is not in the resolved set; `list` shows what is", n)
	}

	// Walk parent links to the pattern. The set is finite and every step moves
	// closer to a pattern, but guard anyway rather than trust that.
	seen := map[rosidl.Name]bool{}
	for cur := n; ; {
		if seen[cur] {
			return fmt.Errorf("provenance for %s is cyclic", n)
		}
		seen[cur] = true

		p, ok := g.Provenance(cur)
		if !ok {
			return nil
		}
		if p.Pattern != "" {
			fmt.Fprintf(o.out(), "%s\n\tmatched by `generate` pattern %q\n", cur, p.Pattern)
			return nil
		}
		fmt.Fprintf(o.out(), "%s\n\treferenced by field %q of %s\n", cur, p.Field, p.Parent)
		cur = p.Parent
	}
}
