package justcode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// statsTestHome isolates the settings file and returns its path.
func statsTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// os.UserConfigDir reads XDG_CONFIG_HOME on unix and AppData on windows;
	// without the latter, a test writes the real host settings file.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	t.Setenv("JUST_CODE_STATS_URL", "")
	path, err := UserSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStatsMachineIDIsStableAndPrivate(t *testing.T) {
	statsTestHome(t)
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	first, err := statsMachineID(DefaultFS, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := statsMachineID(DefaultFS, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second {
		t.Fatalf("machine id must be stable: %q %q", first, second)
	}
	if len(first) != 32 {
		t.Fatalf("machine id should be 16 random bytes as hex, got %q", first)
	}
	if runtime.GOOS != "windows" {
		// Windows reports a synthesized 0666 regardless of the requested
		// mode; assert the POSIX mode only where the filesystem carries it.
		info, err := os.Stat(filepath.Join(stateDir, "stats", "machine-id"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("machine id permissions = %o", info.Mode().Perm())
		}
	}
}

func TestBuildStatsEventNeverCarriesTheProjectPath(t *testing.T) {
	statsTestHome(t)
	stateDir := t.TempDir()
	secretRoot := filepath.Join(t.TempDir(), "projet-confidentiel-42")
	event, err := BuildStatsEvent(DefaultFS, stateDir, StatsEventInput{
		Kind: "init", Version: "1.2.3", ProjectRoot: secretRoot,
		Init: &InitStats{
			Skills:  map[string]bool{"official/rgaa": true},
			MCPs:    map[string]bool{"data-gouv": false},
			Runtime: "microsandbox", Isolation: "full", Harness: "opencode",
			SkillsStorage: "versioned", GitHubCase: 1, Advanced: false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.ProjectID == "" {
		t.Fatal("a project event carries a local project identifier")
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"projet-confidentiel-42", filepath.Dir(secretRoot)} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("payload leaked %q: %s", forbidden, data)
		}
	}
	if !strings.Contains(string(data), `"skills":{"official/rgaa":true}`) {
		t.Fatalf("skills map missing: %s", data)
	}
}

func TestStatsQueueIsBoundedAndOrdered(t *testing.T) {
	stateDir := t.TempDir()
	for i := 0; i < statsQueueLimit+5; i++ {
		event := StatsEvent{SchemaVersion: StatsSchemaVersion, EventID: string(rune('a' + i)), Kind: "setup"}
		if err := EnqueueStatsEvent(DefaultFS, stateDir, event); err != nil {
			t.Fatal(err)
		}
	}
	events, err := PendingStatsEvents(DefaultFS, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != statsQueueLimit {
		t.Fatalf("queue length = %d, want %d", len(events), statsQueueLimit)
	}
	if events[len(events)-1].EventID != string(rune('a'+statsQueueLimit+4)) {
		t.Fatalf("newest event was dropped: %q", events[len(events)-1].EventID)
	}
	if err := ClearStatsQueue(DefaultFS, stateDir); err != nil {
		t.Fatal(err)
	}
	events, _ = PendingStatsEvents(DefaultFS, stateDir)
	if len(events) != 0 {
		t.Fatalf("clear left events: %v", events)
	}
}

func TestStatsQueueRecoversAStaleLock(t *testing.T) {
	stateDir := t.TempDir()
	lock := filepath.Join(stateDir, "stats", "queue.lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte("dead owner"), 0o600); err != nil {
		t.Fatal(err)
	}
	previousWait := statsQueueLockWait
	statsQueueLockWait = 20 * time.Millisecond
	t.Cleanup(func() { statsQueueLockWait = previousWait })
	if err := EnqueueStatsEvent(DefaultFS, stateDir, StatsEvent{SchemaVersion: StatsSchemaVersion, EventID: "e", Kind: "setup"}); err != nil {
		t.Fatalf("a stale lock must be recovered: %v", err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("the lock must be released: %v", err)
	}
}

func TestFlushStatsPostsAndClearsOnlyOnSuccess(t *testing.T) {
	settingsPath := statsTestHome(t)
	if err := WriteStatsConsent(DefaultFS, settingsPath, true, true); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	event, err := BuildStatsEvent(DefaultFS, stateDir, StatsEventInput{Kind: "setup", Setup: &SetupStats{Platforms: []string{"albert"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := EnqueueStatsEvent(DefaultFS, stateDir, event); err != nil {
		t.Fatal(err)
	}

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"kind":"setup"`) || !strings.Contains(string(body), `"machineId"`) {
			t.Errorf("unexpected payload: %s", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	t.Setenv("JUST_CODE_STATS_URL", server.URL)
	if err := FlushStats(context.Background(), DefaultFS, stateDir); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("endpoint calls = %d", calls)
	}
	if events, _ := PendingStatsEvents(DefaultFS, stateDir); len(events) != 0 {
		t.Fatalf("a successful flush must clear the queue: %v", events)
	}

	// A failing endpoint keeps the queue for a later attempt.
	if err := EnqueueStatsEvent(DefaultFS, stateDir, event); err != nil {
		t.Fatal(err)
	}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()
	t.Setenv("JUST_CODE_STATS_URL", failing.URL)
	if err := FlushStats(context.Background(), DefaultFS, stateDir); err == nil {
		t.Fatal("a 500 must be reported")
	}
	if events, _ := PendingStatsEvents(DefaultFS, stateDir); len(events) != 1 {
		t.Fatalf("a failed flush must keep the queue: %v", events)
	}
}

func TestFlushStatsRespectsConsent(t *testing.T) {
	settingsPath := statsTestHome(t)
	if err := WriteStatsConsent(DefaultFS, settingsPath, false, true); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	event := StatsEvent{SchemaVersion: StatsSchemaVersion, EventID: "e", Kind: "setup"}
	if err := EnqueueStatsEvent(DefaultFS, stateDir, event); err != nil {
		t.Fatal(err)
	}
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
	}))
	defer server.Close()
	t.Setenv("JUST_CODE_STATS_URL", server.URL)
	if err := FlushStats(context.Background(), DefaultFS, stateDir); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("nothing may be sent without consent, calls = %d", calls)
	}
}

func TestFlushPreservesEventsQueuedDuringTheRequest(t *testing.T) {
	settingsPath := statsTestHome(t)
	if err := WriteStatsConsent(DefaultFS, settingsPath, true, true); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	first := StatsEvent{SchemaVersion: StatsSchemaVersion, EventID: "first", Kind: "setup"}
	if err := EnqueueStatsEvent(DefaultFS, stateDir, first); err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Another wizard queues an event while this flush is in flight.
		if err := EnqueueStatsEvent(DefaultFS, stateDir, StatsEvent{SchemaVersion: StatsSchemaVersion, EventID: "second", Kind: "init"}); err != nil {
			t.Errorf("concurrent enqueue: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	t.Setenv("JUST_CODE_STATS_URL", server.URL)
	if err := FlushStats(context.Background(), DefaultFS, stateDir); err != nil {
		t.Fatal(err)
	}
	events, err := PendingStatsEvents(DefaultFS, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventID != "second" {
		t.Fatalf("an event queued during the request must survive: %+v", events)
	}
}

func TestRecordStatsEventIsANoOpWithoutConsent(t *testing.T) {
	statsTestHome(t)
	stateDir := t.TempDir()
	RecordStatsEvent(DefaultFS, stateDir, StatsEventInput{Kind: "setup"})
	if events, _ := PendingStatsEvents(DefaultFS, stateDir); len(events) != 0 {
		t.Fatalf("no consent must queue nothing: %v", events)
	}
}

func TestRecordStatsAbandonRequiresAnAnsweredConsent(t *testing.T) {
	settingsPath := statsTestHome(t)
	stateDir := t.TempDir()
	// Opt-in recorded manually (stats on) before any wizard answered the
	// question: a run abandoned before the review must stay silent.
	if err := WriteUserSettings(DefaultFS, settingsPath, UserSettings{SchemaVersion: 1, StatsEnabled: true, StatsConsentAsked: false}); err != nil {
		t.Fatal(err)
	}
	RecordStatsEvent(DefaultFS, stateDir, StatsEventInput{Kind: "abandon", Stage: "credentials"})
	if events, _ := PendingStatsEvents(DefaultFS, stateDir); len(events) != 0 {
		t.Fatalf("an unanswered consent must queue no abandonment: %v", events)
	}
	if err := WriteStatsConsent(DefaultFS, settingsPath, true, true); err != nil {
		t.Fatal(err)
	}
	previousSpawn := StatsFlushSpawn
	StatsFlushSpawn = func() {}
	t.Cleanup(func() { StatsFlushSpawn = previousSpawn })
	RecordStatsEvent(DefaultFS, stateDir, StatsEventInput{Kind: "abandon", Stage: "credentials"})
	if events, _ := PendingStatsEvents(DefaultFS, stateDir); len(events) != 1 {
		t.Fatalf("an answered opt-in must queue the abandonment: %v", events)
	}
}

func TestStatsMachineIDLoserReadsTheWinner(t *testing.T) {
	statsTestHome(t)
	stateDir := t.TempDir()
	path := statsMachineIDPath(stateDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("winner-id\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := statsMachineID(DefaultFS, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if id != "winner-id" {
		t.Fatalf("a second process must reuse the winning identifier, got %q", id)
	}
}
