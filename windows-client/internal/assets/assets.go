// Package assets selects the built React bundle, with a clear development
// fallback when Go is compiled before the frontend.
package assets

import (
	"embed"
	"io/fs"
)

//go:embed all:bundle all:fallback
var content embed.FS

func Files() fs.FS {
	if bundle, err := fs.Sub(content, "bundle"); err == nil {
		if _, err := fs.Stat(bundle, "index.html"); err == nil {
			return bundle
		}
	}
	fallback, _ := fs.Sub(content, "fallback")
	return fallback
}
