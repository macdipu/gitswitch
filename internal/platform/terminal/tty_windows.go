//go:build windows

package terminal

import (
	"errors"
	"os"
	"unsafe"
)

var (
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
	procGetBufferInfo  = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

type TTYState struct{ in, out uint32 }

func getMode(f *os.File) (uint32, error) {
	var m uint32
	if r, _, e := procGetConsoleMode.Call(f.Fd(), uintptr(unsafe.Pointer(&m))); r == 0 {
		return 0, e
	}
	return m, nil
}

func setMode(f *os.File, m uint32) { procSetConsoleMode.Call(f.Fd(), uintptr(m)) }

// MakeRaw turns off line input and echo, and turns on VT sequences for
// keys and colours (Windows 10 and later; Windows Terminal, VS Code, JetBrains).
func MakeRaw() (*TTYState, error) {
	in, err := getMode(os.Stdin)
	if err != nil {
		return nil, errors.New("not a console")
	}
	out, _ := getMode(os.Stdout)
	const (
		processedInput  = 0x1
		lineInput       = 0x2
		echoInput       = 0x4
		vtInput         = 0x200
		processedOutput = 0x1
		vtProcessing    = 0x4
	)
	setMode(os.Stdin, (in&^(processedInput|lineInput|echoInput))|vtInput)
	setMode(os.Stdout, out|processedOutput|vtProcessing)
	return &TTYState{in, out}, nil
}

func RestoreTTY(s *TTYState) {
	if s != nil {
		setMode(os.Stdin, s.in)
		setMode(os.Stdout, s.out)
	}
}

func TermSize() (w, h int) {
	var info struct {
		Size, Cursor             [2]int16
		Attr                     uint16
		Left, Top, Right, Bottom int16
		MaxX, MaxY               int16
	}
	if r, _, _ := procGetBufferInfo.Call(os.Stdout.Fd(), uintptr(unsafe.Pointer(&info))); r == 0 {
		return 80, 24
	}
	return int(info.Right-info.Left) + 1, int(info.Bottom-info.Top) + 1
}
