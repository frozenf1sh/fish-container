package cli

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var errNoCommand = errors.New("no command provided")

func IsNoCommandError(err error) bool {
	return errors.Is(err, errNoCommand)
}

// Execute routes command-line input to command handlers.
func Execute(args []string) error {
	if len(args) == 0 {
		printHelp()
		return errNoCommand
	}

	name := args[0]
	handler, ok := commands[name]
	if !ok {
		printHelp()
		return fmt.Errorf("unknown command: %s", name)
	}

	return handler(args[1:])
}

func printHelp() {
	names := make([]string, 0, len(commands))
	for name := range commands {
		if strings.HasPrefix(name, "__") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Println("fish-container: stage-1 command scaffold")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  fish-container <command> [args]")
	fmt.Println()
	fmt.Println("Commands:")
	for _, name := range names {
		fmt.Printf("  %s\n", name)
	}
}
