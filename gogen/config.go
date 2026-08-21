// Package gogen turns parsed ROS2 interface definitions into Go source whose
// struct field order matches the CDR wire layout byte for byte.
package gogen

import (
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"github.com/HeoJeongBo/rosidl-gen-go/rosidl"
)

// Config is the rosidl-gen.yaml document.
type Config struct {
	// Out is the output directory, relative to the config file.
	Out string `yaml:"out"`
	// Package is the Go package name written into every generated file.
	Package string `yaml:"package"`

	// SearchPaths are scanned for ament packages, earlier entries winning.
	// `${VAR}` is expanded from the environment.
	SearchPaths []string `yaml:"search_paths"`

	// Imports maps a Go package qualifier used in External to its import path.
	Imports map[string]string `yaml:"imports"`

	// External binds a ROS interface to a Go type that already exists instead of
	// generating one. A msg maps to the full type name; a srv maps to the prefix
	// of its `<prefix>Request` / `<prefix>Response` pair. Values without a `.`
	// qualifier are hand-written in the output package.
	External map[string]string `yaml:"external"`

	// Rename overrides the Go type name derived from an interface name. Only
	// needed to break a collision between two packages.
	Rename map[string]string `yaml:"rename"`

	// Generate lists the interfaces to emit. Nested dependencies are pulled in
	// automatically. Patterns: `pkg/**`, `pkg/msg/*`, `pkg/srv/*`, `pkg/kind/Type`.
	Generate []string `yaml:"generate"`

	// Emit tunes optional parts of the emitted Go source.
	Emit EmitOptions `yaml:"emit"`

	// dir is the directory holding the config file; every path is relative to it.
	dir string
	// extra holds top-level sections the core schema does not know, for
	// extension emitters to claim via Section.
	extra map[string]ast.Node
	// claimed marks extra sections some emitter has decoded.
	claimed map[string]bool
}

// EmitOptions selects optional emissions.
type EmitOptions struct {
	// AsError controls emitting `AsError() error` on a service response that
	// carries the `bool success` + `string message` result pair (the
	// std_srvs/Trigger idiom). Unset means on.
	AsError *bool `yaml:"as_error"`
}

// AsErrorEnabled reports whether AsError() is emitted on service responses
// carrying the success/message pair. Unset means on, so an emitter cannot read
// the AsError field directly and get the right answer.
func (e EmitOptions) AsErrorEnabled() bool { return e.asError() }

func (e EmitOptions) asError() bool { return e.AsError == nil || *e.AsError }

// LoadConfig reads and validates the config at path.
//
// Core keys are decoded strictly. An unknown top-level section is not an
// error here: it is kept for an extension emitter to claim via
// [Config.Section], and the CLI rejects whatever remains unclaimed
// ([Config.UnclaimedSections]), so a typo still fails loudly.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		// os.ReadFile's message names the path but nothing else; a missing
		// config is the most likely first-run failure, so say what to do.
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s: no such config; write one (`rosidl-gen init` prints a skeleton) or pass -config", path)
		}
		return nil, err
	}
	return ParseConfig(b, filepath.Dir(path), path)
}

