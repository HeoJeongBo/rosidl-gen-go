package cmd

import (
	"flag"
	"fmt"

	"github.com/HeoJeongBo/rosidl-gen-go/cmd/generate"
)

// newFlagSet builds a flag set that prints a usage line naming the subcommand,
// rather than the temporary binary path `go run` produces for os.Args[0].
func newFlagSet(name, synopsis string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() {
		w := fs.Output()
		fmt.Fprintf(w, "Usage: rosidl-gen %s\n\n", synopsis)
		fs.PrintDefaults()
	}
	return fs
}

// parse reports flagParseError when the flag package already printed the
// problem, and rejects leftover positional arguments — silently ignoring them
// meant `rosidl-gen my.yaml` generated from the default config and claimed
// success.
func parse(fs *flag.FlagSet, args []string, wantArgs int) error {
	if err := fs.Parse(args); err != nil {
		return flagParseError
	}
	if fs.NArg() > wantArgs {
		extra := fs.Args()[wantArgs:]
		return fmt.Errorf("unexpected argument %q; %s takes no positional arguments (did you mean -config %s?)",
			extra[0], fs.Name(), extra[0])
	}
	if fs.NArg() < wantArgs {
		return fmt.Errorf("%s needs %d argument(s); run `rosidl-gen %s -h`", fs.Name(), wantArgs, fs.Name())
	}
	return nil
}

func configFlag(fs *flag.FlagSet) *string {
	return fs.String("config", "rosidl-gen.yaml", "path to the generator config")
}

func runGenerate(args []string) error {
	fs := newFlagSet("generate", "[flags]")
	configPath := configFlag(fs)
	verbose := fs.Bool("v", false, "report the resolved search paths and interface set")
	check := fs.Bool("check", false, "verify the on-disk output matches; write nothing")
	dryRun := fs.Bool("n", false, "report what would be written and removed; write nothing")
	version := fs.Bool("version", false, "print version information and exit")
	if err := parse(fs, args, 0); err != nil {
		return err
	}
	if *version {
		return runVersion(nil)
	}
	if *check && *dryRun {
		return fmt.Errorf("-check and -n are mutually exclusive")
	}

	return generate.Run(generate.Options{
		ConfigPath: *configPath,
		Verbose:    *verbose,
		Check:      *check,
		DryRun:     *dryRun,
	})
}

func runNames(args []string) error {
	fs := newFlagSet("names", "names [flags]")
	configPath := configFlag(fs)
	dryRun := fs.Bool("n", false, "report what would be written; write nothing")
	if err := parse(fs, args, 0); err != nil {
		return err
	}
	return generate.Names(generate.Options{ConfigPath: *configPath, DryRun: *dryRun})
}

func runList(args []string) error {
	fs := newFlagSet("list", "list [flags]")
	configPath := configFlag(fs)
	if err := parse(fs, args, 0); err != nil {
		return err
	}
	return generate.List(generate.Options{ConfigPath: *configPath})
}

func runExplain(args []string) error {
	fs := newFlagSet("explain", "explain [flags] <pkg/kind/Type>")
	configPath := configFlag(fs)
	if err := parse(fs, args, 1); err != nil {
		return err
	}
	return generate.Explain(generate.Options{ConfigPath: *configPath}, fs.Arg(0))
}
