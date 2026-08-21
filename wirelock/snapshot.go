package wirelock

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// LockFormat is the version of the on-disk lock this package writes and is
// willing to read.
//
// It is checked rather than inferred. An older lock is missing whole categories
// — a format-1 file has no constants at all — and reading one leniently would
// compare zero locked constants against every current one, call them all
// additions, and report nothing until somebody happened to regenerate. A lock
// that silently checks less than it claims is worse than no lock.
const LockFormat = 2

// Constant is a `<type> NAME=<value>` declaration. Value is the literal text
// from the .msg, unparsed: what matters is whether it changed, and comparing the
// source text answers that without needing to know the type.
type Constant struct {
	Name  string
	Value string
}

// Snapshot is everything a lock records: the field layout of each interface,
// and the constants each one declares.
//
// Constants are not on the wire. They are here because nothing else can see
// them change — the compiler cannot (both sides still compile), the .msg
// comparison cannot (it compares layout), and rosidl's type descriptions do not
// carry them. Meanwhile the agent and the robot are built at different times
// from the same declaration, so a value that moves between those builds is the
// same silent divergence a reordered field is.
type Snapshot struct {
	Fields Layout
	Consts map[string][]Constant
}

// Format renders a snapshot for committing. generator names the tool that wrote
// it — a comment, ignored on read, there so that a diff which touches only the
// header reads as "the generator changed how it spells things" rather than as
// hundreds of wire changes.
func (s Snapshot) Format(generator string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# lock-format: %d\n", LockFormat)
	fmt.Fprintf(&b, "# generator: %s\n", generator)
	b.WriteString("#\n")
	b.WriteString("# The CDR wire layout of every generated interface, and the constants they\n")
	b.WriteString("# declare. One line per type as `field shape`, then one line per constant.\n")
	b.WriteString("#\n")
	b.WriteString("# Field ORDER is the wire format. Field NAMES are recorded because swapping\n")
	b.WriteString("# two same-typed fields changes no shape and would otherwise be invisible.\n")
	b.WriteString("# CONSTANTS are recorded because they never reach the wire, so nothing else\n")
	b.WriteString("# can see a value change between two builds that must agree.\n")
	b.WriteString("#\n")
	b.WriteString("# Comments and docs are absent by design: they cannot break anything, and a\n")
	b.WriteString("# check that fails on them gets switched off.\n")

	for _, name := range slices.Sorted(maps.Keys(s.Fields)) {
		fmt.Fprintf(&b, "%s: %s\n", name, joinFields(s.Fields[name]))
		// A constant's value can be a quoted string containing commas, so one
		// per line rather than a comma-separated tail. It also makes a changed
		// value exactly one line of diff.
		for _, c := range s.Consts[name] {
			fmt.Fprintf(&b, "const %s.%s = %s\n", name, c.Name, c.Value)
		}
	}
	return []byte(b.String())
}

// ParseSnapshot reads a committed lock. A line it cannot read is an error rather
// than a skip: dropping one would remove that type from the comparison and leave
// the check green about it.
func ParseSnapshot(b []byte) (Snapshot, error) {
	out := Snapshot{Fields: Layout{}, Consts: map[string][]Constant{}}
	format := 0

	for i, line := range strings.Split(string(b), "\n") {
		n := i + 1
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if k, v, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(line, "#")), ": "); ok && k == "lock-format" {
				if _, err := fmt.Sscanf(v, "%d", &format); err != nil {
					return Snapshot{}, fmt.Errorf("line %d: unreadable lock-format %q", n, v)
				}
			}
			continue
		}

		if rest, ok := strings.CutPrefix(line, "const "); ok {
			key, value, ok := strings.Cut(rest, " = ")
			if !ok {
				return Snapshot{}, fmt.Errorf("line %d: no ` = ` in constant %q", n, rest)
			}
			typeName, constName, ok := cutLast(key, ".")
			if !ok {
				return Snapshot{}, fmt.Errorf("line %d: constant %q is not `type.NAME`", n, key)
			}
			out.Consts[typeName] = append(out.Consts[typeName], Constant{Name: constName, Value: value})
			continue
		}

		// Cut on the bare colon, not on ": ". Neither a ROS name nor a shape
		// contains one, and a type with no fields renders as `name:` with
		// nothing after it — a form ": " cannot match.
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			return Snapshot{}, fmt.Errorf("line %d: no `name: fields` separator: %q", n, line)
		}
		fields, err := parseFields(strings.TrimSpace(rest))
		if err != nil {
			return Snapshot{}, fmt.Errorf("line %d: %w", n, err)
		}
		out.Fields[name] = fields
	}

	if format != LockFormat {
		return Snapshot{}, fmt.Errorf(
			"this lock is format %d, but this generator writes format %d — "+
				"regenerate it (`rosidl-gen wirelock`) and commit the result; "+
				"reading it as-is would check less than it looks like it checks", format, LockFormat)
	}
	return out, nil
}

