package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var files embed.FS

// Files returns the embedded frontend directory. The generated output may be
// absent in a fresh checkout; callers should treat missing files as 404s.
func Files() fs.FS {
	dist, err := fs.Sub(files, "dist")
	if err != nil {
		panic(err)
	}
	return dist
}
