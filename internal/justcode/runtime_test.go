package justcode

import "testing"

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
		{"", ""},
		{"--podman", ""},
		{"", "podman"},
		{"--", ""},
	}
	for _, c := range cases {
		if _, err := ResolveRuntime(c.flag, c.pref); err == nil {
			t.Errorf("ResolveRuntime(%q, %q): expected error", c.flag, c.pref)
		}
	}
}
