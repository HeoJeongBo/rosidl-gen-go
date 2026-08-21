package wirelock

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HeoJeongBo/rosidl-gen-go/rosidl"
)

// TestDiffDetects is the table that says what a lock is FOR.
//
// It runs against Diff directly rather than through a regeneration, so it covers
// the whole space of layout changes in milliseconds. The must-pass half is the
// important one: a check that fires on a comment or a rename gets switched off,
// and then it guards nothing at all.
func TestDiffDetects(t *testing.T) {
	const k = "demo_msgs/msg/Example"

	breaking := []struct {
		name           string
		locked, actual []string
		// wants are substrings the description must contain. Detail is not
		// decoration: a reader uses it to decide whether two programs have to be
		// released together, so asserting only that it is non-empty would let
		// Classify degrade to "changed" with every test still green.
		wants []string
	}{
		// Reorder first: it is the worst case. The total size is unchanged, so
		// cdr reports nothing at all and the values simply arrive swapped.
		{"field reorder", []string{"string", "uint8"}, []string{"uint8", "string"},
			[]string{"REORDERED", "index 0 string -> uint8"}},
		{"field inserted mid-struct", []string{"string", "uint8"}, []string{"string", "float32", "uint8"},
			[]string{"INSERTED at index 1", "float32"}},
		{"field appended", []string{"string", "uint8"}, []string{"string", "uint8", "float32"},
			[]string{"APPENDED", "outermost"}},
		{"field removed from the end", []string{"string", "uint8"}, []string{"string"},
			[]string{"REMOVED", "uint8"}},
		{"field removed mid-struct", []string{"string", "float32", "uint8"}, []string{"string", "uint8"},
			[]string{"REMOVED at index 1", "float32"}},
		{"integer widened", []string{"int32"}, []string{"int64"},
			[]string{"index 0 int32 -> int64"}},
		{"integer narrowed", []string{"int64"}, []string{"int32"},
			[]string{"index 0 int64 -> int32"}},
		{"sign changed", []string{"int32"}, []string{"uint32"},
			[]string{"int32 -> uint32"}},
		{"float widened", []string{"float32"}, []string{"float64"},
			[]string{"float32 -> float64"}},
		{"fixed array length changed", []string{"[9]float64"}, []string{"[16]float64"},
			[]string{"[9]float64 -> [16]float64"}},
		{"fixed array became a sequence", []string{"[9]float64"}, []string{"[]float64"},
			[]string{"[9]float64 -> []float64"}},
		{"sequence became a fixed array", []string{"[]float64"}, []string{"[9]float64"},
			[]string{"[]float64 -> [9]float64"}},
		{"scalar became a sequence", []string{"float64"}, []string{"[]float64"},
			[]string{"float64 -> []float64"}},
		{"sequence element type changed", []string{"[]float32"}, []string{"[]float64"},
			[]string{"[]float32 -> []float64"}},
		{"nested type replaced", []string{"std_msgs/msg/Header"}, []string{"builtin_interfaces/msg/Time"},
			[]string{"std_msgs/msg/Header -> builtin_interfaces/msg/Time"}},
		{"nested became a sequence of itself", []string{"std_msgs/msg/Header"}, []string{"[]std_msgs/msg/Header"},
			[]string{"std_msgs/msg/Header -> []std_msgs/msg/Header"}},
		// Same length and same multiset would read as a reorder; these differ so
		// the description must not claim one.
		{"swap that is not a permutation", []string{"int32", "float32"}, []string{"float32", "float64"},
			[]string{"field type changed"}},
		{"permutation with duplicate shapes", []string{"float64", "int32", "float64"}, []string{"float64", "float64", "int32"},
			[]string{"REORDERED"}},
	}
	for _, tc := range breaking {
		t.Run("breaks/"+tc.name, func(t *testing.T) {
			got := Diff(Set{k: tc.locked}, Set{k: tc.actual})
			require.Len(t, got, 1, "%s must be reported: %v -> %v", tc.name, tc.locked, tc.actual)
			require.Equal(t, k, got[0].Type)
			for _, want := range tc.wants {
				require.Contains(t, got[0].Detail, want,
					"the description must say what changed, for %s", tc.name)
			}
		})
	}

	t.Run("breaks/a permutation is never called a type change", func(t *testing.T) {
		got := Diff(Set{k: {"int32", "float32"}}, Set{k: {"float32", "int32"}})
		require.Len(t, got, 1)
		require.NotContains(t, got[0].Detail, "field type changed",
			"a reorder and a retype need different remedies; conflating them misleads the reader")
	})

	// Every row below is locked == current, because that IS the claim: each of
	// these .msg edits leaves the shape vector untouched, so Diff never sees
	// them. They share one code path and prove one thing between them — that the
	// vocabulary drops what CDR drops.
	harmless := []struct {
		name           string
		locked, actual []string
		why            string
	}{
		{"field renamed", []string{"string", "uint8"}, []string{"string", "uint8"},
			"CDR encodes by position; names are never on the wire"},
		{"comment added or edited", []string{"string", "uint8"}, []string{"string", "uint8"},
			"comments do not reach a shape at all"},
		{"constant added or renumbered", []string{"string", "uint8"}, []string{"string", "uint8"},
			"constants are not fields and are not transmitted"},
		{"string bound widened", []string{"string"}, []string{"string"},
			"a CDR string carries its own length prefix, so <=64 and <=128 are the same bytes"},
		{"sequence bound widened", []string{"[]uint8"}, []string{"[]uint8"},
			"a sequence carries its own length prefix, so the declared bound is metadata"},
		{"bounded sequence became unbounded", []string{"[]uint8"}, []string{"[]uint8"},
			"identical on the wire; the vocabulary collapses both to []"},
		{"byte spelled as uint8", []string{"uint8"}, []string{"uint8"},
			"rosidl.Primitives normalises byte and char to uint8 before comparison"},
	}
	for _, tc := range harmless {
		t.Run("passes/"+tc.name, func(t *testing.T) {
			require.Empty(t, Diff(Set{k: tc.locked}, Set{k: tc.actual}),
				"%s must NOT be reported: %s", tc.name, tc.why)
		})
	}

	t.Run("reports every changed type, sorted", func(t *testing.T) {
		// Diff returns a list and a human reads the output, so the order has to
		// be stable across runs — map iteration is not.
		got := Diff(
			Set{"b/msg/B": {"uint8"}, "a/msg/A": {"uint8"}, "c/msg/C": {"uint8"}},
			Set{"b/msg/B": {"uint16"}, "a/msg/A": {"uint16"}, "c/msg/C": {"uint8"}},
		)
		require.Len(t, got, 2, "both changed types must be reported, not just the first")
		require.Equal(t, []string{"a/msg/A", "b/msg/B"}, []string{got[0].Type, got[1].Type})
	})

	t.Run("mixes changed, added and removed in one report", func(t *testing.T) {
		got := Diff(
			Set{"a/msg/A": {"uint8"}, "b/msg/B": {"uint8"}},
			Set{"a/msg/A": {"uint16"}, "c/msg/C": {"bool"}},
		)
		require.Len(t, got, 3)
		require.Equal(t, []string{"a/msg/A", "b/msg/B", "c/msg/C"},
			[]string{got[0].Type, got[1].Type, got[2].Type})
		require.Contains(t, got[1].Detail, "left")
		require.Contains(t, got[2].Detail, "entered")
	})

	t.Run("passes/both empty", func(t *testing.T) {
		require.Empty(t, Diff(Set{}, Set{}))
	})

	t.Run("breaks/first generation reports everything as entering", func(t *testing.T) {
		got := Diff(Set{}, Set{k: {"uint8"}})
		require.Len(t, got, 1)
		require.Contains(t, got[0].Detail, "entered")
	})
}

