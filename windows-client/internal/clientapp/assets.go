package clientapp

import (
	"embed"
	"io/fs"
)

//go:embed assets/*
var embeddedAssets embed.FS

func Assets() (fs.FS, error) { return fs.Sub(embeddedAssets, "assets") }
