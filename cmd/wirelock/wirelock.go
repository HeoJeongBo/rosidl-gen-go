// Package wirelock holds the command behind `rosidl-gen wirelock`: write the
// CDR wire layout of the configured interfaces, or verify the committed one.
//
// It is a separate command from `generate` on purpose. A lock rewritten by
// every regeneration records whatever the definitions now say, which is not the
// same as what someone decided to accept — and accepting a wire change is a
// release-coordination decision, not a side effect of running a code generator.
package wirelock

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/HeoJeongBo/rosidl-gen-go/cppnames"
	"github.com/HeoJeongBo/rosidl-gen-go/gogen"
	"github.com/HeoJeongBo/rosidl-gen-go/wirelock"
)

// Options mirrors the CLI flags.
type Options struct {
	// ConfigPath locates the rosidl-gen.yaml document.
	ConfigPath string
	// Check verifies the committed lock and writes nothing.
	Check bool
	// Generator names the tool writing the lock, for its header comment. It is
	// recorded so that a diff touching only the header reads as "the generator
	// changed how it spells things" instead of as hundreds of wire changes.
	Generator string
	// Out receives progress; errors are returned, not printed.
	Out io.Writer
}

func (o Options) out() io.Writer {
	if o.Out == nil {
		return os.Stdout
	}
	return o.Out
}

func (o Options) generator() string {
	if o.Generator == "" {
		return "rosidl-gen-go"
	}
	return o.Generator
}

// Drift is returned by a failing check. It carries the changes so a caller can
// tell a moved wire apart from a broken config, which is what the CLI's
// distinct exit code rests on.
type Drift struct {
	Changes []wirelock.Change
	text    string
}

func (e *Drift) Error() string { return e.text }

// Run writes or verifies the lock according to o.
func Run(o Options) error {
	cfg, err := gogen.LoadConfig(o.ConfigPath)
	if err != nil {
		return err
	}
	// cppnames claims its own section so an unrelated `names:` does not read as
	// unknown here; this command does not otherwise care about it.
	if _, _, err := cppnames.FromConfig(cfg); err != nil {
		return fmt.Errorf("%s: %w", o.ConfigPath, err)
	}

	lock, ok, err := wirelock.FromConfig(cfg)
	if err != nil {
		return fmt.Errorf("%s: %w", o.ConfigPath, err)
	}
	if !ok {
		return fmt.Errorf("%s: no `wirelock:` section to act on", o.ConfigPath)
	}

	g, err := gogen.New(cfg)
	if err != nil {
		return err
	}
	if err := g.Resolve(); err != nil {
		return err
	}
	current, err := wirelock.ComputeSnapshot(g)
	if err != nil {
		return err
	}

	if !o.Check {
		if err := lock.WriteSnapshot(current, o.generator()); err != nil {
			return err
		}
		fmt.Fprintf(o.out(), "rosidl-gen: %d types -> %s\n", len(current.Fields), lock.Path())
		return nil
	}

	changes, err := lock.CheckSnapshot(current)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		fmt.Fprintf(o.out(), "rosidl-gen: %d types match %s\n", len(current.Fields), lock.Path())
		return nil
	}

	return &Drift{Changes: changes, text: report(lock.Path(), changes)}
}

// report renders the drift for a human, split by what it asks of them.
//
// The markers exist so a consumer's CI can lift the block out of a build log.
// Anything that runs the generator inside a container relays its output through
// something that prefixes every line, and prefixed output cannot be parsed as a
// CI annotation.
func report(path string, changes []wirelock.Change) string {
	var breaking, stale []wirelock.Change
	for _, c := range changes {
		if c.Severity == wirelock.SeverityStale {
			stale = append(stale, c)
			continue
		}
		breaking = append(breaking, c)
	}

	var b strings.Builder
	b.WriteString("WIRE-LOCK-BEGIN\n")
	fmt.Fprintf(&b, "the generated interfaces no longer match %s\n", path)

	if len(breaking) > 0 {
		b.WriteString("\nDEPLOY TOGETHER — a peer built before this change reads it differently.\n")
		for _, c := range breaking {
			fmt.Fprintf(&b, "  %s: %s\n", c.Type, c.Detail)
		}
		b.WriteString("\n")
		b.WriteString("  Field order IS the wire format: CDR carries no names and no tags, so a\n")
		b.WriteString("  reordered or resized field decodes into the wrong place with no error\n")
		b.WriteString("  anywhere. Unless every line above is a field appended at the end of an\n")
		b.WriteString("  OUTERMOST message, both sides have to ship as a pair.\n")
	}

	if len(stale) > 0 {
		b.WriteString("\nLOCK STALE — nothing that reaches the wire changed.\n")
		for _, c := range stale {
			fmt.Fprintf(&b, "  %s: %s\n", c.Type, c.Detail)
		}
		b.WriteString("\n")
		b.WriteString("  No release coordination is needed for these. They still fail because a\n")
		b.WriteString("  lock left stale stops working: once it records a name no field carries\n")
		b.WriteString("  any more, a later reorder of that field matches nothing and is never\n")
		b.WriteString("  reported.\n")
	}

	b.WriteString("\nRe-run `rosidl-gen wirelock` and commit the lock with the change.\n")
	b.WriteString("WIRE-LOCK-END")
	return b.String()
}
