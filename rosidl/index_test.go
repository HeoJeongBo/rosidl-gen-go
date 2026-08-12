package rosidl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pkgTree writes an ament package under root. defs are file names relative to
// the package directory, e.g. "msg/Thing.msg".
func pkgTree(t *testing.T, root, pkg string, defs map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, pkg)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `<?xml version="1.0"?><package format="3"><name>` + pkg + `</name></package>`
	if err := os.WriteFile(filepath.Join(dir, "package.xml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range defs {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func demoRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	pkgTree(t, root, "demo_msgs", map[string]string{
		"msg/State.msg":    "uint8 mode\n",
		"msg/Reading.msg":  "float64 value\n",
		"srv/SetMode.srv":  "uint8 mode\n---\nbool ok\n",
		"srv/Reset.srv":    "---\nbool ok\n",
		"msg/notadef.txt":  "ignored\n",
		"srv/notadef.json": "ignored\n",
	})
	return root
}

func TestNewIndexFindsPackagesOneLevelDown(t *testing.T) {
	ix, err := NewIndex([]string{demoRoot(t)})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(ix.Names()); got != 4 {
		t.Fatalf("indexed %d names, want 4: %v", got, ix.Names())
	}
	if got := ix.Packages(); len(got) != 1 || got[0] != "demo_msgs" {
		t.Errorf("Packages() = %v", got)
	}
}

// A search path may be the package itself, not only a directory of packages.
func TestNewIndexAcceptsAPackageAsTheRoot(t *testing.T) {
	root := t.TempDir()
	dir := pkgTree(t, root, "demo_msgs", map[string]string{"msg/State.msg": "uint8 mode\n"})

	ix, err := NewIndex([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if !ix.Has(Name{Package: "demo_msgs", Kind: KindMsg, Type: "State"}) {
		t.Errorf("names = %v", ix.Names())
	}
}

// The scan is one level deep. A colcon workspace whose packages sit under
// src/interfaces/<pkg> therefore finds nothing when pointed at src/ — the
// constraint that produced the most misleading error in the tool.
func TestNewIndexDoesNotDescendTwoLevels(t *testing.T) {
	root := t.TempDir()
	pkgTree(t, filepath.Join(root, "interfaces"), "demo_msgs", map[string]string{"msg/State.msg": "uint8 mode\n"})

	ix, err := NewIndex([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(ix.Names()); got != 0 {
		t.Fatalf("indexed %d names from two levels down, want 0", got)
	}

	_, err = ix.Match("demo_msgs/**")
	if err == nil {
		t.Fatal("want an error")
	}
	// The message has to point at the search path, not the pattern.
	for _, want := range []string{"no ament packages were found", "scanned:", "package.xml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestNewIndexEarlierSearchPathWins(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	pkgTree(t, first, "demo_msgs", map[string]string{"msg/State.msg": "uint8 winner\n"})
	pkgTree(t, second, "demo_msgs", map[string]string{"msg/State.msg": "uint8 loser\n"})

	ix, err := NewIndex([]string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	m, err := ix.Message(Name{Package: "demo_msgs", Kind: KindMsg, Type: "State"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Fields[0].Name != "winner" {
		t.Errorf("field = %q, want the definition from the first search path", m.Fields[0].Name)
	}
}

func TestNewIndexSkipsBuildArtifacts(t *testing.T) {
	root := t.TempDir()
	pkgTree(t, filepath.Join(root, "build"), "demo_msgs", map[string]string{"msg/State.msg": "uint8 mode\n"})
	pkgTree(t, filepath.Join(root, "install"), "other_msgs", map[string]string{"msg/Thing.msg": "uint8 x\n"})

	ix, err := NewIndex([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(ix.Names()); got != 0 {
		t.Errorf("indexed %d names from build/install, want 0: %v", got, ix.Names())
	}
}

func TestNewIndexRejectsMissingSearchPath(t *testing.T) {
	if _, err := NewIndex([]string{filepath.Join(t.TempDir(), "absent")}); err == nil {
		t.Fatal("want an error for a search path that does not exist")
	}
}

// The `generate` pattern grammar is the tool's most user-facing syntax.
func TestMatchPatterns(t *testing.T) {
	ix, err := NewIndex([]string{demoRoot(t)})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		pattern string
		want    []string
	}{
		{"demo_msgs/**", []string{
			"demo_msgs/msg/Reading", "demo_msgs/msg/State",
			"demo_msgs/srv/Reset", "demo_msgs/srv/SetMode",
		}},
		{"demo_msgs/msg/*", []string{"demo_msgs/msg/Reading", "demo_msgs/msg/State"}},
		{"demo_msgs/srv/*", []string{"demo_msgs/srv/Reset", "demo_msgs/srv/SetMode"}},
		{"demo_msgs/msg/State", []string{"demo_msgs/msg/State"}},
		{"demo_msgs/srv/SetMode", []string{"demo_msgs/srv/SetMode"}},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			got, err := ix.Match(tc.pattern)
			if err != nil {
				t.Fatalf("Match: %v", err)
			}
			var names []string
			for _, n := range got {
				names = append(names, n.String())
			}
			if strings.Join(names, ",") != strings.Join(tc.want, ",") {
				t.Errorf("Match(%q) = %v, want %v", tc.pattern, names, tc.want)
			}
		})
	}
}

func TestMatchRejectsBadPatterns(t *testing.T) {
	ix, err := NewIndex([]string{demoRoot(t)})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, pattern, want string }{
		{"bare package", "demo_msgs", "malformed"},
		{"wrong two-part wildcard", "demo_msgs/*", "want `pkg/**`"},
		{"too many parts", "a/b/c/d", "malformed"},
		{"unknown package", "nope_msgs/**", "packages found: demo_msgs"},
		{"unknown kind", "demo_msgs/action/Thing", `unknown kind "action"`},
		{"unknown type", "demo_msgs/msg/Nope", "matched no interface"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ix.Match(tc.pattern)
			if err == nil {
				t.Fatalf("Match(%q) succeeded, want an error", tc.pattern)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestIndexLookupAndMessages(t *testing.T) {
	ix, err := NewIndex([]string{demoRoot(t)})
	if err != nil {
		t.Fatal(err)
	}
	svc := Name{Package: "demo_msgs", Kind: KindSrv, Type: "SetMode"}

	// A service has two message bodies and cannot be looked up as one.
	if _, err := ix.Lookup(svc); err == nil {
		t.Error("Lookup on a service succeeded, want an error")
	}
	msgs, err := ix.Messages(svc)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("Messages(svc) returned %d bodies, want 2", len(msgs))
	}
	if !strings.HasSuffix(msgs[0].Name.Type, "_Request") || !strings.HasSuffix(msgs[1].Name.Type, "_Response") {
		t.Errorf("bodies = %s, %s", msgs[0].Name, msgs[1].Name)
	}

	if p, ok := ix.Path(svc); !ok || !strings.HasSuffix(p, "SetMode.srv") {
		t.Errorf("Path(svc) = %q, %v", p, ok)
	}
	if ix.Has(Name{Package: "demo_msgs", Kind: KindMsg, Type: "Nope"}) {
		t.Error("Has reported a definition that does not exist")
	}
}
