//go:build desktop

// Package window is the desktop window: the app page (package api) in the
// system web view through Wails. Without the `desktop` build tag it is left
// out (nowindow.go), so plain builds need no cgo.
package window

import (
	"context"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/macdipu/gitswitch/internal/bootstrap"
	"github.com/macdipu/gitswitch/internal/interfaces/desktop/api"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// Available reports whether this build has the desktop window.
const Available = true

// Run opens the app window and returns when it is closed. A second
// `gitswitch ui` brings the open window to the front instead.
func Run(a *bootstrap.App) error {
	process.DetachConsole() // started from the Start menu: drop the empty console
	var ctx context.Context
	pick := func(title string) (string, error) {
		return runtime.OpenDirectoryDialog(ctx, runtime.OpenDialogOptions{Title: title, DefaultDirectory: filesystem.HomeDir(), CanCreateDirectories: true})
	}
	return wails.Run(&options.App{
		Title:            "GitSwitch",
		Width:            1180,
		Height:           780,
		MinWidth:         760,
		MinHeight:        520,
		BackgroundColour: &options.RGBA{R: 247, G: 248, B: 250, A: 255},
		AssetServer:      &assetserver.Options{Handler: api.Handler(a, pick)},
		OnStartup:        func(c context.Context) { ctx = c },
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "github.com.macdipu.gitswitch",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				if ctx != nil {
					runtime.WindowUnminimise(ctx)
					runtime.Show(ctx)
				}
			},
		},
	})
}
