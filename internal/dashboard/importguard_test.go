package dashboard

import (
	"os/exec"
	"strings"
	"testing"
)

const moduleRoot = "github.com/nextlevelbuilder/goclaw"

func TestImportGuard(t *testing.T) {
	targets := []string{
		moduleRoot + "/cmd/dashboard",
		moduleRoot + "/internal/dashboard/...",
	}
	out, err := exec.Command("go", append([]string{"list", "-deps"}, targets...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps failed (dashboard packages may not exist yet): %v\n%s", err, string(out))
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == moduleRoot+"/internal/dashboard" {
			continue // the package itself is allowed
		}
		if !strings.HasPrefix(line, moduleRoot+"/internal/") {
			continue
		}
		if !strings.HasPrefix(line, moduleRoot+"/internal/dashboard/") {
			t.Errorf("illegal internal import leaked from dashboard: %s", line)
		}
	}

	// pkg/protocol must not import any internal/* package (it is imported by
	// dashboard and must stay dependency-lean).
	protoOut, protoErr := exec.Command("go", "list", "-deps", moduleRoot+"/pkg/protocol").CombinedOutput()
	if protoErr != nil {
		t.Fatalf("go list -deps pkg/protocol failed: %v\n%s", protoErr, string(protoOut))
	}
	for _, line := range strings.Split(strings.TrimSpace(string(protoOut)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, moduleRoot+"/internal/") {
			t.Errorf("pkg/protocol illegally imports internal package: %s", line)
		}
	}
}
