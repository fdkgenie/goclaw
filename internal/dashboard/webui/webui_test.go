package webui

import "testing"

func TestAssetsFallbackNil(t *testing.T) {
	if Assets() != nil {
		t.Fatal("expected Assets() to be nil without the dashboardui build tag")
	}
}
