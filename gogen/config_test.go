package gogen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "rosidl-gen.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const minimalConfig = `
out: out
package: demo
search_paths: [msg]
generate: [demo_msgs/**]
`

func TestLoadConfigUnknownSectionIsKeptNotRejected(t *testing.T) {
	p := writeConfig(t, t.TempDir(), minimalConfig+`
myplugin:
  flavor: spicy
`)
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}

	got := c.UnclaimedSections()
	if len(got) != 1 || got[0] != "myplugin" {
		t.Fatalf("UnclaimedSections = %v, want [myplugin]", got)
	}

	var section struct {
		Flavor string `yaml:"flavor"`
	}
	ok, err := c.Section("myplugin", &section)
	if err != nil || !ok {
		t.Fatalf("Section = %v, %v", ok, err)
	}
	if section.Flavor != "spicy" {
		t.Fatalf("Flavor = %q, want spicy", section.Flavor)
	}
	if left := c.UnclaimedSections(); len(left) != 0 {
		t.Fatalf("after claim, UnclaimedSections = %v, want none", left)
	}
}

func TestSectionAbsent(t *testing.T) {
	p := writeConfig(t, t.TempDir(), minimalConfig)
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	var v struct{}
	if ok, err := c.Section("nope", &v); ok || err != nil {
		t.Fatalf("Section(absent) = %v, %v; want false, nil", ok, err)
	}
}

func TestSectionRejectsUnknownKeys(t *testing.T) {
	p := writeConfig(t, t.TempDir(), minimalConfig+`
myplugin:
  flavour: typo
`)
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	var section struct {
		Flavor string `yaml:"flavor"`
	}
	if _, err := c.Section("myplugin", &section); err == nil {
		t.Fatal("Section decoded an unknown key without error")
	}
}

func TestLoadConfigCoreKeysStayStrict(t *testing.T) {
	p := writeConfig(t, t.TempDir(), `
out: out
package: demo
generate: [x/**]
emit:
  as_eror: true
`)
	if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), "emit") {
		t.Fatalf("typo inside a core section did not fail: %v", err)
	}
}

func TestAsErrorDefaultsOn(t *testing.T) {
	var e EmitOptions
	if !e.asError() {
		t.Fatal("asError zero value should be on")
	}
	off := false
	e.AsError = &off
	if e.asError() {
		t.Fatal("asError(false) should be off")
	}
}

func TestAsErrorDisabledOmitsMethod(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "msg", "demo_msgs")
	if err := os.MkdirAll(filepath.Join(pkg, "srv"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(pkg, "package.xml"):        `<package format="3"><name>demo_msgs</name></package>`,
		filepath.Join(pkg, "srv", "Trigger.srv"): "---\nbool success\nstring message\n",
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	emit := func(t *testing.T, cfgBody string) string {
		t.Helper()
		cfg, err := LoadConfig(writeConfig(t, dir, cfgBody))
		if err != nil {
			t.Fatal(err)
		}
		g, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.Resolve(); err != nil {
			t.Fatal(err)
		}
		files, err := g.Emit()
		if err != nil {
			t.Fatal(err)
		}
		var all strings.Builder
		for _, f := range files {
			all.Write(f.Body)
		}
		return all.String()
	}

	if src := emit(t, minimalConfig); !strings.Contains(src, "AsError") {
		t.Fatal("default emission should include AsError")
	}
	if src := emit(t, minimalConfig+"emit:\n  as_error: false\n"); strings.Contains(src, "AsError") {
		t.Fatal("emit.as_error: false should omit AsError")
	}
}
