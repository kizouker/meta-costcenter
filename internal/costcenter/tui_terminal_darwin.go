//go:build darwin

package costcenter

import "golang.org/x/sys/unix"

const (
	terminalGetTermios = unix.TIOCGETA
	terminalSetTermios = unix.TIOCSETA
)
