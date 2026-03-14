package cli

import (
	"flag"
	"fmt"
	"os"

	"fish-container/internal/runtime"
)

func runCommand(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var rootfs string
	var hostname string
	fs.StringVar(&rootfs, "rootfs", "", "local rootfs path")
	fs.StringVar(&hostname, "hostname", "fish-container", "container hostname")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse run flags: %w", err)
	}

	return runtime.Run(runtime.RunSpec{
		Rootfs:   rootfs,
		Hostname: hostname,
		Command:  fs.Args(),
	})
}
