package cmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/HeoJeongBo/rosidl-gen-go/cmd/generate"
)

// Main runs the rosidl-gen CLI and exits the process on failure.
func Main() {
	configPath := flag.String("config", "rosidl-gen.yaml", "path to the generator config")
	verbose := flag.Bool("v", false, "list the resolved interface set")
	check := flag.Bool("check", false, "verify the on-disk output matches; write nothing")
	flag.Parse()

	err := generate.Run(generate.Options{
		ConfigPath: *configPath,
		Verbose:    *verbose,
		Check:      *check,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "rosidl-gen: %v\n", err)
		os.Exit(1)
	}
}
