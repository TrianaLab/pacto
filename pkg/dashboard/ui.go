package dashboard

import (
	"embed"
	"io/fs"
)

//go:embed ui
var uiFS embed.FS

// EmbeddedUI returns the embedded filesystem containing dashboard UI assets.
func EmbeddedUI() fs.FS {
	return uiFS
}
