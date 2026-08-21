package wirelock

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func snap(fields []Field, consts ...Constant) Snapshot {
	s := Snapshot{
		Fields: Layout{"demo_msgs/msg/M": fields},
		Consts: map[string][]Constant{},
	}
	if len(consts) > 0 {
		s.Consts["demo_msgs/msg/M"] = consts
	}
	return s
}

func only(t *testing.T, changes []Change) Change {
	t.Helper()
	require.Len(t, changes, 1)
	return changes[0]
}

// TestStaleLockCannotMaskALaterReorder is the reason every difference fails,
// not just the ones that move bytes.
//
// A rename does not touch the wire, so it is tempting to pass it silently. Do
// that and the lock keeps a name no field carries any more — and the swap that
// comes later, in a different commit by a different person, is no longer a
// permutation of anything the lock knows. The expensive check goes quiet
// forever, and nothing ever says why.
func TestStaleLockCannotMaskALaterReorder(t *testing.T) {
	locked := snap(fields("position", "float64", "velocity", "float64"))

	// Step 1: a rename. Nothing reaches the wire...
	renamedOnly := snap(fields("pos", "float64", "velocity", "float64"))
	c := only(t, DiffSnapshot(locked, renamedOnly))
	require.Equal(t, SeverityStale, c.Severity, "a rename asks nothing of a deployment")
	require.Contains(t, c.Detail, "renamed")

	// ...but it MUST fail, so the lock gets refreshed.
	require.NotEmpty(t, DiffSnapshot(locked, renamedOnly),
		"passing here is what lets step 2 go unnoticed")

	// Step 2: the swap, against a lock that was never refreshed. Failing is not
	// enough — it has to be told apart from the rename it superficially looks
	// like, or the reader is handed "no release coordination needed" about a
	// change that silently swaps two values on the wire.
	//
	// `velocity` is the witness: it survives both edits and changes index.
	swappedAfterRename := snap(fields("velocity", "float64", "pos", "float64"))
	c = only(t, DiffSnapshot(locked, swappedAfterRename))
	require.Equal(t, SeverityBreaking, c.Severity,
		"a swap reached through a stale lock must not read as a harmless rename")
	require.Contains(t, c.Detail, "velocity: index 1 -> 0")

	// And against a lock that WAS refreshed after the rename, it is named for
	// what it is.
	refreshed := renamedOnly
	c = only(t, DiffSnapshot(refreshed, swappedAfterRename))
	require.Equal(t, SeverityBreaking, c.Severity)
	require.Contains(t, c.Detail, "REORDERED")
}

// TestDiffSnapshotSeverity is the table that says what each kind of edit costs
// the reader. The severity is the whole point: everything here fails, and the
// only question is whether two programs have to ship together.
func TestDiffSnapshotSeverity(t *testing.T) {
	base := snap(
		fields("a", "int32", "b", "float64", "c", "float64"),
		Constant{"MODE_IDLE", "0"}, Constant{"MODE_TORQUE", "5"},
	)

	cases := []struct {
		name    string
		current Snapshot
		want    Severity
		wants   []string
	}{
		{
			"constant value changed",
			snap(fields("a", "int32", "b", "float64", "c", "float64"),
				Constant{"MODE_IDLE", "0"}, Constant{"MODE_TORQUE", "7"}),
			SeverityBreaking,
			[]string{"constant MODE_TORQUE changed 5 -> 7", "still sends the old value"},
		},
		{
			"constant added",
			snap(fields("a", "int32", "b", "float64", "c", "float64"),
				Constant{"MODE_IDLE", "0"}, Constant{"MODE_NEW", "9"}, Constant{"MODE_TORQUE", "5"}),
			SeverityStale,
			[]string{"constant MODE_NEW added"},
		},
		{
			"constant removed",
			snap(fields("a", "int32", "b", "float64", "c", "float64"),
				Constant{"MODE_TORQUE", "5"}),
			SeverityStale,
			[]string{"constant MODE_IDLE removed"},
		},
		{
			"field renamed",
			snap(fields("a", "int32", "b", "float64", "renamed", "float64"),
				Constant{"MODE_IDLE", "0"}, Constant{"MODE_TORQUE", "5"}),
			SeverityStale,
			[]string{"renamed", "index 2 c -> renamed"},
		},
		{
			"same-typed fields swapped",
			snap(fields("a", "int32", "c", "float64", "b", "float64"),
				Constant{"MODE_IDLE", "0"}, Constant{"MODE_TORQUE", "5"}),
			SeverityBreaking,
			[]string{"REORDERED", "b: index 1 -> 2", "c: index 2 -> 1"},
		},
		{
			"field widened",
			snap(fields("a", "int64", "b", "float64", "c", "float64"),
				Constant{"MODE_IDLE", "0"}, Constant{"MODE_TORQUE", "5"}),
			SeverityBreaking,
			[]string{"index 0 int32 -> int64"},
		},
		{
			"field inserted",
			snap(fields("a", "int32", "n", "uint8", "b", "float64", "c", "float64"),
				Constant{"MODE_IDLE", "0"}, Constant{"MODE_TORQUE", "5"}),
			SeverityBreaking,
			[]string{"INSERTED at index 1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := only(t, DiffSnapshot(base, tc.current))
			require.Equal(t, tc.want, c.Severity, "detail was: %s", c.Detail)
			for _, w := range tc.wants {
				require.Contains(t, c.Detail, w)
			}
		})
	}

	t.Run("an identical snapshot reports nothing", func(t *testing.T) {
		require.Empty(t, DiffSnapshot(base, base))
	})

	t.Run("a type leaving and entering is stale, not breaking", func(t *testing.T) {
		other := Snapshot{
			Fields: Layout{"demo_msgs/msg/Other": fields("x", "bool")},
			Consts: map[string][]Constant{},
		}
		got := DiffSnapshot(base, other)
		require.Len(t, got, 2)
		for _, c := range got {
			require.Equal(t, SeverityStale, c.Severity, "%s: %s", c.Type, c.Detail)
		}
	})

	t.Run("constants of a departed type are not repeated", func(t *testing.T) {
		empty := Snapshot{Fields: Layout{}, Consts: map[string][]Constant{}}
		got := DiffSnapshot(base, empty)
		require.Len(t, got, 1, "one line about the type, not one per constant")
		require.Contains(t, got[0].Detail, "left the generated set")
	})
}

