package generate

import (
	"io"
	"strings"
	"testing"
)

// The committed goldens under example/ are the only end-to-end check that the
// emitters still produce what was reviewed. CI ran them, but `go test ./...`
// did not, so a developer could land an emission change with a green local
// test run and only learn about it from CI.
func TestExampleGoldensAreCurrent(t *testing.T) {
	err := Run(Options{
		ConfigPath: "../../example/rosidl-gen.yaml",
		Check:      true,
		Out:        io.Discard,
	})
	if err == nil {
		return
	}

	var stale *StaleOutput
	if ok := asStale(err, &stale); ok {
		t.Fatalf("example/out is stale; regenerate it and review the diff:\n%v", err)
	}
	t.Fatalf("checking the example: %v", err)
}

func asStale(err error, target **StaleOutput) bool {
	s, ok := err.(*StaleOutput)
	if ok {
		*target = s
	}
	return ok
}

// The custom-emitter example is checked by its own program, which lives in
// package main and cannot be imported. Assert the pieces this package owns
// instead: that a config carrying a third-party section still loads for the
// read-only commands, and is rejected for generation.
func TestUnclaimedSectionIsStrictOnlyForOutput(t *testing.T) {
	const cfg = "../../example/emitter/rosidl-gen.yaml"

	if _, _, err := load(cfg, false); err != nil {
		t.Errorf("read-only load rejected a config with a third-party section: %v", err)
	}

	_, _, err := load(cfg, true)
	if err == nil {
		t.Fatal("generation accepted a section no emitter claimed")
	}
	for _, want := range []string{"docs", "known sections"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestDiffShowsChangedLines(t *testing.T) {
	got := diffLines(
		[]byte("package demo\n\ntype T struct {\n\tA int\n\tB int\n}\n"),
		[]byte("package demo\n\ntype T struct {\n\tB int\n\tA int\n}\n"),
	)
	if !strings.Contains(got, "- \tA int") || !strings.Contains(got, "+ \tA int") {
		t.Errorf("diff did not show the reorder:\n%s", got)
	}
	// Unchanged context must be present so a reader can locate the change.
	if !strings.Contains(got, "  type T struct {") {
		t.Errorf("diff has no context:\n%s", got)
	}
	// Context windows that touch must merge; a line printed twice reads as two
	// separate changes.
	if n := strings.Count(got, "\tB int"); n != 1 {
		t.Errorf("`B int` appears %d times, want 1 — overlapping hunks were not merged:\n%s", n, got)
	}
}

func TestDiffMergesNothingWhenChangesAreFarApart(t *testing.T) {
	a := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n"
	b := "X\nb\nc\nd\ne\nf\ng\nh\ni\nY\n"
	got := diffLines([]byte(a), []byte(b))
	// Two distant changes, and the untouched middle must not be printed.
	if strings.Contains(got, "  e") || strings.Contains(got, "  f") {
		t.Errorf("diff included unrelated context:\n%s", got)
	}
	if !strings.Contains(got, "- a") || !strings.Contains(got, "- j") {
		t.Errorf("diff missed one of the two changes:\n%s", got)
	}
}

func TestDiffOnIdenticalInputIsEmpty(t *testing.T) {
	if got := diffLines([]byte("a\nb\n"), []byte("a\nb\n")); !strings.Contains(got, "trailing whitespace") {
		t.Errorf("diff of identical input = %q", got)
	}
}
