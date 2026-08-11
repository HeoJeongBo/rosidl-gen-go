package gogen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Output is the complete result of one generation run: every file that run
// would write, keyed by the absolute path it belongs at. It is produced by
// [Generator.Run] and consumed either by [Output.Write] or, for a CI drift
// gate, by [Output.Check].
type Output struct {
	outDir     string
	files      map[string][]byte
	paths      []string // emission order; deterministic
	interfaces int
}

// OutDir returns the resolved output directory stale files are pruned from.
func (o *Output) OutDir() string { return o.outDir }

// Paths returns the absolute path of every file in emission order.
func (o *Output) Paths() []string { return append([]string(nil), o.paths...) }

// Files returns the rendered files keyed by absolute path.
func (o *Output) Files() map[string][]byte {
	out := make(map[string][]byte, len(o.files))
	for p, b := range o.files {
		out[p] = append([]byte(nil), b...)
	}
	return out
}

// WriteStats summarizes what a Write did.
type WriteStats struct {
	// Interfaces is the number of resolved ROS interfaces.
	Interfaces int
	// Files is the number of files written.
	Files int
	// Pruned is the number of stale *.g.go files removed from OutDir.
	Pruned int
	// OutDir is the resolved output directory.
	OutDir string
}

// Write writes every file and prunes stale *.g.go files from the output
// directory (non-recursively; files this run emitted are never pruned, and
// nothing without the .g.go suffix is ever touched, so hand-written code in
// the same directory is safe).
func (o *Output) Write() (WriteStats, error) {
	for _, p := range o.paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return WriteStats{}, err
		}
		if err := os.WriteFile(p, o.files[p], 0o644); err != nil {
			return WriteStats{}, err
		}
	}

	pruned, err := pruneStale(o.outDir, o.files)
	if err != nil {
		return WriteStats{}, err
	}
	return WriteStats{
		Interfaces: o.interfaces,
		Files:      len(o.paths),
		Pruned:     pruned,
		OutDir:     o.outDir,
	}, nil
}

// Check compares the on-disk output against what this run would write, and
// writes nothing. The generated files are tracked by git, so a CI build runs
// this instead of regenerating: a definition that changed without a generator
// run fails the build rather than being silently papered over by a fresh
// generation nobody reviewed.
//
// The comparison is byte for byte, so it covers constant values and doc
// comments too — neither of which a reflection-based layout check looks at.
// Drift is reported as a [*DriftError].
func (o *Output) Check() error {
	drift := &DriftError{}
	for _, p := range o.paths {
		got, err := os.ReadFile(p)
		switch {
		case os.IsNotExist(err):
			drift.Missing = append(drift.Missing, p)
			continue
		case err != nil:
			return err
		}
		if !bytes.Equal(got, o.files[p]) {
			drift.Outdated = append(drift.Outdated, p)
		}
	}

	entries, err := os.ReadDir(o.outDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".g.go") {
			continue
		}
		p := filepath.Join(o.outDir, e.Name())
		if _, ours := o.files[p]; !ours {
			drift.Stale = append(drift.Stale, p)
		}
	}

	if len(drift.Missing)+len(drift.Outdated)+len(drift.Stale) > 0 {
		return drift
	}
	return nil
}

// DriftError is Check's failure: the on-disk output does not match what the
// run would write. It carries the categorized paths and no remediation text —
// the caller knows how its output gets regenerated.
type DriftError struct {
	// Missing files should exist but do not.
	Missing []string
	// Outdated files exist with different bytes.
	Outdated []string
	// Stale *.g.go files exist in the output directory but would not be
	// written by this run.
	Stale []string
}

func (e *DriftError) Error() string {
	return fmt.Sprintf("output drift: %d missing, %d outdated, %d stale",
		len(e.Missing), len(e.Outdated), len(e.Stale))
}

// Problems renders one aligned line per drifted path, in check order.
func (e *DriftError) Problems() []string {
	var out []string
	for _, p := range e.Missing {
		out = append(out, "missing:  "+p)
	}
	for _, p := range e.Outdated {
		out = append(out, "outdated: "+p)
	}
	for _, p := range e.Stale {
		out = append(out, "stale:    "+p)
	}
	return out
}

// pruneStale removes *.g.go left behind by a previous run whose config listed
// interfaces this run no longer generates. Without it a dropped package keeps
// compiling from a file nothing regenerates.
func pruneStale(dir string, keep map[string][]byte) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".g.go") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if _, ours := keep[p]; ours {
			continue
		}
		if err := os.Remove(p); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
