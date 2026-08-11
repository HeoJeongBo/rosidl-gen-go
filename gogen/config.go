// Package gogen turns parsed ROS2 interface definitions into Go source whose
// struct field order matches the CDR wire layout byte for byte.
package gogen

import (
	"fmt"
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
		return nil, err
	}

	f, err := parser.ParseBytes(b, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(f.Docs) != 1 || f.Docs[0].Body == nil {
		return nil, fmt.Errorf("%s: expected a single yaml document", path)
	}
	mapping, ok := f.Docs[0].Body.(*ast.MappingNode)
	if !ok {
		return nil, fmt.Errorf("%s: config must be a mapping", path)
	}

	c := Config{
		dir:     filepath.Dir(path),
		extra:   map[string]ast.Node{},
		claimed: map[string]bool{},
	}
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
			if _, dup := c.extra[key]; dup {
				return nil, fmt.Errorf("%s: duplicate section %q", path, key)
			}
			c.extra[key] = kv.Value
			continue
		}
		if err := yaml.NodeToValue(kv.Value, dst, yaml.Strict()); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", path, key, err)
		}
	}

	if c.Out == "" || c.Package == "" {
		return nil, fmt.Errorf("%s: `out` and `package` are required", path)
	}
	if len(c.Generate) == 0 {
		return nil, fmt.Errorf("%s: `generate` is empty", path)
	}
	for _, k := range sortedKeys(c.External) {
		if _, err := rosidl.ParseName(k, ""); err != nil {
			return nil, fmt.Errorf("%s: external %q: %w", path, k, err)
		}
		if q, _, ok := strings.Cut(c.External[k], "."); ok {
			if _, known := c.Imports[q]; !known {
				return nil, fmt.Errorf("%s: external %q uses qualifier %q with no `imports` entry", path, k, q)
			}
		}
	}
	return &c, nil
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
func (c *Config) ResolvedSearchPaths() []string {
	out := make([]string, 0, len(c.SearchPaths))
	for _, raw := range c.SearchPaths {
		unset := false
		p := os.Expand(raw, func(k string) string {
			v, ok := os.LookupEnv(k)
			if !ok || v == "" {
				unset = true
			}
			return v
		})
		if unset {
			continue
		}
		p = c.Path(p)
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			continue
		}
		out = append(out, p)
	}
	return out
}
