package main

import (
	"context"
	"errors"
	"flag"

	"verity/internal/verification"
)

func internalCommand(args []string) error {
	if len(args) == 0 || args[0] != "egress-relay" {
		return errors.New("usage: verity internal egress-relay --socket PATH --listen 127.0.0.1:18080")
	}
	flags := flag.NewFlagSet("egress-relay", flag.ContinueOnError)
	socket := flags.String("socket", "", "host broker Unix socket")
	listen := flags.String("listen", "", "container loopback proxy address")
	readyFile := flags.String("ready-file", "", "container readiness marker")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected egress relay arguments")
	}
	return verification.RunEgressRelay(context.Background(), *socket, *listen, *readyFile)
}