// ParseConfig decodes a config that is already in memory. dir is what every
// relative path in it resolves against, and name is used in error messages.
//
// LoadConfig is the usual entry point; this exists because a Config built as a
// struct literal has no directory and would silently resolve paths against the
// process working directory.
func ParseConfig(b []byte, dir, name string) (*Config, error) {
	f, err := parser.ParseBytes(b, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if len(f.Docs) == 0 || (len(f.Docs) == 1 && f.Docs[0].Body == nil) {
		return nil, fmt.Errorf("%s: config is empty", name)
	}
	if len(f.Docs) != 1 {
		return nil, fmt.Errorf("%s: expected a single yaml document, got %d", name, len(f.Docs))
	}
	mapping, ok := f.Docs[0].Body.(*ast.MappingNode)
	if !ok {
		return nil, fmt.Errorf("%s: config must be a mapping of keys, e.g. `out: internal/ros`", name)
	}

	path := name
	c := Config{
		dir:     dir,
		extra:   map[string]ast.Node{},
		claimed: map[string]bool{},
	}
	// Duplicate keys, core or extension, are a parse error: goccy rejects them
	// before this loop runs. TestLoadConfigRejectsDuplicateKeys pins that.
	for _, kv := range mapping.Values {
		key := kv.Key.String()
		var dst any
		switch key {
		case "out":
			dst = &c.Out
		case "package":
			dst = &c.Package
		case "search_paths":
			dst = &c.SearchPaths
		case "imports":
			dst = &c.Imports
		case "external":
			dst = &c.External
		case "rename":
			dst = &c.Rename
		case "generate":
			dst = &c.Generate
		case "emit":
			dst = &c.Emit
		default:
			c.extra[key] = kv.Value
			continue
		}
		if err := yaml.NodeToValue(kv.Value, dst, yaml.Strict()); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", path, key, err)
		}
	}

	if err := c.validate(path); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate(path string) error {
	switch {
	case c.Out == "" && c.Package == "":
		return fmt.Errorf("%s: `out` and `package` are required", path)
	case c.Out == "":
		return fmt.Errorf("%s: `out` is required (the directory to write generated files to)", path)
	case c.Package == "":
		return fmt.Errorf("%s: `package` is required (the Go package name for generated files)", path)
	}
	// Caught here rather than by gofmt: an invalid package name otherwise
	// surfaces as a parse error against a generated file the user never wrote.
	if !isGoIdent(c.Package) {
		return fmt.Errorf("%s: package %q is not a valid Go identifier", path, c.Package)
	}
	if len(c.Generate) == 0 {
		return fmt.Errorf("%s: `generate` is empty; list the interfaces to emit, e.g. `- my_msgs/**`", path)
	}

	for _, k := range sortedKeys(c.External) {
		if _, err := rosidl.ParseName(k, ""); err != nil {
			return fmt.Errorf("%s: external %q: %w", path, k, err)
		}
		v := c.External[k]
		q, rest, qualified := strings.Cut(v, ".")
		if qualified {
			if _, known := c.Imports[q]; !known {
				return fmt.Errorf("%s: external %q uses qualifier %q with no `imports` entry", path, k, q)
			}
			if !isGoIdent(rest) {
				return fmt.Errorf("%s: external %q: %q is not a valid Go type name", path, k, rest)
			}
		} else if !isGoIdent(v) {
			return fmt.Errorf("%s: external %q: %q is not a valid Go type name", path, k, v)
		}
	}

	// `rename` was previously validated deep inside New, where the error
	// reached the user with no config path and no mention of the section.
	for _, k := range sortedKeys(c.Rename) {
		if _, err := rosidl.ParseName(k, ""); err != nil {
			return fmt.Errorf("%s: rename %q: %w", path, k, err)
		}
		if v := c.Rename[k]; !isGoIdent(v) {
			return fmt.Errorf("%s: rename %q: %q is not a valid Go identifier", path, k, v)
		}
	}
	return nil
}

// isGoIdent reports whether s can be used as a Go identifier: a non-keyword
// identifier that is exported where the caller needs it to be.
func isGoIdent(s string) bool {
	return token.IsIdentifier(s) && !token.IsKeyword(s)
}

// Section strictly decodes the top-level extension section key into v and
// marks it claimed; ok is false when the config has no such section.
func (c *Config) Section(key string, v any) (ok bool, err error) {
	node, ok := c.extra[key]
	if !ok {
		return false, nil
	}
	c.claimed[key] = true
	if err := yaml.NodeToValue(node, v, yaml.Strict()); err != nil {
		return true, fmt.Errorf("%s: %w", key, err)
	}
	return true, nil
}

// UnclaimedSections returns the unknown top-level sections nothing has
// claimed, sorted. The CLI turns a non-empty result into an error.
func (c *Config) UnclaimedSections() []string {
	var out []string
	for k := range c.extra {
		if !c.claimed[k] {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// Path resolves p against the config file's directory.
func (c *Config) Path(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.dir, p)
}

// ResolvedSearchPaths expands `${VAR}` and makes each path absolute. An entry
// referencing an unset variable, or naming a directory that does not exist, is
// dropped — that keeps an optional path such as `${ROS_ROOT}/share` a no-op on
// a machine without ROS while still using it when one is present.
//
// Dropping silently is what makes an optional path work, but it is also the
// usual reason a `generate` pattern matches nothing. Use [Config.SearchPathReport]
// to see what was dropped and why.
func (c *Config) ResolvedSearchPaths() []string {
	paths, _ := c.SearchPathReport()
	return paths
}

// DroppedPath is a search path that was configured but not scanned.
type DroppedPath struct {
	// Path is the entry as written in the config.
	Path string
	// Reason says why it was skipped, in lower case and without punctuation.
	Reason string
}

// SearchPathReport resolves the search paths and, alongside the ones that will
// be scanned, reports every entry that was dropped and why.
func (c *Config) SearchPathReport() (paths []string, dropped []DroppedPath) {
	for _, raw := range c.SearchPaths {
		var missingVar string
		p := os.Expand(raw, func(k string) string {
			v, ok := os.LookupEnv(k)
			if !ok {
				missingVar = k
			} else if v == "" {
				missingVar = k + " is empty"
			}
			return v
		})
		if missingVar != "" {
			dropped = append(dropped, DroppedPath{raw, "${" + missingVar + "} is not set"})
			continue
		}

		abs := c.Path(p)
		info, err := os.Stat(abs)
		switch {
		case os.IsNotExist(err):
			dropped = append(dropped, DroppedPath{raw, "no such directory: " + abs})
		case err != nil:
			dropped = append(dropped, DroppedPath{raw, err.Error()})
		case !info.IsDir():
			dropped = append(dropped, DroppedPath{raw, "not a directory: " + abs})
		default:
			paths = append(paths, abs)
		}
	}
	return paths, dropped
}

// KnownSections lists the top-level config keys the core schema understands,
// sorted. An extension emitter claims anything else via [Config.Section].
func KnownSections() []string {
	return []string{"emit", "external", "generate", "imports", "out", "package", "rename", "search_paths", "wirelock"}
}
