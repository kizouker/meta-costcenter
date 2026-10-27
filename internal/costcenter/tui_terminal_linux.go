//go:build linux

package costcenter

import "golang.org/x/sys/unix"

const (
	terminalGetTermios = unix.TCGETS
	terminalSetTermios = unix.TCSETS
)