// parseFields reads the `name shape, name shape` tail of a type line. An empty
// tail is legal and means a type with no fields, which the rosidl parser does
// not currently produce — it substitutes a dummy member — but which costs
// nothing to round-trip correctly.
func parseFields(rest string) ([]Field, error) {
	if strings.TrimSpace(rest) == "" {
		return nil, nil
	}
	var fields []Field
	for _, part := range strings.Split(rest, ", ") {
		part = strings.TrimSpace(part)
		name, shape, ok := strings.Cut(part, " ")
		if !ok {
			return nil, fmt.Errorf("%q is a bare shape, so this lock predates field names", part)
		}
		fields = append(fields, Field{Name: name, Shape: shape})
	}
	return fields, nil
}

func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// DiffSnapshot reports every difference between a committed lock and a current
// one, sorted so a failure reads the same way twice.
//
// Every difference is reported, including the ones no deployment has to care
// about — see [SeverityStale] for why a lock that is allowed to drift stops
// working. What differs between the two severities is what the reader has to do,
// not whether they hear about it.
func DiffSnapshot(locked, current Snapshot) []Change {
	var out []Change

	for _, name := range slices.Sorted(maps.Keys(locked.Fields)) {
		cur, ok := current.Fields[name]
		if !ok {
			out = append(out, Change{
				Type:     name,
				Detail:   "left the generated set: nothing generates it any more",
				Severity: SeverityStale,
			})
			continue
		}
		lf := locked.Fields[name]
		if d := ClassifyLayout(lf, cur); d != "" {
			out = append(out, Change{Type: name, Detail: d, Severity: SeverityBreaking})
		} else if !slices.Equal(lf, cur) {
			out = append(out, Change{Type: name, Detail: renamed(lf, cur), Severity: SeverityStale})
		}
	}
	for _, name := range slices.Sorted(maps.Keys(current.Fields)) {
		if _, ok := locked.Fields[name]; !ok {
			out = append(out, Change{
				Type:     name,
				Detail:   "entered the generated set: " + joinFields(current.Fields[name]),
				Severity: SeverityStale,
			})
		}
	}

	out = append(out, diffConsts(locked, current)...)

	slices.SortFunc(out, func(a, b Change) int {
		if c := strings.Compare(a.Type, b.Type); c != 0 {
			return c
		}
		if a.Severity != b.Severity {
			return int(a.Severity) - int(b.Severity)
		}
		return strings.Compare(a.Detail, b.Detail)
	})
	return out
}

// renamed describes a difference that ClassifyLayout already found harmless, so
// the vectors are the same length and every shape matches.
func renamed(locked, current []Field) string {
	var diffs []string
	for i := range locked {
		if locked[i].Name != current[i].Name {
			diffs = append(diffs, fmt.Sprintf("index %d %s -> %s", i, locked[i].Name, current[i].Name))
		}
	}
	return "field(s) renamed (" + strings.Join(diffs, "; ") + "), which no decoder can see"
}

// diffConsts compares the constants of types present on BOTH sides. A type that
// entered or left is already one report; repeating it once per constant would
// bury the line that matters.
func diffConsts(locked, current Snapshot) []Change {
	var out []Change
	for _, name := range slices.Sorted(maps.Keys(locked.Fields)) {
		if _, ok := current.Fields[name]; !ok {
			continue
		}
		l, c := byName(locked.Consts[name]), byName(current.Consts[name])

		for _, k := range slices.Sorted(maps.Keys(l)) {
			cur, ok := c[k]
			if !ok {
				// Removal is not a deployment question: whatever the sender
				// already ships keeps its value, and any consumer that named
				// this constant now fails to compile.
				out = append(out, Change{
					Type:     name,
					Detail:   fmt.Sprintf("constant %s removed (was %s)", k, l[k]),
					Severity: SeverityStale,
				})
				continue
			}
			if cur != l[k] {
				out = append(out, Change{
					Type: name,
					Detail: fmt.Sprintf(
						"constant %s changed %s -> %s — not on the wire, but a peer built "+
							"before this change still sends the old value", k, l[k], cur),
					Severity: SeverityBreaking,
				})
			}
		}
		for _, k := range slices.Sorted(maps.Keys(c)) {
			if _, ok := l[k]; !ok {
				out = append(out, Change{
					Type:     name,
					Detail:   fmt.Sprintf("constant %s added (%s)", k, c[k]),
					Severity: SeverityStale,
				})
			}
		}
	}
	return out
}

func byName(cs []Constant) map[string]string {
	m := make(map[string]string, len(cs))
	for _, c := range cs {
		m[c.Name] = c.Value
	}
	return m
}
