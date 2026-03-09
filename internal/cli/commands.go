package cli

import "fmt"

type commandHandler func(args []string) error

var commands = map[string]commandHandler{
	"cleanup": commandNotImplemented("cleanup"),
	"create":  commandNotImplemented("create"),
	"delete":  commandNotImplemented("delete"),
	"exec":    commandNotImplemented("exec"),
	"images":  commandNotImplemented("images"),
	"network": commandNotImplemented("network"),
	"ps":      commandNotImplemented("ps"),
	"pull":    commandNotImplemented("pull"),
	"run":     commandNotImplemented("run"),
	"start":   commandNotImplemented("start"),
}

func commandNotImplemented(name string) commandHandler {
	return func(_ []string) error {
		return fmt.Errorf("command %q not implemented yet", name)
	}
}
