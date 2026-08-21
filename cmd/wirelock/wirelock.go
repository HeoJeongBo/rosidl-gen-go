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
	// Out receives progress; errors are returned, not printed.
	Out io.Writer
}

func (o Options) out() io.Writer {
	if o.Out == nil {
		return os.Stdout
	}
	return o.Out
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
	current, err := wirelock.Compute(g)
	if err != nil {
		return err
	}

	if !o.Check {
		if err := lock.Write(current); err != nil {
			return err
		}
		fmt.Fprintf(o.out(), "rosidl-gen: %d types -> %s\n", len(current), lock.Path())
		return nil
	}

	changes, err := lock.Check(current)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		fmt.Fprintf(o.out(), "rosidl-gen: %d types match %s\n", len(current), lock.Path())
		return nil
	}

	// The markers exist so a consumer's CI can lift this block out of a build
	// log. Anything that runs the generator inside a container relays its output
	// through something that prefixes every line, and prefixed output cannot be
	// parsed as a CI annotation.
	var b strings.Builder
	b.WriteString("WIRE-LOCK-BEGIN\n")
	fmt.Fprintf(&b, "the CDR wire layout changed against %s:\n", lock.Path())
	for _, c := range changes {
		fmt.Fprintf(&b, "  %s: %s\n", c.Type, c.Detail)
	}
	b.WriteString("\n")
	b.WriteString("Field order IS the wire format, so this is a compatibility question, not a\n")
	b.WriteString("formatting one. Unless every change above is a field appended at the end of an\n")
	b.WriteString("OUTERMOST message, both sides of the wire must be deployed together.\n")
	b.WriteString("If that is intended, re-run `rosidl-gen wirelock` and commit the lock.\n")
	b.WriteString("WIRE-LOCK-END")

	return &Drift{Changes: changes, text: b.String()}
}
