//go:build linux

package sshctl

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func validateControlSocketIdentityPlatform() error {
	return nil
}

func openControlSocketIdentity(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	identity := os.NewFile(uintptr(fd), path)
	if identity == nil {
		_ = unix.Close(fd)
		return nil, errors.New("wrap SSH ControlMaster socket identity")
	}
	info, err := identity.Stat()
	if err != nil {
		_ = identity.Close()
		return nil, err
	}
	if info.Mode()&os.ModeSocket == 0 {
		_ = identity.Close()
		return nil, fmt.Errorf("%q is not a Unix socket", path)
	}
	return identity, nil
}
