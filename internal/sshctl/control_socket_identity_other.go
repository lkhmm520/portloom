//go:build !linux

package sshctl

import (
	"errors"
	"os"
)

var errUnsupportedControlSocketIdentity = errors.New("secure SSH ControlMaster socket identity requires Linux O_PATH support")

func validateControlSocketIdentityPlatform() error {
	return errUnsupportedControlSocketIdentity
}

func openControlSocketIdentity(string) (*os.File, error) {
	return nil, errUnsupportedControlSocketIdentity
}
