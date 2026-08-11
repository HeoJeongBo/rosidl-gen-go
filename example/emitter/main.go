// Command emitter-example adds an artifact to rosidl-gen without forking it.
//
// The core output — one Go file per ROS package, plus the registry — is fixed.
// Anything else is a [gogen.Emitter]: implement the interface, read your
// options from your own top-level config section, and compose it with
// [gogen.Run]. That is the whole extension contract, and this file is the
// smallest program that exercises all of it.
//
// Usage:
//
//	go run ./example/emitter -config example/emitter/rosidl-gen.yaml
//	go run ./example/emitter -config example/emitter/rosidl-gen.yaml -check
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/HeoJeongBo/rosidl-gen-go/gogen"
)

// docsConfig is the `docs:` section of this example's rosidl-gen.yaml. The core
// schema does not know the key; LoadConfig keeps it aside and this program
// claims it below.
type docsConfig struct {
	// Out is the markdown file to write, relative to the config file.
	Out string `yaml:"out"`
}

// mdIndex writes a markdown table of every interface the generator resolved.
type mdIndex struct {
	c docsConfig
}

// Name identifies the emitter in errors.
func (mdIndex) Name() string { return "mdindex" }

// Emit renders the table. An emitter sees the whole resolved generation through
// the Generator: the selected interface set, the Go identifier assigned to each
// one, and the parsed definitions behind them.
func (e mdIndex) Emit(g *gogen.Generator) ([]gogen.File, error) {
	var b strings.Builder
	b.WriteString("# Generated interfaces\n\n")
	b.WriteString("| ROS interface | Go type | Fields |\n")
	b.WriteString("| --- | --- | --- |\n")

	for _, n := range g.Selected() {
		ident, ok := g.GoName(n)
		if !ok {
			return nil, fmt.Errorf("%s: no Go identifier assigned", n)
		}
		// A service resolves to two message bodies, which the generator names
		// <ident>Request and <ident>Response; counting across both is why this
		// walks Messages rather than Message.
		msgs, err := g.Index().Messages(n)
		if err != nil {
			return nil, err
		}
		fields := 0
		for _, m := range msgs {
			fields += len(m.Fields)
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %d |\n", n, ident, fields)
	}

	// File.Name is resolved relative to the config file, not to `out`, so an
	// emitter can place its artifact anywhere the config can express.
	return []gogen.File{{Name: e.c.Out, Body: []byte(b.String())}}, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "emitter-example: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "rosidl-gen.yaml", "path to the generator config")
	check := flag.Bool("check", false, "verify the on-disk output matches; write nothing")
	flag.Parse()

	cfg, err := gogen.LoadConfig(*configPath)
	if err != nil {
		return err
	}

	var c docsConfig
	ok, err := cfg.Section("docs", &c)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s: no `docs` section", *configPath)
	}
	if c.Out == "" {
		return fmt.Errorf("%s: docs: `out` is required", *configPath)
	}
	// Claiming is what keeps the config strict: whatever no emitter took is a
	// typo, or a section this build does not know how to honour.
	if unknown := cfg.UnclaimedSections(); len(unknown) > 0 {
		return fmt.Errorf("%s: unknown config section(s): %s", *configPath, strings.Join(unknown, ", "))
	}

	out, err := gogen.Run(cfg, mdIndex{c: c})
	if err != nil {
		return err
	}

	if *check {
		err := out.Check()
		// Drift comes back typed, carrying the categorized paths and no
		// remediation text, so each caller can phrase its own.
		var drift *gogen.DriftError
		if errors.As(err, &drift) {
			return fmt.Errorf("output is stale; re-run without -check\n\t%s",
				strings.Join(drift.Problems(), "\n\t"))
		}
		if err != nil {
			return err
		}
		fmt.Printf("emitter-example: %d files in %s are up to date\n", len(out.Paths()), out.OutDir())
		return nil
	}

	stats, err := out.Write()
	if err != nil {
		return err
	}
	fmt.Printf("emitter-example: %d interfaces -> %d files in %s\n",
		stats.Interfaces, stats.Files, stats.OutDir)
	return nil
}
