package wirelock

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/HeoJeongBo/rosidl-gen-go/gogen"
	"github.com/HeoJeongBo/rosidl-gen-go/rosidl"
)

// Config is the `wirelock:` section of the generator config.
//
//	wirelock:
//	  out: internal/hros/testdata/wire-lock.txt   # config-relative
//
// The section is optional. A consumer that does not want a lock simply omits
// it, and every command behaves as it did before the feature existed.
type Config struct {
	// Out is the lock file, relative to the config file. It is meant to be
	// COMMITTED: the lock's whole value is being a reference that does not move
	// when the definitions do.
	Out string `yaml:"out"`
}

// Lock is a configured lock file.
type Lock struct {
	path string
}

// FromConfig claims the `wirelock:` section of cfg. ok is false when the config
// has no such section, in which case the caller should do nothing.
//
// Claiming matters even for a caller that will not use the result: the generate
// command rejects any section no one took, so a config declaring a lock would
// otherwise fail to generate at all.
func FromConfig(cfg *gogen.Config) (l *Lock, ok bool, err error) {
	var c Config
	ok, err = cfg.Section("wirelock", &c)
	if !ok || err != nil {
		return nil, ok, err
	}
	if c.Out == "" {
		return nil, true, fmt.Errorf("wirelock: `out` is required")
	}
	return &Lock{path: cfg.Path(c.Out)}, true, nil
}

// Path is the resolved lock file.
func (l *Lock) Path() string { return l.path }

// Write renders set to the lock file, creating parent directories.
//
// This is a deliberate act, not a side effect of generating: a lock rewritten
// by every regeneration would record whatever the sources now say instead of
// what someone decided to accept.
func (l *Lock) Write(set Set) error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(l.path, set.Format(), 0o644)
}

// Load reads the committed lock.
func (l *Lock) Load() (Set, error) {
	b, err := os.ReadFile(l.path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Check compares the committed lock against current and returns the
// differences, or a [*DriftError] if the file cannot be read at all.
func (l *Lock) Check(current Set) ([]Change, error) {
	locked, err := l.Load()
	if err != nil {
		return nil, err
	}
	return Diff(locked, current), nil
}

// Compute renders the wire layout of every interface a resolved generator
// selected, keyed by canonical ROS name with a service split into its two
// halves.
//
// It reads the definitions, not the emitted Go, so it needs nothing but the
// generator that already resolved them. Externally bound interfaces are absent
// from the selection by design and so are absent here — but a field that
// REFERENCES one still records that reference by name, because the field's
// position and the type it names are both on the wire.
//
// Resolve must already have run.
func Compute(g *gogen.Generator) (Set, error) {
	out := Set{}
	for _, n := range g.Selected() {
		msgs, err := g.Index().Messages(n)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		for _, m := range msgs {
			shapes := make([]string, 0, len(m.Fields))
			for _, f := range m.Fields {
				shapes = append(shapes, shapeOfType(f.Type))
			}
			out[m.Name.String()] = shapes
		}
	}
	return out, nil
}

// shapeOfType renders one .msg field in the same vocabulary Registry.Shapes
// produces from a Go struct. The two paths must agree — a lock written from one
// and checked against the other would report drift that is not there.
//
// Bounded and unbounded sequences collapse to the same form because they are
// identical on the wire; a fixed array's LENGTH does not, because it carries no
// length prefix. A string's bound is dropped for the same reason a sequence's
// is: the value carries its own length.
func shapeOfType(t rosidl.Type) string {
	base := t.Primitive
	if base == "" {
		base = t.Nested.String()
	} else if goType, ok := rosidl.Primitives[base]; ok {
		// byte and char are spellings of uint8; normalise the way the Go side
		// necessarily does, so the two vocabularies match.
		base = goType
	}

	switch t.Array {
	case rosidl.ArrayFixed:
		return fmt.Sprintf("[%d]%s", t.ArraySize, base)
	case rosidl.ArrayBounded, rosidl.ArrayDynamic:
		return "[]" + base
	default:
		return base
	}
}
