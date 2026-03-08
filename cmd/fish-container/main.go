package main

import (
	"flag"
	"fmt"
)

func main() {
	flag.Usage = func() {
		fmt.Println("fish-container: OCI runtime learning project")
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  fish-container <command> [flags]")
		fmt.Println()
		fmt.Println("Use stage-1 scaffold first, commands will be added incrementally.")
	}

	flag.Parse()
	flag.Usage()
}
