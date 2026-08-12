package gogen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HeoJeongBo/rosidl-gen-go/rosidl"
)

// fixture writes an ament package plus a config over it, and returns the
// config path.
func fixture(t *testing.T, defs map[string]string, configBody string) string {
	t.Helper()
	dir := t.TempDir()
	pkg := filepath.Join(dir, "msg", "demo_msgs")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `<?xml version="1.0"?><package format="3"><name>demo_msgs</name></package>`
	if err := os.WriteFile(filepath.Join(pkg, "package.xml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range defs {
		p := filepath.Join(pkg, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath := filepath.Join(dir, "rosidl-gen.yaml")
	if err := os.WriteFile(cfgPath, []byte(configBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

const baseConfig = "out: out\npackage: demo\nsearch_paths: [msg]\ngenerate: [demo_msgs/**]\n"

func resolveFixture(t *testing.T, defs map[string]string, configBody string) (*Generator, error) {
	t.Helper()
	cfg, err := LoadConfig(fixture(t, defs, configBody))
	if err != nil {
		return nil, err
	}
	g, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return g, g.Resolve()
}

// The constructor a defaulted message gets is part of the package namespace.
// Without claiming it, `Foo` with defaults alongside a message named `NewFoo`
// emitted a duplicate declaration that gofmt cannot see — format.Source parses,
// it does not type-check — so the tool reported success and the consumer's
// build broke.
func TestResolveDetectsConstructorCollision(t *testing.T) {
	_, err := resolveFixture(t, map[string]string{
		"msg/Thing.msg":    "float64 rate 10.0\n",
		"msg/NewThing.msg": "uint8 x\n",
	}, baseConfig)
	if err == nil {
		t.Fatal("want a collision error for NewThing vs New(Thing)")
	}
	if !strings.Contains(err.Error(), "NewThing") {
		t.Errorf("error %q does not name the colliding identifier", err)
	}
}

// Only a message that actually declares a non-zero default gets a constructor,
// so NewThing is fine next to a Thing without one.
func TestResolveAllowsNewPrefixWithoutDefaults(t *testing.T) {
	if _, err := resolveFixture(t, map[string]string{
		"msg/Thing.msg":    "uint8 x\n",
		"msg/NewThing.msg": "uint8 x\n",
	}, baseConfig); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

// registry.g.go declares GeneratedTypes unconditionally.
func TestResolveDetectsRegistryCollision(t *testing.T) {
	_, err := resolveFixture(t, map[string]string{
		"msg/GeneratedTypes.msg": "uint8 x\n",
	}, baseConfig)
	if err == nil || !strings.Contains(err.Error(), "GeneratedTypes") {
		t.Fatalf("err = %v, want a GeneratedTypes collision", err)
	}
}

func TestResolveDetectsConstantCollision(t *testing.T) {
	// Thing.MODE_A becomes ThingModeA; a message named ThingModeA collides.
	_, err := resolveFixture(t, map[string]string{
		"msg/Thing.msg":      "uint8 MODE_A=0\nuint8 mode\n",
		"msg/ThingModeA.msg": "uint8 x\n",
	}, baseConfig)
	if err == nil || !strings.Contains(err.Error(), "ThingModeA") {
		t.Fatalf("err = %v, want a ThingModeA collision", err)
	}
}

func TestRenameBreaksACollision(t *testing.T) {
	defs := map[string]string{
		"msg/Thing.msg":    "float64 rate 10.0\n",
		"msg/NewThing.msg": "uint8 x\n",
	}
	g, err := resolveFixture(t, defs, baseConfig+"rename:\n  demo_msgs/msg/NewThing: RenamedThing\n")
	if err != nil {
		t.Fatalf("Resolve with a rename: %v", err)
	}
	got, ok := g.GoName(rosidl.Name{Package: "demo_msgs", Kind: rosidl.KindMsg, Type: "NewThing"})
	if !ok || got != "RenamedThing" {
		t.Errorf("GoName = %q, %v; want RenamedThing", got, ok)
	}
}

func TestRenameValidation(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"bad value", baseConfig + "rename:\n  demo_msgs/msg/Thing: not-an-ident\n", "not a valid Go identifier"},
		{"bad key", baseConfig + "rename:\n  Thing: Renamed\n", "rename \"Thing\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfig(fixture(t, map[string]string{"msg/Thing.msg": "uint8 x\n"}, tc.body))
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

// Provenance is what `explain` reads: a direct match names the pattern, a
// nested reference names the field that pulled it in.
func TestProvenance(t *testing.T) {
	g, err := resolveFixture(t, map[string]string{
		"msg/State.msg":   "Reading recent\n",
		"msg/Reading.msg": "float64 value\n",
	}, "out: out\npackage: demo\nsearch_paths: [msg]\ngenerate: [demo_msgs/msg/State]\n")
	if err != nil {
		t.Fatal(err)
	}

	state := rosidl.Name{Package: "demo_msgs", Kind: rosidl.KindMsg, Type: "State"}
	reading := rosidl.Name{Package: "demo_msgs", Kind: rosidl.KindMsg, Type: "Reading"}

	if p, ok := g.Provenance(state); !ok || p.Pattern != "demo_msgs/msg/State" {
		t.Errorf("State provenance = %+v, %v", p, ok)
	}
	p, ok := g.Provenance(reading)
	if !ok || p.Parent != state || p.Field != "recent" {
		t.Errorf("Reading provenance = %+v, %v; want referenced by State.recent", p, ok)
	}
	if _, ok := g.Provenance(rosidl.Name{Package: "demo_msgs", Kind: rosidl.KindMsg, Type: "Absent"}); ok {
		t.Error("Provenance reported an interface that was never selected")
	}
}

func TestExternalsAreNotSelectedButAreReported(t *testing.T) {
	body := baseConfig + "imports:\n  ros: example.com/ros\nexternal:\n  other_msgs/msg/Header: ros.Header\n"
	g, err := resolveFixture(t, map[string]string{"msg/Thing.msg": "other_msgs/Header header\n"}, body)
	if err != nil {
		t.Fatal(err)
	}

	header := rosidl.Name{Package: "other_msgs", Kind: rosidl.KindMsg, Type: "Header"}
	for _, n := range g.Selected() {
		if n == header {
			t.Fatal("an external interface was selected for generation")
		}
	}
	ext := g.Externals()
	if len(ext) != 1 || ext[0] != header {
		t.Fatalf("Externals() = %v", ext)
	}
	if got, ok := g.ExternalType(header); !ok || got != "ros.Header" {
		t.Errorf("ExternalType = %q, %v", got, ok)
	}
}

func TestGoTypeRendersArrayShapes(t *testing.T) {
	g, err := resolveFixture(t, map[string]string{
		"msg/Thing.msg":   "uint8 a\nfloat32[3] b\nuint32[] c\nReading[<=2] d\nstring<=8 e\n",
		"msg/Reading.msg": "float64 value\n",
	}, baseConfig)
	if err != nil {
		t.Fatal(err)
	}
	m, err := g.Index().Lookup(rosidl.Name{Package: "demo_msgs", Kind: rosidl.KindMsg, Type: "Thing"})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"uint8", "[3]float32", "[]uint32", "[]Reading", "string"}
	for i, f := range m.Fields {
		got, quals, err := g.GoType(f.Type)
		if err != nil {
			t.Fatalf("GoType(%s): %v", f.Name, err)
		}
		if got != want[i] {
			t.Errorf("GoType(%s) = %q, want %q", f.Name, got, want[i])
		}
		if len(quals) != 0 {
			t.Errorf("GoType(%s) wanted imports %v for a local type", f.Name, quals)
		}
	}
}

func TestGoTypeReportsExternalQualifier(t *testing.T) {
	body := baseConfig + "imports:\n  ros: example.com/ros\nexternal:\n  other_msgs/msg/Header: ros.Header\n"
	g, err := resolveFixture(t, map[string]string{"msg/Thing.msg": "other_msgs/Header header\n"}, body)
	if err != nil {
		t.Fatal(err)
	}
	m, err := g.Index().Lookup(rosidl.Name{Package: "demo_msgs", Kind: rosidl.KindMsg, Type: "Thing"})
	if err != nil {
		t.Fatal(err)
	}

	got, quals, err := g.GoType(m.Fields[0].Type)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ros.Header" {
		t.Errorf("GoType = %q", got)
	}
	if len(quals) != 1 || quals[0] != "ros" {
		t.Errorf("qualifiers = %v, want [ros]", quals)
	}
}

func TestFieldNote(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"plain", "uint8 a\n", ""},
		{"bounded sequence", "uint8[<=4] a\n", " // <=4 entries"},
		{"bounded string", "string<=8 a\n", " // <=8 bytes"},
		{"default", "bool a true\n", " // default true"},
		{"combined", "string<=8 a \"x\"\n", " // <=8 bytes, default \"x\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := rosidl.ParseMessage(
				rosidl.Name{Package: "p", Kind: rosidl.KindMsg, Type: "T"}, "T.msg", tc.body)
			if err != nil {
				t.Fatal(err)
			}
			if got := FieldNote(m.Fields[0]); got != tc.want {
				t.Errorf("FieldNote = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPascal(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"mode", "Mode"},
		{"state_of_charge", "StateOfCharge"},
		{"MODE_A", "ModeA"},
		{"http_url", "HttpUrl"},
		{"mesh_uvindex", "MeshUvindex"},
		{"a__b", "AB"},
		{"", ""},
	} {
		if got := Pascal(tc.in); got != tc.want {
			t.Errorf("Pascal(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCommentBlock(t *testing.T) {
	if got := CommentBlock("\t", nil); got != "" {
		t.Errorf("CommentBlock(nil) = %q, want empty so callers can concatenate", got)
	}
	got := CommentBlock("", []string{"one", "", "two"})
	if got != "// one\n//\n// two\n" {
		t.Errorf("CommentBlock = %q", got)
	}
}

func TestFormatFileExcerptsRatherThanDumping(t *testing.T) {
	var src strings.Builder
	src.WriteString("package demo\n\n")
	for i := 0; i < 200; i++ {
		src.WriteString("// filler\n")
	}
	src.WriteString("type Broken struct {\n") // never closed

	_, err := FormatFile("demo.g.go", []byte(src.String()))
	if err == nil {
		t.Fatal("want a gofmt error")
	}
	if n := strings.Count(err.Error(), "\n"); n > 12 {
		t.Errorf("error spans %d lines; it should excerpt, not dump the file", n)
	}
	if !strings.Contains(err.Error(), "demo.g.go") {
		t.Errorf("error %q does not name the file", err)
	}
}
