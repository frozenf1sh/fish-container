package cli

import (
	"fmt"
	"os"

	"fish-container/internal/runtime"
)

type commandHandler func(args []string) error

var commands = map[string]commandHandler{
	runtime.InitCommandName(): runInitCommand,
	startDaemonCommandName:    startDaemonCommand,
	"cleanup":                 runCleanupCommand,
	"create":                  createCommand,
	"delete":                  deleteCommand,
	"exec":                    commandNotImplemented("exec"),
	"images":                  imagesCommand,
	"network":                 commandNotImplemented("network"),
	"ps":                      commandNotImplemented("ps"),
	"pull":                    pullCommand,
	"run":                     runCommand,
	"start":                   startCommand,
	"state":                   stateCommand,
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
