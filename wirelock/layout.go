package wirelock

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Field is one field of a message: the name it has in the .msg, and its shape in
// the vocabulary [Registry.Shapes] and shapeOfType both produce.
//
// The name is not on the wire — CDR carries no field names and no per-field tags
// — so it can never be the reason a change is breaking. It is recorded because
// without it a lock cannot tell two cases apart that a shape vector spells
// identically:
//
//	position float64, velocity float64   ->   velocity float64, position float64
//
// That swap changes nothing about the bytes and everything about what they mean.
// It compiles, every structural check passes, and the receiver decodes velocity
// into position forever. Shapes alone cannot see it; names make it a permutation.
type Field struct {
	Name  string
	Shape string
}

func (f Field) String() string { return f.Name + " " + f.Shape }

// Layout is a lock keyed by canonical ROS name, with a service split into its two
// halves. It is [Set] plus the field names.
type Layout map[string][]Field

// Shapes drops the names, giving the vector [Classify] compares.
func (l Layout) Shapes(name string) []string { return shapesOf(l[name]) }

// Set discards the names. It exists so a caller holding a Layout can still use
// the shape-only API.
func (l Layout) Set() Set {
	out := make(Set, len(l))
	for name := range l {
		out[name] = l.Shapes(name)
	}
	return out
}

func names(fields []Field) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.Name
	}
	return out
}

// DiffLayout reports every difference between a committed lock and a current
// one, sorted by type name so a failure reads the same way twice.
func DiffLayout(locked, current Layout) []Change {
	var out []Change
	for _, name := range slices.Sorted(maps.Keys(locked)) {
		cur, ok := current[name]
		if !ok {
			out = append(out, Change{Type: name, Detail: "left the locked set: nothing reaches it any more"})
			continue
		}
		if d := ClassifyLayout(locked[name], cur); d != "" {
			out = append(out, Change{Type: name, Detail: d})
		}
	}
	for _, name := range slices.Sorted(maps.Keys(current)) {
		if _, ok := locked[name]; !ok {
			out = append(out, Change{Type: name, Detail: "entered the locked set: " + joinFields(current[name])})
		}
	}
	slices.SortFunc(out, func(a, b Change) int { return strings.Compare(a.Type, b.Type) })
	return out
}

// ClassifyLayout describes how two field vectors differ, or returns "" when the
// difference does not reach the wire.
//
// Shapes decide first, and when they differ this defers entirely to [Classify]:
// a change that moves bytes is described the same way whether or not names are
// recorded. Names are consulted only for the case shapes cannot express — the
// same types in a different order.
//
// A pure RENAME returns "": the wire is untouched, and failing on it is what
// makes people switch a check off.
//
// The two are told apart by asking, of every name that appears on BOTH sides,
// whether it still sits at the same index. A rename makes a name vanish and
// another appear; a reorder makes a surviving name move. Asking instead whether
// the names are a permutation is not enough, and the gap is reachable: rename a
// field in one commit, leave the lock stale, then swap it with its neighbour in
// the next. The result is neither a permutation nor harmless.
//
// Ambiguous cases resolve toward "moved". When the locked names are `a, b` and
// the current ones are `b, c`, no record can say whether b slid down a slot or
// two fields were renamed past each other — and only one of those readings is
// safe to be wrong about.
func ClassifyLayout(locked, current []Field) string {
	lockedShapes, currentShapes := shapesOf(locked), shapesOf(current)
	if !slices.Equal(lockedShapes, currentShapes) {
		return Classify(lockedShapes, currentShapes)
	}

	lockedNames, currentNames := names(locked), names(current)
	if slices.Equal(lockedNames, currentNames) {
		return ""
	}

	moved := movedNames(lockedNames, currentNames)
	if len(moved) == 0 {
		return "" // renamed, not moved
	}
	return "fields REORDERED, so values arrive swapped with no decode error (" +
		strings.Join(moved, "; ") + ") — the types are identical, so nothing but the names records it"
}

// movedNames describes every name present in both vectors that changed index.
func movedNames(locked, current []string) []string {
	at := make(map[string]int, len(current))
	for i, n := range current {
		at[n] = i
	}
	var out []string
	for i, n := range locked {
		if j, ok := at[n]; ok && j != i {
			out = append(out, fmt.Sprintf("%s: index %d -> %d", n, i, j))
		}
	}
	return out
}

func shapesOf(fields []Field) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.Shape
	}
	return out
}

func joinFields(fields []Field) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = f.String()
	}
	return strings.Join(parts, ", ")
}

// Format renders a lock for committing: a header, then one type per line, sorted,
// fields comma-separated as `name shape`.
//
// One line per type is what makes a mid-struct insertion read as a single changed
// line in review. A nested type is recorded by name rather than expanded, so a
// change deep in the tree shows on that type's own line instead of rewriting
// every line that reaches it.
func (l Layout) Format() []byte {
	var b strings.Builder
	b.WriteString("# CDR wire layout, one type per line, as `field shape`. Field ORDER is the\n")
	b.WriteString("# wire format; the names are here only so a swap of two same-typed fields\n")
	b.WriteString("# is visible, since it changes no shape. A diff here is a deployment\n")
	b.WriteString("# question, not a formatting one.\n")
	for _, name := range slices.Sorted(maps.Keys(l)) {
		fmt.Fprintf(&b, "%s: %s\n", name, joinFields(l[name]))
	}
	return []byte(b.String())
}

// ParseLayout reads a lock back. A line it cannot read is an error rather than a
// skip: dropping one would remove that type from the comparison and leave the
// check green about it.
//
// A lock written before names were recorded fails here by design. Reading it as
// if the shapes were names would compare nonsense to nonsense and report no
// drift, which is the one outcome a lock must never produce by accident.
func ParseLayout(b []byte) (Layout, error) {
	out := Layout{}
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rest, ok := strings.Cut(line, ": ")
		if !ok {
			return nil, fmt.Errorf("line %d: no `name: fields` separator: %q", i+1, line)
		}
		var fields []Field
		for _, part := range strings.Split(rest, ", ") {
			part = strings.TrimSpace(part)
			fname, shape, ok := strings.Cut(part, " ")
			if !ok {
				return nil, fmt.Errorf(
					"line %d: %q is a bare shape, so this lock predates field names; "+
						"regenerate it (`rosidl-gen wirelock`) and commit the result", i+1, part)
			}
			fields = append(fields, Field{Name: fname, Shape: shape})
		}
		out[name] = fields
	}
	return out, nil
}
