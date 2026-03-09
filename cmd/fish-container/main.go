package main

import (
	"fmt"
	"os"

	"fish-container/internal/cli"
)

func main() {
	if err := cli.Execute(os.Args[1:]); err != nil {
		if cli.IsNoCommandError(err) {
			return
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