// TestNested pins the one function that decides how far a closure reaches.
//
// A shape that names a type but is read as a primitive drops that whole branch:
// the lock keeps passing, the file keeps looking complete, and the types behind
// the missed reference are no longer guarded. Array and sequence prefixes are
// where that is easiest to get wrong.
func TestNested(t *testing.T) {
	refs := map[string]string{
		"std_msgs/msg/Header":          "std_msgs/msg/Header",
		"[]demo_msgs/msg/Item":         "demo_msgs/msg/Item",
		"[3]geometry_msgs/msg/Point":   "geometry_msgs/msg/Point",
		"[128]demo_msgs/msg/Slot":      "demo_msgs/msg/Slot",
		"example.com/pkg.Unregistered": "example.com/pkg.Unregistered",
	}
	for shape, want := range refs {
		t.Run("reference/"+shape, func(t *testing.T) {
			got, ok := Nested(shape)
			require.True(t, ok, "%q refers to a type", shape)
			require.Equal(t, want, got)
		})
	}

	for _, shape := range []string{
		"bool", "string", "int8", "uint8", "int16", "uint16",
		"int32", "uint32", "int64", "uint64", "float32", "float64",
		"[]uint8", "[9]float64", "[]string", "[36]float64",
	} {
		t.Run("primitive/"+shape, func(t *testing.T) {
			_, ok := Nested(shape)
			require.False(t, ok, "%q is a primitive; treating it as a type would break the walk", shape)
		})
	}
}

// TestPrimitivesTrackRosidl is the guard against this package restating what the
// parser already knows. If rosidl learns a type and this set is a copy, a shape
// it now emits reads as a nested reference and the closure fails on a name that
// is not a type — or worse, a consumer's copy diverges silently.
func TestPrimitivesTrackRosidl(t *testing.T) {
	for spelling, goType := range rosidlPrimitives() {
		require.True(t, primitives[goType],
			"rosidl spells %q as Go %q, but the shape vocabulary does not know it", spelling, goType)
	}
}

