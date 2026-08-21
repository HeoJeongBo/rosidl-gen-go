package wirelock

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HeoJeongBo/rosidl-gen-go/gogen"
	"github.com/HeoJeongBo/rosidl-gen-go/rosidl"
)

// TestShapeOfTypeMatchesGoPath is the premise the whole design rests on.
//
// A lock written from the .msg and checked against the Go structs — or the
// reverse — only works if the two paths spell a layout identically. They are
// separate implementations over separate inputs, so nothing but a test keeps
// them in step, and a divergence would surface as drift that is not there.
func TestShapeOfTypeMatchesGoPath(t *testing.T) {
	cases := []struct {
		name string
		typ  rosidl.Type
		want string // what Registry.Shapes produces for the same field
	}{
		{"bool", rosidl.Type{Primitive: "bool"}, "bool"},
		{"string", rosidl.Type{Primitive: "string"}, "string"},
		{"byte normalises to uint8", rosidl.Type{Primitive: "byte"}, "uint8"},
		{"char normalises to uint8", rosidl.Type{Primitive: "char"}, "uint8"},
		{"int32", rosidl.Type{Primitive: "int32"}, "int32"},
		{"float64", rosidl.Type{Primitive: "float64"}, "float64"},
		{
			"fixed array keeps its length",
			rosidl.Type{Primitive: "float64", Array: rosidl.ArrayFixed, ArraySize: 9},
			"[9]float64",
		},
		{
			"bounded sequence drops its bound",
			rosidl.Type{Primitive: "uint8", Array: rosidl.ArrayBounded, ArraySize: 128},
			"[]uint8",
		},
		{
			"unbounded sequence",
			rosidl.Type{Primitive: "uint8", Array: rosidl.ArrayDynamic},
			"[]uint8",
		},
		{
			"nested by canonical name",
			rosidl.Type{Nested: rosidl.Name{Package: "std_msgs", Kind: rosidl.KindMsg, Type: "Header"}},
			"std_msgs/msg/Header",
		},
		{
			"sequence of nested",
			rosidl.Type{
				Nested: rosidl.Name{Package: "demo_msgs", Kind: rosidl.KindMsg, Type: "Item"},
				Array:  rosidl.ArrayDynamic,
			},
			"[]demo_msgs/msg/Item",
		},
		{
			"fixed array of nested",
			rosidl.Type{
				Nested:    rosidl.Name{Package: "geometry_msgs", Kind: rosidl.KindMsg, Type: "Point"},
				Array:     rosidl.ArrayFixed,
				ArraySize: 3,
			},
			"[3]geometry_msgs/msg/Point",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shapeOfType(tc.typ)
			require.Equal(t, tc.want, got)

			// Every shape is a wrapper over a base that is either a primitive
			// or a canonical ROS name. A base outside that grammar means the
			// two vocabularies have diverged, which is what this test exists to
			// catch — so check the BASE, after the wrapper is stripped.
			base, isRef := Nested(got)
			if isRef {
				require.Contains(t, base, "/", "a reference must be a canonical ROS name")
			} else {
				require.NotEmpty(t, got)
			}
		})
	}
}

// TestBothPathsAgree is the real cross-check behind the table above.
//
// The table pins the spelling but would stay green if Registry.Shapes drifted,
// since it only asserts against what this file CLAIMS the Go path produces. Here
// one fixture goes through both implementations — the .msg field list and the Go
// struct in wirelock_test.go that mirrors it — and the outputs must be equal.
//
// This is what lets a lock written by the generator be checked against a
// consumer's structs, and vice versa.
func TestBothPathsAgree(t *testing.T) {
	msg := func(pkg, typ string) rosidl.Name {
		return rosidl.Name{Package: pkg, Kind: rosidl.KindMsg, Type: typ}
	}

	// Field lists as the .msg parser would produce them, for the same types
	// demoRegistry() holds as Go structs.
	defs := map[string][]rosidl.Type{
		"demo_msgs/msg/Array": {
			{Nested: msg("std_msgs", "Header")},
			{Nested: msg("demo_msgs", "Item"), Array: rosidl.ArrayDynamic},
			{Primitive: "float32", Array: rosidl.ArrayFixed, ArraySize: 4},
		},
		"demo_msgs/msg/Item": {
			{Primitive: "string"},
			{Primitive: "float64"},
		},
		"std_msgs/msg/Header": {
			{Nested: msg("builtin_interfaces", "Time")},
			{Primitive: "string"},
		},
		"builtin_interfaces/msg/Time": {
			{Primitive: "int32"},
			{Primitive: "uint32"},
		},
		"demo_msgs/srv/Run_Request":  {{Primitive: "string"}},
		"demo_msgs/srv/Run_Response": {{Primitive: "bool"}},
	}

	r := demoRegistry()
	for name, fields := range defs {
		t.Run(name, func(t *testing.T) {
			fromGo, err := r.Shapes(name)
			require.NoError(t, err)

			fromMsg := make([]string, 0, len(fields))
			for _, f := range fields {
				fromMsg = append(fromMsg, shapeOfType(f))
			}

			require.Equal(t, fromGo, fromMsg,
				"the .msg path and the Go path must spell %s identically", name)
		})
	}
}

// TestFromConfig covers the section contract: absent means the consumer does
// not want a lock, present-but-incomplete is an error rather than a default.
func TestFromConfig(t *testing.T) {
	write := func(t *testing.T, body string) *gogen.Config {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "rosidl-gen.yaml")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		cfg, err := gogen.LoadConfig(path)
		require.NoError(t, err)
		return cfg
	}

	const base = "out: ./out\npackage: demo\nsearch_paths: [.]\ngenerate: [demo_msgs/**]\n"

	t.Run("absent section declines the feature", func(t *testing.T) {
		_, ok, err := FromConfig(write(t, base))
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("out is required", func(t *testing.T) {
		_, ok, err := FromConfig(write(t, base+"wirelock:\n  out: \"\"\n"))
		require.True(t, ok, "the section was present, so it was claimed")
		require.Error(t, err)
		require.Contains(t, err.Error(), "`out` is required")
	})

	t.Run("resolves out against the config file", func(t *testing.T) {
		cfg := write(t, base+"wirelock:\n  out: testdata/wire-lock.txt\n")
		lock, ok, err := FromConfig(cfg)
		require.NoError(t, err)
		require.True(t, ok)
		require.True(t, filepath.IsAbs(lock.Path()))
		require.Equal(t, "wire-lock.txt", filepath.Base(lock.Path()))
	})

	t.Run("round-trips through the file", func(t *testing.T) {
		cfg := write(t, base+"wirelock:\n  out: nested/dir/wire-lock.txt\n")
		lock, _, err := FromConfig(cfg)
		require.NoError(t, err)

		want := Set{"demo_msgs/msg/X": {"uint8", "string"}}
		require.NoError(t, lock.Write(want), "Write creates parent directories")

		got, err := lock.Load()
		require.NoError(t, err)
		require.Equal(t, want, got)

		require.Empty(t, must(lock.Check(want)), "an unchanged layout reports nothing")
		require.Len(t, must(lock.Check(Set{"demo_msgs/msg/X": {"string", "uint8"}})), 1,
			"a reorder must be reported")
	})
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
