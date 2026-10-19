//go:build dashboardui

package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

// Assets returns the embedded dashboard UI file system. Only compiled when
// the dashboardui build tag is set.
func Assets() fs.FS { return &assets }
