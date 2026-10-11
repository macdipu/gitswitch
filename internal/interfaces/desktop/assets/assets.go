// Package assets holds the desktop app's page, built into the program.
package assets

import _ "embed"

// Index is the app page (one self-contained HTML file).
//
//go:embed index.html
var Index []byte
