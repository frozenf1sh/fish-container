package cli

import (
	"fmt"
	"os"

	"fish-container/internal/runtime"
)

type commandHandler func(args []string) error

var commands = map[string]commandHandler{
	runtime.InitCommandName(): runInitCommand,
	"cleanup":                 runCleanupCommand,
	"create":                  commandNotImplemented("create"),
	"delete":                  commandNotImplemented("delete"),
	"exec":                    commandNotImplemented("exec"),
	"images":                  commandNotImplemented("images"),
	"network":                 commandNotImplemented("network"),
	"ps":                      commandNotImplemented("ps"),
	"pull":                    pullCommand,
	"run":                     runCommand,
	"start":                   commandNotImplemented("start"),
}

func runInitCommand(args []string) error {
	return runtime.ChildMain(args)
}

func runCleanupCommand(_ []string) error {
	_, _ = fmt.Fprintln(os.Stdout, "cleanup: no persisted host resources in stage-2 minimal mode")
	return nil
}

func commandNotImplemented(name string) commandHandler {
	return func(_ []string) error {
		return fmt.Errorf("command %q not implemented yet", name)
	}
}
