//go:build !dashboardui

package webui

import "io/fs"

// Assets returns nil when the dashboardui build tag is not set. Callers must
// handle nil and fall back to --web-dir.
func Assets() fs.FS { return nil }
