package rosidl

import (
	"fmt"
	"strconv"
	"strings"
)

// ArrayKind describes the array shape of a field.
type ArrayKind int

const (
	// ArrayNone is a scalar field.
	ArrayNone ArrayKind = iota
	// ArrayFixed is `T[N]` — a fixed-size array, no length prefix on the wire.
	ArrayFixed
	// ArrayBounded is `T[<=N]` — a sequence with an upper bound.
	ArrayBounded
	// ArrayDynamic is `T[]` — an unbounded sequence.
	ArrayDynamic
)

// Primitives are the built-in rosidl field types, keyed by their .msg spelling.
// The value is the native Go type a reflection-based CDR codec expects for a
// wire-compatible layout.
var Primitives = map[string]string{
	"bool":    "bool",
	"byte":    "uint8",
	"char":    "uint8",
	"float32": "float32",
	"float64": "float64",
	"int8":    "int8",
	"uint8":   "uint8",
	"int16":   "int16",
	"uint16":  "uint16",
	"int32":   "int32",
	"uint32":  "uint32",
	"int64":   "int64",
	"uint64":  "uint64",
	"string":  "string",
}

// unsupported are rosidl primitives with no wire-compatible Go type. CDR frames
// a wstring as a count of UTF-16 code units with no terminator, where a string
// is a byte count plus UTF-8 plus a NUL; a byte-oriented codec has only the
// byte path. Mapping one onto the other would compile and pass a
// reflection-based layout check while mis-framing every message from that
// field on. Refuse instead.
var unsupported = map[string]string{
	"wstring": "CDR encodes it as UTF-16 code units, which the codec cannot express",
	"wchar":   "CDR encodes it as a UTF-16 code unit, which the codec cannot express",
}

// Type is a parsed field type: either a primitive or a reference to another
// interface, optionally wrapped in an array shape.
type Type struct {
	// Primitive is the .msg spelling of a built-in type; empty when Nested is set.
	Primitive string
	// Nested names the referenced interface; zero when Primitive is set.
	Nested Name
	// StringBound is N of `string<=N`; 0 when unbounded or not a string.
	StringBound int

	// Array is the array shape.
	Array ArrayKind
	// ArraySize is N of `T[N]` or `T[<=N]`; 0 for ArrayNone and ArrayDynamic.
	ArraySize int
}

// IsNested reports whether the type references another interface.
func (t Type) IsNested() bool { return t.Primitive == "" }

// String renders the type back to its .msg spelling. Used in error messages and
// generated doc comments.
func (t Type) String() string {
	base := t.Primitive
	if base == "" {
		base = t.Nested.Package + "/" + t.Nested.Type
	}
	if t.StringBound > 0 {
		base += "<=" + strconv.Itoa(t.StringBound)
	}
	switch t.Array {
	case ArrayFixed:
		return base + "[" + strconv.Itoa(t.ArraySize) + "]"
	case ArrayBounded:
		return base + "[<=" + strconv.Itoa(t.ArraySize) + "]"
	case ArrayDynamic:
		return base + "[]"
	default:
		return base
	}
}

// parseType parses a .msg type expression. fallbackPkg resolves unqualified
// nested references to the enclosing package.
func parseType(s, fallbackPkg string) (Type, error) {
	var t Type

	base := s
	if strings.HasSuffix(base, "]") {
		open := strings.LastIndex(base, "[")
		if open < 0 {
			return t, fmt.Errorf("unbalanced array brackets in %q", s)
		}
		spec := base[open+1 : len(base)-1]
		base = base[:open]

		switch {
		case spec == "":
			t.Array = ArrayDynamic
		case strings.HasPrefix(spec, "<="):
			n, err := strconv.Atoi(strings.TrimSpace(spec[2:]))
			if err != nil {
				return t, fmt.Errorf("bad array bound in %q: %w", s, err)
			}
			t.Array, t.ArraySize = ArrayBounded, n
		default:
			n, err := strconv.Atoi(strings.TrimSpace(spec))
			if err != nil {
				return t, fmt.Errorf("bad array size in %q: %w", s, err)
			}
			t.Array, t.ArraySize = ArrayFixed, n
		}
	}

	if i := strings.Index(base, "<="); i >= 0 {
		n, err := strconv.Atoi(strings.TrimSpace(base[i+2:]))
		if err != nil {
			return t, fmt.Errorf("bad string bound in %q: %w", s, err)
		}
		base, t.StringBound = base[:i], n
	}

	// Checked before the Primitives lookup: an unsupported primitive would
	// otherwise fall through to ParseName and surface as a confusing
	// "no definition found for <pkg>/msg/wstring".
	if why, bad := unsupported[base]; bad {
		return t, fmt.Errorf("unsupported type %q in %q: %s", base, s, why)
	}

	if _, ok := Primitives[base]; ok {
		if t.StringBound > 0 && base != "string" {
			return t, fmt.Errorf("bound on non-string type in %q", s)
		}
		t.Primitive = base
		return t, nil
	}
	if t.StringBound > 0 {
		return t, fmt.Errorf("bound on non-string type in %q", s)
	}

	n, err := ParseName(base, fallbackPkg)
	if err != nil {
		return t, err
	}
	t.Nested = n
	return t, nil
}
