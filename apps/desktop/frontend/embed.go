package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var files embed.FS
var Assets, _ = fs.Sub(files, "dist")
