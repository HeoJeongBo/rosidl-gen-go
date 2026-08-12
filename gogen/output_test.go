package gogen

import (
	"os"
	"path/filepath"
	"testing"
)

// newOutput builds an Output directly, which is what lets these tests exercise
// Write and Check without standing up a whole interface tree.
func newOutput(outDir string, files map[string][]byte) *Output {
	o := &Output{outDir: outDir, files: map[string][]byte{}}
	for p, b := range files {
		o.paths = append(o.paths, p)
		o.files[p] = b
	}
	return o
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Pruning is the only destructive thing this package does. These three
// invariants are what the README promises, so they are pinned rather than
// argued: emitted files survive, non-.g.go files are untouchable, and a stale
// generated file is removed.
func TestWritePruneInvariants(t *testing.T) {
	dir := t.TempDir()
	handWritten := filepath.Join(dir, "override.go")
	notGenerated := filepath.Join(dir, "notes.md")
	stale := filepath.Join(dir, "gone_msgs.g.go")
	emitted := filepath.Join(dir, "kept_msgs.g.go")

	write(t, handWritten, "package ros\n")
	write(t, notGenerated, "hello\n")
	write(t, stale, "package ros // from a previous run\n")
	write(t, emitted, "stale contents\n")

	out := newOutput(dir, map[string][]byte{emitted: []byte("fresh contents\n")})
	stats, err := out.Write()
	if err != nil {
		t.Fatal(err)
	}

	if !exists(handWritten) {
		t.Error("hand-written .go file was pruned")
	}
	if !exists(notGenerated) {
		t.Error("non-Go file was pruned")
	}
	if exists(stale) {
		t.Error("stale .g.go survived the prune")
	}
	if got := read(t, emitted); got != "fresh contents\n" {
		t.Errorf("emitted file = %q, want the fresh contents", got)
	}
	if stats.Pruned != 1 {
		t.Errorf("Pruned = %d, want 1", stats.Pruned)
	}
	if stats.Files != 1 {
		t.Errorf("Files = %d, want 1", stats.Files)
	}
}

// A subdirectory is not scanned: the prune is one level deep by design, so a
// nested package's generated files belong to whoever generated them.
func TestWriteDoesNotPruneSubdirectories(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "sub", "other.g.go")
	write(t, nested, "package sub\n")

	emitted := filepath.Join(dir, "a.g.go")
	if _, err := newOutput(dir, map[string][]byte{emitted: []byte("x\n")}).Write(); err != nil {
		t.Fatal(err)
	}
	if !exists(nested) {
		t.Error("a .g.go in a subdirectory was pruned")
	}
}

func TestWriteCreatesMissingOutputDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does", "not", "exist")
	emitted := filepath.Join(dir, "a.g.go")

	if _, err := newOutput(dir, map[string][]byte{emitted: []byte("x\n")}).Write(); err != nil {
		t.Fatalf("Write into a missing directory: %v", err)
	}
	if got := read(t, emitted); got != "x\n" {
		t.Errorf("file = %q", got)
	}
}

func TestCheckCategorizesDrift(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.g.go")
	outdated := filepath.Join(dir, "outdated.g.go")
	current := filepath.Join(dir, "current.g.go")
	stale := filepath.Join(dir, "stale.g.go")

	write(t, outdated, "old\n")
	write(t, current, "same\n")
	write(t, stale, "left over\n")

	out := newOutput(dir, map[string][]byte{
		missing:  []byte("new\n"),
		outdated: []byte("new\n"),
		current:  []byte("same\n"),
	})

	err := out.Check()
	drift, ok := err.(*DriftError)
	if !ok {
		t.Fatalf("Check() = %v, want a *DriftError", err)
	}
	if len(drift.Missing) != 1 || drift.Missing[0] != missing {
		t.Errorf("Missing = %v", drift.Missing)
	}
	if len(drift.Outdated) != 1 || drift.Outdated[0] != outdated {
		t.Errorf("Outdated = %v", drift.Outdated)
	}
	if len(drift.Stale) != 1 || drift.Stale[0] != stale {
		t.Errorf("Stale = %v", drift.Stale)
	}

	// Check must not have touched anything.
	if read(t, outdated) != "old\n" || !exists(stale) || exists(missing) {
		t.Error("Check modified the output directory")
	}
}

func TestCheckPassesWhenCurrent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.g.go")
	write(t, p, "same\n")

	if err := newOutput(dir, map[string][]byte{p: []byte("same\n")}).Check(); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

// Check tolerates a not-yet-created output directory; every file is simply
// missing. Write and Check disagreeing here used to be an asymmetry.
func TestCheckOnMissingOutputDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	p := filepath.Join(dir, "a.g.go")

	err := newOutput(dir, map[string][]byte{p: []byte("x\n")}).Check()
	drift, ok := err.(*DriftError)
	if !ok {
		t.Fatalf("Check() = %v, want a *DriftError", err)
	}
	if len(drift.Missing) != 1 || len(drift.Stale) != 0 {
		t.Errorf("drift = %+v, want one missing and no stale", drift)
	}
}

func TestStaleReportsWithoutRemoving(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "gone.g.go")
	write(t, stale, "x\n")

	out := newOutput(dir, map[string][]byte{filepath.Join(dir, "kept.g.go"): []byte("y\n")})
	got, err := out.Stale()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != stale {
		t.Fatalf("Stale() = %v, want [%s]", got, stale)
	}
	if !exists(stale) {
		t.Error("Stale() removed a file; it must only report")
	}
}

// A write must not leave a partially written file behind for a reader that
// races it, and the temporary it goes through must not survive.
func TestWriteLeavesNoTemporaries(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.g.go")
	if _, err := newOutput(dir, map[string][]byte{p: []byte("x\n")}).Write(); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.g.go" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("output directory = %v, want just a.g.go", names)
	}
}

func TestDriftErrorProblems(t *testing.T) {
	e := &DriftError{Missing: []string{"m"}, Outdated: []string{"o"}, Stale: []string{"s"}}
	got := e.Problems()
	want := []string{"missing:  m", "outdated: o", "stale:    s"}
	if len(got) != len(want) {
		t.Fatalf("Problems() = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Problems()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
