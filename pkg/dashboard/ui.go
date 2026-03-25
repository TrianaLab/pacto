package dashboard

import (
	"embed"
	"io/fs"
)

//go:embed ui
var uiFS embed.FS

// EmbeddedUI returns the embedded filesystem rooted at the ui/ subdirectory.
// The error from fs.Sub is ignored because the ui/ directory is always
// present in the embedded FS at compile time.
func EmbeddedUI() fs.FS {
	sub, _ := fs.Sub(uiFS, "ui")
	return sub
}
