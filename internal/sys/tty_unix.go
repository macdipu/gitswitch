//go:build linux || darwin

package sys

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

type TTYState struct{ t syscall.Termios }

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

// MakeRaw switches the Terminal to raw input so single keys can be read.
func MakeRaw() (*TTYState, error) {
	fd := os.Stdin.Fd()
	var old syscall.Termios
	if err := ioctl(fd, ioctlGetTermios, unsafe.Pointer(&old)); err != nil {
		return nil, errors.New("not a Terminal")
	}
	raw := old
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctl(fd, ioctlSetTermios, unsafe.Pointer(&raw)); err != nil {
		return nil, err
	}
	return &TTYState{old}, nil
}

func RestoreTTY(s *TTYState) {
	if s != nil {
		_ = ioctl(os.Stdin.Fd(), ioctlSetTermios, unsafe.Pointer(&s.t))
	}
}

func TermSize() (w, h int) {
	var ws struct{ Row, Col, X, Y uint16 }
	if err := ioctl(os.Stdout.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil || ws.Col == 0 {
		return 80, 24
	}
	return int(ws.Col), int(ws.Row)
}
