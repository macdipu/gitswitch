//go:build desktop

package window

// Wails' file dialogs use UTType, which lives in the UniformTypeIdentifiers
// framework; Wails does not link it, so the macOS build fails without this
// ("Undefined symbols: _OBJC_CLASS_$_UTType").

// #cgo LDFLAGS: -framework UniformTypeIdentifiers
import "C"
