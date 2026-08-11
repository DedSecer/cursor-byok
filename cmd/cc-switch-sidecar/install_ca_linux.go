//go:build linux

package main

import (
	"flag"
	"fmt"
	"os"

	cursorhost "cursor/internal/cursor"
)

func runInstallCA(args []string) (bool, error) {
	if len(args) == 0 || (args[0] != "install-ca" && args[0] != "remove-ca") {
		return false, nil
	}
	action := args[0]
	flags := flag.NewFlagSet(action, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var certPath string
	var backend string
	flags.StringVar(&certPath, "cert", "", "CA certificate path")
	flags.StringVar(&backend, "backend", "", "Linux trust backend")
	if err := flags.Parse(args[1:]); err != nil {
		return true, err
	}
	if certPath == "" || backend == "" {
		return true, fmt.Errorf("--cert and --backend are required")
	}
	if action == "remove-ca" {
		return true, cursorhost.RemoveLinuxCA(certPath, backend)
	}
	return true, cursorhost.InstallLinuxCA(certPath, backend)
}
