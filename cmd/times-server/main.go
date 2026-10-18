package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	serverconfiguration "github.com/bochkarevart/1c-selection/configuration/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	command := serverconfiguration.NewCommand(serverconfiguration.Serve)
	if err := command.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
