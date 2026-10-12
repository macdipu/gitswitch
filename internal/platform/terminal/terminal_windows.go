//go:build windows

package terminal

import (
	"io"
	"os"
	"syscall"
	"unsafe"
)

type Terminal struct {
	In  io.Reader
	Out io.Writer
	fi  *os.File
	fo  *os.File
}

func (t *Terminal) Close() { t.fi.Close(); t.fo.Close() }

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	getConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	user32           = syscall.NewLazyDLL("user32.dll")
	isWindowVisible  = user32.NewProc("IsWindowVisible")
	getClassName     = user32.NewProc("GetClassNameW")
)

// visible reports whether a person can see the console window. ConPTY
// terminals (Windows Terminal, VS Code, Android Studio) draw the console
// themselves; their hidden "PseudoConsoleWindow" stands for a visible tab.
func visible(h uintptr) bool {
	if v, _, _ := isWindowVisible.Call(h); v != 0 {
		return true
	}
	buf := make([]uint16, 64)
	n, _, _ := getClassName.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return n > 0 && syscall.UTF16ToString(buf[:n]) == "PseudoConsoleWindow"
}

// OpenTerminal opens the console directly. When git was started by an IDE
// or GUI client with no console (CREATE_NO_WINDOW), GetConsoleWindow returns
// 0 and we never prompt. A console started hidden (SW_HIDE) has a window
// nobody can see, so it counts as none. ConPTY terminals return a handle
// (see visible), so prompting works there.
func OpenTerminal() *Terminal {
	if NoTerminalPrompt() {
		return nil
	}
	h, _, _ := getConsoleWindow.Call()
	if h == 0 || !visible(h) {
		return nil
	}
	fi, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	fo, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		fi.Close()
		return nil
	}
	return &Terminal{In: fi, Out: fo, fi: fi, fo: fo}
}