// TestSnapshotRoundTrip pins the file format, including the values that would
// break a comma-separated encoding and the refusal to read an older format.
func TestSnapshotRoundTrip(t *testing.T) {
	s := Snapshot{
		Fields: Layout{
			"demo_msgs/msg/A": fields("h", "std_msgs/msg/Header", "vals", "[]float64", "cov", "[9]float64"),
			"demo_msgs/msg/B": fields("ok", "bool"),
		},
		Consts: map[string][]Constant{
			// A string constant's value can hold commas and spaces — the reason
			// constants get a line each instead of a comma-separated tail.
			"demo_msgs/msg/A": {
				{"GREETING", `"hello, world"`},
				{"LIMIT", "255"},
			},
		},
	}

	got, err := ParseSnapshot(s.Format("rosidl-gen-go v0.6.0"))
	require.NoError(t, err)
	require.Equal(t, s, got)
	require.Empty(t, DiffSnapshot(s, got))

	t.Run("the header names the generator", func(t *testing.T) {
		require.Contains(t, string(s.Format("rosidl-gen-go v0.6.0")), "# generator: rosidl-gen-go v0.6.0")
	})

	t.Run("a comma inside a constant value survives", func(t *testing.T) {
		require.Equal(t, `"hello, world"`, got.Consts["demo_msgs/msg/A"][0].Value)
	})

	t.Run("an older format is refused, not read leniently", func(t *testing.T) {
		// Format 1 had no constants. Reading it would call every current
		// constant an addition and check none of their values.
		_, err := ParseSnapshot([]byte("demo_msgs/msg/A: x int32\n"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "regenerate")

		_, err = ParseSnapshot([]byte("# lock-format: 1\ndemo_msgs/msg/A: x int32\n"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "format 1")
	})

	t.Run("a malformed line is an error, never a skip", func(t *testing.T) {
		for _, bad := range []string{
			"# lock-format: 2\ndemo_msgs/msg/A x int32\n",
			"# lock-format: 2\ndemo_msgs/msg/A: int32\n",
			"# lock-format: 2\nconst demo_msgs/msg/A.X 5\n",
			"# lock-format: 2\nconst NOTATYPE = 5\n",
		} {
			_, err := ParseSnapshot([]byte(bad))
			require.Error(t, err, "must not silently drop: %q", bad)
		}
	})

	t.Run("a fieldless type round-trips", func(t *testing.T) {
		// The rosidl parser substitutes a dummy member so this does not occur
		// today, but a format that cannot express it would corrupt on the day
		// that changes.
		empty := Snapshot{
			Fields: Layout{"demo_msgs/msg/E": nil},
			Consts: map[string][]Constant{},
		}
		back, err := ParseSnapshot(empty.Format("t"))
		require.NoError(t, err)
		require.Equal(t, empty, back)
	})
}
