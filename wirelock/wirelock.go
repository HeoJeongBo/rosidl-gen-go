// Package wirelock records and compares the CDR wire layout of generated types.
//
// A generator whose output is regenerated on every build has no fixed reference
// in it. Checking the generated Go against the .msg it came from, or against
// rosidl's own type descriptions, compares two derivations of one source: they
// move together and agree, whatever the source says. So nothing notices the
// DEFINITION ITSELF MOVING — an upstream package changing under a floating
// container tag, say, with no commit in the consumer's repository to blame.
//
// A lock is that missing reference. It records, per type, the ordered list of
// field shapes CDR actually reads: widths, fixed-array lengths, and nested type
// names. Field names, comments, constants, string bounds and sequence bounds do
// not appear, because none of them reaches the wire — so the everyday edits that
// make a byte-for-byte check unbearable pass a lock untouched, while a reorder
// or an inserted field does not.
//
// The package deliberately does NOT decide what to lock or when to fail. Which
// types matter is the consumer's policy — a generator knows the interfaces it
// was asked to emit, not the subset a program actually puts on a wire — and
// whether a given change is acceptable depends on which side deploys first,
// which no library can know.
//
// Typical use, from a test that owns both halves of that policy:
//
//	roots := []string{"friday_msgs/msg/MotorStateArray", "std_srvs/srv/Trigger"}
//	current, err := wirelock.Registry(GeneratedTypes).Closure(roots)
//	locked, err := wirelock.Parse(committed)
//	for _, c := range wirelock.Diff(locked, current) {
//		t.Errorf("%s: %s", c.Type, c.Detail)
//	}
package wirelock

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/HeoJeongBo/rosidl-gen-go/rosidl"
)

// Set is a lock: canonical ROS name to the ordered shapes of its fields.
type Set map[string][]string

// Registry is the generated-types table a consumer holds — canonical ROS name
// to a zero value of the generated Go struct. rosidl-gen emits exactly this
// shape as GeneratedTypes in registry.g.go, keyed with a service split into its
// _Request and _Response halves.
type Registry map[string]any

// primitives is the set of shapes that terminate a walk, derived from
// rosidl.Primitives rather than restated. Restating it is how a consumer's copy
// silently diverges the day the generator learns a new type — the exact drift
// class a lock exists to catch, reintroduced one level up.
var primitives = func() map[string]bool {
	m := make(map[string]bool, len(rosidl.Primitives))
	for _, goType := range rosidl.Primitives {
		m[goType] = true
	}
	return m
}()

// Shapes returns the wire shape of every exported field of one registered type,
// in declaration order — which is the order CDR encodes them in.
func (r Registry) Shapes(name string) ([]string, error) {
	v, ok := r[name]
	if !ok {
		return nil, fmt.Errorf("%s is not in the registry", name)
	}
	t := reflect.TypeOf(v)
	if t == nil || t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%s is registered as %T, not a struct", name, v)
	}

	names := r.rosNames()
	out := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue // cdr skips it too
		}
		out = append(out, shapeOf(f.Type, names))
	}
	return out, nil
}

// Closure returns the lock for everything reachable from roots: the roots
// themselves plus every type they nest, transitively. A root naming a service
// contributes both of its halves, since a service is carried as two messages.
//
// A reference it cannot resolve is an error, never a silently pruned branch. A
// walk that quietly stops short produces a lock that still looks complete and no
// longer guards what it dropped.
func (r Registry) Closure(roots []string) (Set, error) {
	queue := make([]string, 0, len(roots))
	for _, root := range roots {
		n, err := rosidl.ParseName(root, "")
		if err != nil {
			return nil, fmt.Errorf("root %q: %w", root, err)
		}
		if n.Kind == rosidl.KindSrv {
			queue = append(queue, root+"_Request", root+"_Response")
			continue
		}
		queue = append(queue, root)
	}

	out := Set{}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if _, done := out[name]; done {
			continue
		}

		shapes, err := r.Shapes(name)
		if err != nil {
			return nil, fmt.Errorf("reachable from a root: %w", err)
		}
		out[name] = shapes

		for _, s := range shapes {
			if nested, ok := Nested(s); ok {
				queue = append(queue, nested)
			}
		}
	}
	return out, nil
}

// rosNames inverts the registry: Go type identity to canonical ROS name, so a
// nested struct is recorded as the name its .msg gives it rather than an import
// path that means nothing on the wire.
func (r Registry) rosNames() map[string]string {
	m := make(map[string]string, len(r))
	for name, v := range r {
		if t := reflect.TypeOf(v); t != nil {
			m[t.PkgPath()+"."+t.Name()] = name
		}
	}
	return m
}

// shapeOf renders one field. Bounded and unbounded sequences collapse to the
// same form because they are identical on the wire; a fixed array's LENGTH does
// not, because it carries no length prefix — the declared size is the layout.
func shapeOf(t reflect.Type, names map[string]string) string {
	switch t.Kind() {
	case reflect.Array:
		return fmt.Sprintf("[%d]%s", t.Len(), elemName(t.Elem(), names))
	case reflect.Slice:
		return "[]" + elemName(t.Elem(), names)
	default:
		return elemName(t, names)
	}
}

