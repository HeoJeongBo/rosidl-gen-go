package gogen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// configIn writes a config into dir and loads it, so search paths resolve
// against a directory the test controls.
func configIn(t *testing.T, dir, body string) *Config {
	t.Helper()
	p := filepath.Join(dir, "rosidl-gen.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// Dropping a search path silently is what makes ${ROS_ROOT}/share optional on
// a machine without ROS. It is also the usual reason a pattern matches
// nothing, so every drop has to be reportable.
func TestSearchPathReport(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "present"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "afile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RGT_SET", filepath.Join(dir, "present"))
	t.Setenv("RGT_EMPTY", "")
	os.Unsetenv("RGT_UNSET")

	cfg := configIn(t, dir, `
out: out
package: demo
generate: [x/**]
search_paths:
  - present
  - ${RGT_SET}
  - ${RGT_UNSET}/share
  - ${RGT_EMPTY}/share
  - absent
  - afile
`)

	paths, dropped := cfg.SearchPathReport()
	if len(paths) != 2 {
		t.Fatalf("kept %d paths, want 2: %v", len(paths), paths)
	}
	for _, p := range paths {
		if !strings.HasSuffix(p, "present") {
			t.Errorf("kept %q, want the existing directory", p)
		}
	}

	want := map[string]string{
		"${RGT_UNSET}/share": "is not set",
		"${RGT_EMPTY}/share": "is empty",
		"absent":             "no such directory",
		"afile":              "not a directory",
	}
	if len(dropped) != len(want) {
		t.Fatalf("dropped %d paths, want %d: %+v", len(dropped), len(want), dropped)
	}
	for _, d := range dropped {
		frag, ok := want[d.Path]
		if !ok {
			t.Errorf("unexpected drop: %+v", d)
			continue
		}
		if !strings.Contains(d.Reason, frag) {
			t.Errorf("drop reason for %q = %q, want it to mention %q", d.Path, d.Reason, frag)
		}
	}

	// The convenience wrapper must agree with the report.
	if got := cfg.ResolvedSearchPaths(); len(got) != len(paths) {
		t.Errorf("ResolvedSearchPaths() = %v, want the same %d paths", got, len(paths))
	}
}

func TestSearchPathsAreConfigRelative(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "msg"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := configIn(t, dir, "out: out\npackage: demo\ngenerate: [x/**]\nsearch_paths: [msg]\n")

	paths, _ := cfg.SearchPathReport()
	if len(paths) != 1 || paths[0] != filepath.Join(dir, "msg") {
		t.Errorf("paths = %v, want them resolved against the config directory", paths)
	}
}

func TestConfigValidation(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"no out", "package: demo\ngenerate: [x/**]\n", "`out` is required"},
		{"no package", "out: o\ngenerate: [x/**]\n", "`package` is required"},
		{"neither", "generate: [x/**]\n", "`out` and `package` are required"},
		{"bad package", "out: o\npackage: my-pkg\ngenerate: [x/**]\n", "not a valid Go identifier"},
		{"keyword package", "out: o\npackage: range\ngenerate: [x/**]\n", "not a valid Go identifier"},
		{"empty generate", "out: o\npackage: demo\ngenerate: []\n", "`generate` is empty"},
		{"bad external key", "out: o\npackage: demo\ngenerate: [x/**]\nexternal:\n  Foo: Bar\n", "external \"Foo\""},
		{
			"external qualifier without import",
			"out: o\npackage: demo\ngenerate: [x/**]\nexternal:\n  a/msg/B: ros.B\n",
			"no `imports` entry",
		},
		{"not a mapping", "- a\n- b\n", "must be a mapping"},
		{"empty file", "", "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "rosidl-gen.yaml")
			if err := os.WriteFile(p, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(p)
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

// Duplicate keys must never be last-wins. Which layer rejects them is an
// implementation detail; that they are rejected is the guarantee.
func TestLoadConfigRejectsDuplicateKeys(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rosidl-gen.yaml")
	body := "out: first\npackage: demo\ngenerate: [x/**]\nout: second\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("a repeated `out` key was accepted")
	}
}

func TestLoadConfigMissingFileSuggestsInit(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "absent.yaml"))
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "init") {
		t.Errorf("error %q should point at a way to get a config", err)
	}
}

func TestParseConfigResolvesAgainstGivenDir(t *testing.T) {
	dir := t.TempDir()
	cfg, err := ParseConfig([]byte("out: out\npackage: demo\ngenerate: [x/**]\n"), dir, "memory")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Path("out"); got != filepath.Join(dir, "out") {
		t.Errorf("Path(\"out\") = %q, want it under %q", got, dir)
	}
}