func TestFormatParseRoundTrip(t *testing.T) {
	want := Set{
		"demo_msgs/msg/RangeState":     {"string", "sensor_msgs/msg/Range", "uint8"},
		"sensor_msgs/msg/Imu":          {"std_msgs/msg/Header", "[9]float64", "[]float32"},
		"std_srvs/srv/Trigger_Request": {"uint8"},
	}
	got, err := Parse(want.Format())
	require.NoError(t, err)
	require.Equal(t, want, got)

	t.Run("skips the header and blank lines", func(t *testing.T) {
		got, err := Parse([]byte("# a comment\n\n  \ndemo_msgs/msg/X: uint8, bool\n"))
		require.NoError(t, err)
		require.Equal(t, Set{"demo_msgs/msg/X": {"uint8", "bool"}}, got)
	})

	t.Run("rejects a line it cannot read", func(t *testing.T) {
		// Silently skipping a malformed line would drop that type from the
		// comparison and leave the check green about it.
		_, err := Parse([]byte("demo_msgs/msg/X uint8\n"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "separator")
	})

	t.Run("round-trips an empty lock", func(t *testing.T) {
		got, err := Parse(Set{}.Format())
		require.NoError(t, err)
		require.Empty(t, got)
	})
}

// The types below stand in for a consumer's generated structs. They are written
// by hand so the closure tests do not need a generator run.

type demoHeader struct {
	Stamp demoTime
	Frame string
}

type demoTime struct {
	Sec     int32
	Nanosec uint32
}

type demoItem struct {
	Name  string
	Value float64
}

type demoArray struct {
	Header demoHeader
	Items  []demoItem
	Fixed  [4]float32
	hidden string //nolint:unused // asserts the unexported-field skip
}

// demoUnreferenced needs a type of its OWN: two registry keys sharing one Go
// struct make the name lookup depend on map order, which is random.
type demoUnreferenced struct {
	Label string
	Count int32
}

type demoRequest struct{ Command string }

type demoResponse struct{ Success bool }

func demoRegistry() Registry {
	return Registry{
		"demo_msgs/msg/Array":           demoArray{},
		"demo_msgs/msg/Item":            demoItem{},
		"std_msgs/msg/Header":           demoHeader{},
		"builtin_interfaces/msg/Time":   demoTime{},
		"demo_msgs/srv/Run_Request":     demoRequest{},
		"demo_msgs/srv/Run_Response":    demoResponse{},
		"demo_msgs/msg/NeverReferenced": demoUnreferenced{},
	}
}

func TestShapes(t *testing.T) {
	got, err := demoRegistry().Shapes("demo_msgs/msg/Array")
	require.NoError(t, err)
	require.Equal(t, []string{
		"std_msgs/msg/Header",
		"[]demo_msgs/msg/Item",
		"[4]float32",
	}, got, "nested types are recorded by ROS name, and the unexported field is skipped as cdr skips it")

	_, err = demoRegistry().Shapes("demo_msgs/msg/Missing")
	require.Error(t, err)
}

// TestClosure covers the walk itself: what it reaches, what it stops at, and
// what it refuses to guess.
func TestClosure(t *testing.T) {
	r := demoRegistry()

	t.Run("descends through nesting and sequence elements", func(t *testing.T) {
		got, err := r.Closure([]string{"demo_msgs/msg/Array"})
		require.NoError(t, err)
		for _, want := range []string{
			"demo_msgs/msg/Array",
			"demo_msgs/msg/Item", // reached only through []Item
			"std_msgs/msg/Header",
			"builtin_interfaces/msg/Time", // reached only through Header
		} {
			require.Contains(t, got, want, "closure missed %s", want)
		}
	})

	t.Run("stops at what no root reaches", func(t *testing.T) {
		got, err := r.Closure([]string{"demo_msgs/msg/Array"})
		require.NoError(t, err)
		require.NotContains(t, got, "demo_msgs/msg/NeverReferenced",
			"a registered type nothing reaches must stay out; locking it would fail builds "+
				"over bytes that never leave the process")
	})

	t.Run("a service root contributes both halves", func(t *testing.T) {
		got, err := r.Closure([]string{"demo_msgs/srv/Run"})
		require.NoError(t, err)
		require.Contains(t, got, "demo_msgs/srv/Run_Request")
		require.Contains(t, got, "demo_msgs/srv/Run_Response")
		require.NotContains(t, got, "demo_msgs/srv/Run",
			"a bare service name is not a wire type; only its halves are")
	})

	t.Run("shared types converge instead of looping", func(t *testing.T) {
		got, err := r.Closure([]string{"demo_msgs/msg/Array", "std_msgs/msg/Header"})
		require.NoError(t, err)
		require.Len(t, got["std_msgs/msg/Header"], 2)
	})

	t.Run("fails on a reference it cannot resolve", func(t *testing.T) {
		// The branch would otherwise be dropped in silence, which is the failure
		// shape a lock exists to prevent.
		partial := Registry{"demo_msgs/msg/Array": demoArray{}}
		_, err := partial.Closure([]string{"demo_msgs/msg/Array"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "not in the registry")
	})

	t.Run("rejects an unparseable root", func(t *testing.T) {
		_, err := r.Closure([]string{"not-a-ros-name"})
		require.Error(t, err)
	})
}

// rosidlPrimitives is a thin indirection so the guard above reads as a claim
// about rosidl rather than about an import.
func rosidlPrimitives() map[string]string { return rosidl.Primitives }