// elemName maps a Go type to its .msg spelling: primitives by kind, structs by
// canonical ROS name. A struct the registry cannot name falls back to its import
// path, which no lock can match — so it surfaces as a resolution error rather
// than as a shape that quietly compares equal to nothing.
func elemName(t reflect.Type, names map[string]string) string {
	if t.PkgPath() == "" || t.Kind() != reflect.Struct {
		return t.Kind().String()
	}
	if n, ok := names[t.PkgPath()+"."+t.Name()]; ok {
		return n
	}
	return t.PkgPath() + "." + t.Name()
}

// Nested reports the type a shape refers to, if it refers to one.
//
// The test is "not a primitive", not "is a known type". Those differ on exactly
// the case that matters: a struct the registry could not name still looks like a
// reference, and answering "not nested" there would drop that whole branch of a
// closure while every check stayed green. Reporting it lets the caller fail on
// the unresolvable name instead.
func Nested(shape string) (string, bool) {
	base := strings.TrimPrefix(shape, "[]")
	if strings.HasPrefix(base, "[") {
		if i := strings.IndexByte(base, ']'); i > 0 {
			base = base[i+1:]
		}
	}
	if primitives[base] {
		return "", false
	}
	return base, true
}

// Change is one difference between two locks. Detail is prose for whoever has to
// decide whether a release needs coordinating; nothing branches on it.
type Change struct {
	Type   string
	Detail string
}

// Diff reports every difference between a committed lock and a current one,
// sorted by type name so a failure reads the same way twice.
func Diff(locked, current Set) []Change {
	var out []Change
	for _, name := range slices.Sorted(maps.Keys(locked)) {
		cur, ok := current[name]
		if !ok {
			out = append(out, Change{name, "left the locked set: nothing reaches it any more"})
			continue
		}
		if !slices.Equal(locked[name], cur) {
			out = append(out, Change{name, Classify(locked[name], cur)})
		}
	}
	for _, name := range slices.Sorted(maps.Keys(current)) {
		if _, ok := locked[name]; !ok {
			out = append(out, Change{name, "entered the locked set: " + strings.Join(current[name], ", ")})
		}
	}
	slices.SortFunc(out, func(a, b Change) int { return strings.Compare(a.Type, b.Type) })
	return out
}

// Classify describes how two shape vectors differ, in the terms a reader needs
// to judge wire compatibility. It never decides whether the change is safe: an
// append is harmless at the end of an outermost message and silently corrupting
// inside a nested type or a sequence element, and which one this is depends on
// how the type is used, not on how it changed.
func Classify(locked, current []string) string {
	switch {
	case len(locked) == len(current):
		var diffs []string
		for i := range locked {
			if locked[i] != current[i] {
				diffs = append(diffs, fmt.Sprintf("index %d %s -> %s", i, locked[i], current[i]))
			}
		}
		verb := "field type changed"
		if isPermutation(locked, current) {
			verb = "fields REORDERED, so values arrive swapped with no decode error"
		}
		return verb + " (" + strings.Join(diffs, "; ") + ")"

	case len(current) > len(locked):
		if slices.Equal(locked, current[:len(locked)]) {
			return fmt.Sprintf("%d field(s) APPENDED (%s) — safe only at the end of an outermost message",
				len(current)-len(locked), strings.Join(current[len(locked):], ", "))
		}
		i := firstDiff(locked, current)
		return fmt.Sprintf("field INSERTED at index %d (%s), shifting everything after it", i, current[i])

	default:
		if slices.Equal(current, locked[:len(current)]) {
			return fmt.Sprintf("%d trailing field(s) REMOVED (%s)",
				len(locked)-len(current), strings.Join(locked[len(current):], ", "))
		}
		i := firstDiff(locked, current)
		return fmt.Sprintf("field REMOVED at index %d (%s), shifting everything after it", i, locked[i])
	}
}

func firstDiff(a, b []string) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

func isPermutation(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

// Format renders a lock for committing: a header, then one type per line, sorted,
// fields comma-separated.
//
// One line per type is what makes a mid-struct insertion read as a single changed
// line in review. A nested type is recorded by name rather than expanded, so a
// change deep in the tree shows on that type's own line instead of rewriting
// every line that reaches it.
func (s Set) Format() []byte {
	var b strings.Builder
	b.WriteString("# CDR wire layout, one type per line. Field order is the wire format.\n")
	b.WriteString("# A diff here is a deployment question, not a formatting one.\n")
	for _, name := range slices.Sorted(maps.Keys(s)) {
		fmt.Fprintf(&b, "%s: %s\n", name, strings.Join(s[name], ", "))
	}
	return []byte(b.String())
}

// Parse reads a lock back. A line it cannot read is an error rather than a skip:
// dropping one would remove that type from the comparison and leave the check
// green about it.
func Parse(b []byte) (Set, error) {
	out := Set{}
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, fields, ok := strings.Cut(line, ": ")
		if !ok {
			return nil, fmt.Errorf("line %d: no `name: fields` separator: %q", i+1, line)
		}
		shapes := strings.Split(fields, ", ")
		for j := range shapes {
			shapes[j] = strings.TrimSpace(shapes[j])
		}
		out[name] = shapes
	}
	return out, nil
}
