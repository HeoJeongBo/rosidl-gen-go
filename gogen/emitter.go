package gogen

import (
	"fmt"
	"path/filepath"
)

// Emitter renders additional files from a resolved generation. The core
// output — one file per ROS package plus the registry — is not an Emitter and
// cannot be removed: extensibility here is additive. An emitter sees the whole
// resolved world through the Generator: [Generator.Selected],
// [Generator.GoName], [Generator.Index] for the parsed definitions, and
// [Generator.Config] for its own configuration section.
//
// Unlike core files, whose File.Name resolves inside the `out` directory, an
// emitter's File.Name is resolved relative to the config file's directory, so
// an emitter can place output anywhere the config can express.
type Emitter interface {
	// Name identifies the emitter in errors.
	Name() string
	// Emit renders the emitter's files.
	Emit(g *Generator) ([]File, error)
}

// Run emits the core output plus every extra emitter and resolves each file to
// its absolute path. Two producers claiming the same path is an error.
func (g *Generator) Run(extra ...Emitter) (*Output, error) {
	files, err := g.Emit()
	if err != nil {
		return nil, err
	}

	out := &Output{
		outDir:     g.cfg.Path(g.cfg.Out),
		files:      map[string][]byte{},
		interfaces: len(g.selected),
	}
	producer := map[string]string{}
	add := func(who, path string, body []byte) error {
		if prev, taken := producer[path]; taken {
			return fmt.Errorf("%s and %s both emit %s", prev, who, path)
		}
		producer[path] = who
		out.paths = append(out.paths, path)
		out.files[path] = body
		return nil
	}

	for _, f := range files {
		if err := add("core", filepath.Join(out.outDir, f.Name), f.Body); err != nil {
			return nil, err
		}
	}

	for _, e := range extra {
		files, err := e.Emit(g)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		for _, f := range files {
			if err := add(e.Name(), g.cfg.Path(f.Name), f.Body); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// Run resolves cfg's interface set and emits everything in one call: the
// programmatic equivalent of the CLI. Use [Output.Write] on the result, or
// [Output.Check] for -check semantics.
func Run(cfg *Config, extra ...Emitter) (*Output, error) {
	g, err := New(cfg)
	if err != nil {
		return nil, err
	}
	if err := g.Resolve(); err != nil {
		return nil, err
	}
	return g.Run(extra...)
}
