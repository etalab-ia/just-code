package justcode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLaunchTimingPersistence(t *testing.T) {
	dir := t.TempDir()
	store := NewLaunchTimingStore(DefaultFS, dir)
	if _, _, ok := store.Observation("test", LaunchCold); ok {
		t.Fatal("missing measurements must not invent an estimate")
	}
	if err := store.Observe("test", LaunchCold, 125*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := store.Observe("test", LaunchWarm, 12*time.Second); err != nil {
		t.Fatal(err)
	}
	reloaded := NewLaunchTimingStore(DefaultFS, dir)
	for kind, want := range map[LaunchKind]int64{LaunchCold: 125, LaunchWarm: 12} {
		low, high, ok := reloaded.Observation("test", kind)
		if !ok || low != want || high != want {
			t.Fatalf("%s = %d %d %v", kind, low, high, ok)
		}
	}
	for i := 0; i < maxLaunchSamples+2; i++ {
		if err := reloaded.Observe("test", LaunchCold, 10*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if got := reloaded.load().Entries["test"].FirstColdSeconds; got != 125 {
		t.Fatalf("first cold baseline overwritten: %d", got)
	}
	if err := os.WriteFile(filepath.Join(dir, launchTimingsFile), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := reloaded.Observation("test", LaunchCold); ok {
		t.Fatal("corrupt history must not produce an estimate")
	}
}

func TestLaunchTimingFingerprintIncludesImageDigest(t *testing.T) {
	key := func(image string) string {
		return LaunchTimingKey(image, "1.18.32", IsolationFull, 2, 4096, "linux", "arm64")
	}
	if key("debian@sha256:a") == key("debian@sha256:b") {
		t.Fatal("new images must not inherit old timing samples")
	}
}

func TestLaunchTimingUsesSelectedGuestImage(t *testing.T) {
	for _, isolation := range []Isolation{IsolationFull, IsolationBackend} {
		for _, ids := range [][]string{nil, {"chrome-devtools"}} {
			m := NewMicrosandboxRuntime(Config{Isolation: isolation})
			m.OpenCodeOverlay.MCPConnectors = ids
			d := NewDispatcherWith(m.cfg, map[Runtime]Backend{RuntimeMicrosandbox: m})
			u := &LaunchUX{}
			d.SetLaunchUX(u)
			want := LaunchTimingKeyFor(m.cfg, msbImageForMCPs(ids))
			if u.TimingKey != want {
				t.Errorf("isolation=%s MCPs=%v: timing image differs from guest image", isolation, ids)
			}
		}
	}
}

func TestLaunchUXMeasuredHintsAndWarningSynthesis(t *testing.T) {
	u := &LaunchUX{TimingStore: NewLaunchTimingStore(DefaultFS, t.TempDir()), TimingKey: "test", AgentRuntime: "OpenCode"}
	if strings.Contains(u.LaunchLine(), "30 secondes") {
		t.Fatal("unmeasured launch must not promise a duration")
	}
	u.RecordFirstStart()
	u.Observe(125 * time.Second)
	if !strings.Contains(u.LaunchLine(), "2 minutes") {
		t.Fatal(u.LaunchLine())
	}
	u.AddWarning("ressources limitées")
	u.AddWarning("ressources limitées")
	u.AddWarning("secret non recherché")
	lines := u.Checklist()
	count := 0
	for _, line := range lines {
		if strings.Contains(line, "⚠") {
			count++
			if !strings.Contains(line, "ressources limitées") || !strings.Contains(line, "secret non recherché") {
				t.Fatal(line)
			}
		}
	}
	if count != 1 {
		t.Fatalf("got %d warning lines: %v", count, lines)
	}
}

func TestLaunchDisablesOpenCodeUpdates(t *testing.T) {
	m := NewMicrosandboxRuntime(Config{Isolation: IsolationFull})
	for _, env := range []map[string]string{m.sandboxEnv(), m.nextStartEnv()} {
		if env["OPENCODE_DISABLE_AUTOUPDATE"] != "1" {
			t.Fatal("guest must not offer unpinned updates")
		}
	}
	d := NewDispatcherWith(Config{}, map[Runtime]Backend{RuntimeMicrosandbox: m})
	u := &LaunchUX{}
	d.SetLaunchUX(u)
	if m.Launch != u || u.TimingKey == "" {
		t.Fatal("report must reach the runtime")
	}
	d.SetLaunchUX(nil)
}

func TestLaunchTransferCountsAreStructuredAndIdempotent(t *testing.T) {
	u := &LaunchUX{}
	manifest := TransferManifest{Entries: []TransferEntry{
		{Included: true},
		{Exclusion: TransferExclusionDotenv, Reason: "arbitrary wording"},
		{Exclusion: TransferExclusionSecret, Reason: "arbitrary wording"},
		{Exclusion: TransferExclusionGitIgnored},
	}}
	u.RecordTransfer(manifest)
	u.RecordTransfer(manifest)
	if u.copiedFiles != 1 || u.dotenvExcluded != 1 || u.secretExcluded != 1 {
		t.Fatalf("incorrect counts: %+v", u)
	}
	if !strings.Contains(u.transferLine(), ".env") || !strings.Contains(u.transferLine(), "sensibles") {
		t.Fatal(u.transferLine())
	}
}

func TestApproveReviewedInputsRejectsChanges(t *testing.T) {
	for _, change := range []string{"unchanged", "changed", "added", "unreadable"} {
		t.Run(change, func(t *testing.T) {
			root, state := trustProject(t), t.TempDir()
			inputs, err := DiscoverUnapprovedInputs(DefaultFS, state, root)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "changed":
				err = os.WriteFile(filepath.Join(root, ".opencode/plugin/hook.js"), []byte("// changed"), 0o600)
			case "added":
				err = os.WriteFile(filepath.Join(root, ".opencode/plugin/new.js"), []byte("// new"), 0o600)
			case "unreadable":
				inputs[0].Hash = ""
			}
			if err != nil {
				t.Fatal(err)
			}
			err = ApproveReviewedInputs(DefaultFS, state, root, inputs)
			if (err == nil) != (change == "unchanged") {
				t.Fatalf("approval result: %v", err)
			}
			remaining, err := DiscoverUnapprovedInputs(DefaultFS, state, root)
			if err != nil {
				t.Fatal(err)
			}
			if change != "unchanged" && len(remaining) == 0 {
				t.Fatal("unreviewed content gained approval")
			}
		})
	}
}
