//go:build windows

package justcode

import "testing"

// On Windows the microsandbox runtime is the default and the only supported
// choice: Docker is not a target there and Tart is macOS-only.
func TestResolveRuntimeWindows(t *testing.T) {
	got, err := ResolveRuntime("", "")
	if err != nil {
		t.Fatalf("ResolveRuntime(\"\", \"\") on Windows: %v", err)
	}
	if got != RuntimeMicrosandbox {
		t.Errorf("ResolveRuntime(\"\", \"\") = %q, want microsandbox", got)
	}
	for _, flag := range []string{"--docker", "--tart"} {
		if _, err := ResolveRuntime(flag, ""); err == nil {
			t.Errorf("ResolveRuntime(%q, \"\") on Windows: expected error", flag)
		}
	}
	for _, pref := range []string{"docker", "tart"} {
		if _, err := ResolveRuntime("", pref); err == nil {
			t.Errorf("ResolveRuntime(\"\", %q) on Windows: expected error", pref)
		}
	}
}

func TestSupportedRuntimesWindows(t *testing.T) {
	got := supportedRuntimes()
	if len(got) != 1 || got[0] != RuntimeMicrosandbox {
		t.Errorf("supportedRuntimes() = %v, want [microsandbox]", got)
	}
}
