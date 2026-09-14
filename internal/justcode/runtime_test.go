package justcode

import (
	"runtime"
	"testing"
)

func TestResolveRuntimeFlag(t *testing.T) {
	cases := []struct {
		flag, pref string
		want       Runtime
	}{
		{"--tart", "", RuntimeTart},
		{"--docker", "", RuntimeDocker},
		{"--microsandbox", "", RuntimeMicrosandbox},
		{"--tart", "docker", RuntimeTart}, // explicit flag wins
		{"--docker", "tart", RuntimeDocker},
	}
	for _, c := range cases {
		got, err := ResolveRuntime(c.flag, c.pref)
		if err != nil {
			t.Fatalf("ResolveRuntime(%q, %q): %v", c.flag, c.pref, err)
		}
		if got != c.want {
			t.Errorf("ResolveRuntime(%q, %q) = %q, want %q", c.flag, c.pref, got, c.want)
		}
	}
}

func TestResolveRuntimePreference(t *testing.T) {
	for _, name := range []string{"docker", "microsandbox", "tart"} {
		got, err := ResolveRuntime("", name)
		if err != nil {
			t.Fatalf("ResolveRuntime(\"\", %q): %v", name, err)
		}
		if string(got) != name {
			t.Errorf("ResolveRuntime(\"\", %q) = %q", name, got)
		}
	}
}

func TestResolveRuntimeErrors(t *testing.T) {
	cases := []struct{ flag, pref string }{
		{"--podman", ""},
		{"", "podman"},
		{"--", ""},
	}
	for _, c := range cases {
		if _, err := ResolveRuntime(c.flag, c.pref); err == nil {
			t.Errorf("ResolveRuntime(%q, %q): expected error", c.flag, c.pref)
		}
	}
	// The empty-selection error only exists off Windows; there the default
	// is microsandbox (covered by runtime_windows_test.go).
	if _, err := ResolveRuntime("", ""); runtime.GOOS != "windows" && err == nil {
		t.Errorf("ResolveRuntime(\"\", \"\") off Windows: expected error")
	}
}

func TestSupportedRuntimesOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows behavior covered by runtime_windows_test.go")
	}
	got := supportedRuntimes()
	if len(got) != 3 {
		t.Errorf("supportedRuntimes() = %v, want all three runtimes", got)
	}
}
