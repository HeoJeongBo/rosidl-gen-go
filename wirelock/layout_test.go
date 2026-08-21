package wirelock

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func fields(pairs ...string) []Field {
	if len(pairs)%2 != 0 {
		panic("want name/shape pairs")
	}
	out := make([]Field, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, Field{Name: pairs[i], Shape: pairs[i+1]})
	}
	return out
}

// TestClassifyLayoutSameTypeReorder is the case that motivated recording names
// at all: two fields of the SAME type traded places. Every shape is unchanged,
// so a shape-only lock spells the message identically and reports nothing —
// while the receiver decodes velocity into position, silently, forever.
func TestClassifyLayoutSameTypeReorder(t *testing.T) {
	locked := fields("position", "float64", "velocity", "float64")
	current := fields("velocity", "float64", "position", "float64")

	require.Equal(t, shapesOf(locked), shapesOf(current),
		"the premise: shapes alone cannot tell these apart")

	got := ClassifyLayout(locked, current)
	require.Contains(t, got, "REORDERED")
	require.Contains(t, got, "position: index 0 -> 1")
	require.Contains(t, got, "velocity: index 1 -> 0")
}

// TestClassifyLayout covers what must fire and what must stay silent. The silent
// half is load-bearing: a check that fails on a rename gets switched off, and
// then it guards nothing at all.
func TestClassifyLayout(t *testing.T) {
	breaking := []struct {
		name            string
		locked, current []Field
		wants           []string
	}{
		{
			"same-type swap, non-adjacent",
			fields("a", "float64", "mid", "uint8", "b", "float64"),
			fields("b", "float64", "mid", "uint8", "a", "float64"),
			[]string{"REORDERED", "a: index 0 -> 2", "b: index 2 -> 0"},
		},
		{
			"three same-typed fields rotated",
			fields("x", "int32", "y", "int32", "z", "int32"),
			fields("z", "int32", "x", "int32", "y", "int32"),
			[]string{"REORDERED", "x: index 0 -> 1", "z: index 2 -> 0"},
		},
		{
			"same-type swap inside an otherwise identical message",
			fields("h", "std_msgs/msg/Header", "p", "[]float64", "q", "[]float64"),
			fields("h", "std_msgs/msg/Header", "q", "[]float64", "p", "[]float64"),
			[]string{"REORDERED", "p: index 1 -> 2", "q: index 2 -> 1"},
		},
		// Shapes still decide when they differ; these must keep reading exactly
		// as they did before names existed.
		{
			// Ambiguous by nature: either `b` slid down a slot, or `a` and `b`
			// were renamed past each other. No record can resolve it, and only
			// one of the two readings is safe to be wrong about.
			"a surviving name that changed index, amid renames",
			fields("a", "string", "b", "string"),
			fields("b", "string", "c", "string"),
			[]string{"REORDERED", "b: index 1 -> 0"},
		},
		{
			"different-type swap still classified by shape",
			fields("a", "string", "b", "uint8"),
			fields("b", "uint8", "a", "string"),
			[]string{"REORDERED", "index 0 string -> uint8"},
		},
		{
			"insertion",
			fields("a", "string", "b", "uint8"),
			fields("a", "string", "n", "float32", "b", "uint8"),
			[]string{"INSERTED at index 1", "float32"},
		},
		{
			"widening",
			fields("t", "int8"),
			fields("t", "int32"),
			[]string{"index 0 int8 -> int32"},
		},
		{
			"append",
			fields("a", "string"),
			fields("a", "string", "b", "float32"),
			[]string{"APPENDED", "outermost"},
		},
		{
			"fixed array resized",
			fields("cov", "[9]float64"),
			fields("cov", "[16]float64"),
			[]string{"[9]float64 -> [16]float64"},
		},
	}
	for _, tc := range breaking {
		t.Run("breaks/"+tc.name, func(t *testing.T) {
			got := ClassifyLayout(tc.locked, tc.current)
			require.NotEmpty(t, got, "%s must be reported", tc.name)
			for _, want := range tc.wants {
				require.Contains(t, got, want)
			}
		})
	}

	harmless := []struct {
		name            string
		locked, current []Field
	}{
		{"identical", fields("a", "string", "b", "uint8"), fields("a", "string", "b", "uint8")},
		{
			"one field renamed",
			fields("torque", "float64", "b", "uint8"),
			fields("torque_nm", "float64", "b", "uint8"),
		},
		{
			"every field renamed",
			fields("a", "string", "b", "uint8"),
			fields("x", "string", "y", "uint8"),
		},
	}
	for _, tc := range harmless {
		t.Run("harmless/"+tc.name, func(t *testing.T) {
			require.Empty(t, ClassifyLayout(tc.locked, tc.current),
				"%s does not move a byte; failing on it is what gets a check switched off", tc.name)
		})
	}
}

// TestLayoutRoundTrip pins the file format, including its refusal to read a lock
// written before names were recorded.
func TestLayoutRoundTrip(t *testing.T) {
	l := Layout{
		"demo_msgs/msg/A": fields("h", "std_msgs/msg/Header", "vals", "[]float64", "cov", "[9]float64"),
		"demo_msgs/msg/B": fields("ok", "bool"),
	}

	got, err := ParseLayout(l.Format())
	require.NoError(t, err)
	require.Equal(t, l, got)
	require.Empty(t, DiffLayout(l, got))

	t.Run("an old shape-only lock is rejected, not misread", func(t *testing.T) {
		_, err := ParseLayout([]byte("demo_msgs/msg/A: int32, uint32\n"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "predates field names")
	})

	t.Run("a line with no separator is an error", func(t *testing.T) {
		_, err := ParseLayout([]byte("demo_msgs/msg/A int32\n"))
		require.Error(t, err)
	})

	t.Run("Set drops the names", func(t *testing.T) {
		require.Equal(t, []string{"bool"}, l.Set()["demo_msgs/msg/B"])
	})
}

// TestDiffLayoutMembership covers types entering and leaving the lock.
func TestDiffLayoutMembership(t *testing.T) {
	a := Layout{"demo_msgs/msg/A": fields("x", "int32")}
	b := Layout{"demo_msgs/msg/B": fields("y", "bool")}

	got := DiffLayout(a, b)
	require.Len(t, got, 2)
	require.Equal(t, "demo_msgs/msg/A", got[0].Type)
	require.Contains(t, got[0].Detail, "left the locked set")
	require.Equal(t, "demo_msgs/msg/B", got[1].Type)
	require.Contains(t, got[1].Detail, "entered the locked set")
	require.Contains(t, got[1].Detail, "y bool")
}
