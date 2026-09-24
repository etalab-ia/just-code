package justcode

import (
	"path/filepath"
	"strings"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// TestMSBCreateOptionsNeverBindMountAWorkspace pins the P22 boundary at the
// SDK option level: whatever the spec says, the mount table must never carry
// a host path for /workspace. A regression here is the original leak — the
// guest would read the host checkout, including its .env files.
func TestMSBCreateOptionsNeverBindMountAWorkspace(t *testing.T) {
	for _, sealed := range []bool{true, false} {
		spec := msbSandboxSpec{
			Image:           msbImage,
			SealedWorkspace: sealed,
			StartScript:     "exec sleep infinity",
		}
		var cfg msb.SandboxConfig
		for _, option := range msbCreateOptions(spec) {
			option(&cfg)
		}
		ws, present := cfg.Volumes["/workspace"]
		if sealed {
			if !present {
				t.Fatalf("a sealed spec must still provide /workspace storage")
			}
			if ws.Bind != "" || ws.Kind() != msb.MountKindOwned {
				t.Fatalf("sealed /workspace mount = %+v (kind %v); want owned storage with no host source", ws, ws.Kind())
			}
			continue
		}
		// An unsealed spec is refused by the runtime; if it ever reaches the
		// options builder, it must not silently become a bind mount either.
		if present && ws.Bind != "" {
			t.Fatalf("an unsealed spec produced a host bind mount: %+v", ws)
		}
	}
}

// TestParseOwnedWorkspaceDistinguishesProvenance pins the persisted-config
// reading the provenance check depends on. The sealed model needs the
// question "does this guest have a host-mounted workspace?", which the raw
// document answers even when the SDK's typed view cannot see the mounts.
func TestParseOwnedWorkspaceDistinguishesProvenance(t *testing.T) {
	cases := []struct {
		name    string
		doc     string
		owned   bool
		wantErr bool
	}{
		{
			name:  "owned dir",
			doc:   `{"mounts":[{"type":"Owned","guest":"/workspace"}]}`,
			owned: true,
		},
		{
			name: "legacy host bind",
			doc:  `{"mounts":[{"type":"Bind","host":"/Users/luis/project","guest":"/workspace"}]}`,
		},
		{
			name: "bind with implicit type",
			doc:  `{"mounts":[{"host":"/home/u/p","guest":"/workspace"}]}`,
		},
		{
			name: "named volume is not a host path",
			doc:  `{"mounts":[{"type":"Named","name":"vol","guest":"/workspace"}]}`,
		},
		{
			name: "no workspace mount at all",
			doc:  `{"mounts":[{"type":"Tmpfs","guest":"/tmp"}]}`,
		},
		{
			name:    "invalid json",
			doc:     `{not json`,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owned, err := parseOwnedWorkspace(tc.doc, "/workspace")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if owned != tc.owned {
				t.Fatalf("owned = %v, want %v", owned, tc.owned)
			}
		})
	}
}

// TestParseWorkspaceMountStillDetectsLegacyBinds pins the check the runtime
// uses to refuse a pre-P22 instance: a host bind must be detected as such.
func TestParseWorkspaceMountStillDetectsLegacyBinds(t *testing.T) {
	mounted, err := parseWorkspaceMount(`{"mounts":[{"type":"Bind","host":"/host/path","guest":"/workspace"}]}`, "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	if mounted != "/host/path" {
		t.Fatalf("mounted = %q", mounted)
	}
	mounted, err = parseWorkspaceMount(`{"mounts":[{"type":"Owned","guest":"/workspace"}]}`, "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	if mounted != "" {
		t.Fatalf("an owned workspace has no host path, got %q", mounted)
	}
}

// TestMSBSandboxEnvCarriesGitIdentity pins the P11 identity reaching the
// guest through the environment the prep script reads, so commits made inside
// the sandbox carry the configured identity rather than the script default.
func TestMSBSandboxEnvCarriesGitIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	path, err := UserSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteUserSettings(DefaultFS, path, UserSettings{
		SchemaVersion: 1, GitName: "Luis Arias", GitEmail: "luis@example.gouv.fr",
	}); err != nil {
		t.Fatal(err)
	}
	m := newTestMicrosandbox(t, &fakeMSBClient{})
	env := m.sandboxEnv()
	if env[msbGitNameEnv] != "Luis Arias" || env[msbGitEmailEnv] != "luis@example.gouv.fr" {
		t.Fatalf("guest env identity = %q <%s>", env[msbGitNameEnv], env[msbGitEmailEnv])
	}
	// The prep script must actually read those names, or the env is inert.
	if !strings.Contains(guestPrepScript, msbGitNameEnv) || !strings.Contains(guestPrepScript, msbGitEmailEnv) {
		t.Fatalf("the guest prep script does not read the identity environment")
	}
}
