package main

import (
	"fmt"
	"os"

	cliconfiguration "github.com/bochkarevart/1c-selection/configuration/cli"
)

func main() {
	command := cliconfiguration.NewCommand()
	if err := command.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
