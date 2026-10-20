//go:build !dashboardui

package webui

import "testing"

// TestAssetsFallbackNil covers the stub build: without the dashboardui tag
// there is no embedded bundle, so the handler must fall back to --web-dir.
func TestAssetsFallbackNil(t *testing.T) {
	if Assets() != nil {
		t.Fatal("expected Assets() to be nil without the dashboardui build tag")
	}
}
