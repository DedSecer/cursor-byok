//go:build !linux

package main

import (
	"errors"
	"strings"
)

func runInstallCA(args []string) (bool, error) {
	if len(args) > 0 && (args[0] == "install-ca" || args[0] == "remove-ca") {
		return true, errors.New(args[0] + " is only supported on Linux: " + strings.Join(args[1:], " "))
	}
	return false, nil
}
