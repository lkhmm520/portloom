//go:build !linux

package main

import (
	"context"
	"fmt"
	"io"
)

const operationalUsage = "\nOperational cutover commands require Linux.\n"

func isOperational(verb string) bool {
	switch verb {
	case "check", "status", "switch", "settle", "rollback":
		return true
	}
	return false
}

func runOperational(_ context.Context, _ []string, _, stderr io.Writer) int {
	fmt.Fprint(stderr, operationalUsage)
	return 2
}
