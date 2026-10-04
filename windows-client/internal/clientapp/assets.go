package clientapp

import (
	"embed"
	"io/fs"
)

//go:embed assets/index.html assets/app.js
var embeddedAssets embed.FS

func Assets() (fs.FS, error) { return fs.Sub(embeddedAssets, "assets") }
