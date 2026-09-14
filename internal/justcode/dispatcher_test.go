package justcode

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
)

// withGOOS temporarily points supportedRuntimes at a different platform so the
// dispatcher's platform-gated sweep can be exercised on any host.
func withGOOS(t *testing.T, goos string) {
	t.Helper()
	prev := currentGOOS
	currentGOOS = goos
	t.Cleanup(func() { currentGOOS = prev })
}

type fakeBackend struct {
	id      Runtime
	running bool
	stopped bool
}

func (f *fakeBackend) ID() Runtime                              { return f.id }
func (f *fakeBackend) Start(context.Context) error              { return nil }
func (f *fakeBackend) Stop(context.Context) error               { f.stopped = true; return nil }
func (f *fakeBackend) Restart(context.Context) error            { return nil }
func (f *fakeBackend) Clean(context.Context) error              { return nil }
func (f *fakeBackend) Doctor(context.Context) error             { return nil }
func (f *fakeBackend) Logs() error                              { return nil }
func (f *fakeBackend) Shell() error                             { return nil }
func (f *fakeBackend) IsRunning(context.Context) (bool, error)  { return f.running, nil }
func (f *fakeBackend) Endpoint(context.Context) (string, error) { return "http://localhost:4096", nil }

func TestDispatcherRunningAndMultiple(t *testing.T) {
	d := NewDispatcherWith(Config{}, map[Runtime]Backend{
		RuntimeDocker:       &fakeBackend{id: RuntimeDocker},
		RuntimeMicrosandbox: &fakeBackend{id: RuntimeMicrosandbox, running: true},
		RuntimeTart:         &fakeBackend{id: RuntimeTart, running: true},
	})
	running, err := d.Running(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// On Windows only microsandbox is probed, so the tart backend is invisible
	// and SingleRunning would succeed. That case is exercised separately below.
	if runtime.GOOS == "windows" {
		if len(running) != 1 || running[0] != RuntimeMicrosandbox {
			t.Fatalf("Running (windows) = %v", running)
		}
		return
	}
	if len(running) != 2 || running[0] != RuntimeMicrosandbox || running[1] != RuntimeTart {
		t.Fatalf("Running = %v", running)
	}
	if _, err := d.SingleRunning(context.Background()); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("SingleRunning expected multiple error, got %v", err)
	}
}

func TestDispatcherSingle(t *testing.T) {
	// Docker is the "running" backend here only where it can actually run; on
	// Windows the sweep is platform-gated, so the same intent uses microsandbox.
	running := RuntimeDocker
	if runtime.GOOS == "windows" {
		running = RuntimeMicrosandbox
	}
	d := NewDispatcherWith(Config{}, map[Runtime]Backend{
		RuntimeDocker:       &fakeBackend{id: RuntimeDocker, running: running == RuntimeDocker},
		RuntimeMicrosandbox: &fakeBackend{id: RuntimeMicrosandbox, running: running == RuntimeMicrosandbox},
		RuntimeTart:         &fakeBackend{id: RuntimeTart},
	})
	rt, err := d.SingleRunning(context.Background())
	if err != nil || rt != running {
		t.Fatalf("SingleRunning = %v, %v", rt, err)
	}
}

// TestDispatcherSingleOnWindows exercises the platform-gated sweep without
// rebuilding for Windows: with only microsandbox running and tart/docker also
// present in the map, the Windows sweep must report exactly one runtime.
func TestDispatcherSingleOnWindows(t *testing.T) {
	withGOOS(t, "windows")
	d := NewDispatcherWith(Config{}, map[Runtime]Backend{
		RuntimeDocker:       &fakeBackend{id: RuntimeDocker},
		RuntimeMicrosandbox: &fakeBackend{id: RuntimeMicrosandbox, running: true},
		RuntimeTart:         &fakeBackend{id: RuntimeTart, running: true},
	})
	running, err := d.Running(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 1 || running[0] != RuntimeMicrosandbox {
		t.Fatalf("Running (windows) = %v, want [microsandbox]", running)
	}
	rt, err := d.SingleRunning(context.Background())
	if err != nil || rt != RuntimeMicrosandbox {
		t.Fatalf("SingleRunning (windows) = %v, %v", rt, err)
	}
}

func TestDispatcherStopAll(t *testing.T) {
	// The set of runtimes actually swept is platform-gated: on Windows tart and
	// docker are invisible to stop, so they must not be marked stopped there.
	docker := &fakeBackend{id: RuntimeDocker}
	msb := &fakeBackend{id: RuntimeMicrosandbox, running: true}
	tart := &fakeBackend{id: RuntimeTart, running: true}
	d := NewDispatcherWith(Config{}, map[Runtime]Backend{
		RuntimeDocker:       docker,
		RuntimeMicrosandbox: msb,
		RuntimeTart:         tart,
	})
	if err := d.StopAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if !msb.stopped || tart.stopped || docker.stopped {
			t.Fatalf("windows: msb=%v tart=%v docker=%v (want msb stopped, tart/docker untouched)", msb.stopped, tart.stopped, docker.stopped)
		}
		return
	}
	if !msb.stopped || !tart.stopped {
		t.Fatalf("expected both running backends to be stopped")
	}
}

func TestDispatcherPrepareNoConflict(t *testing.T) {
	// Use the running backend that is actually visible to the sweep: on Windows
	// docker is not probed, so the "no conflict" intent is exercised with msb.
	running := RuntimeDocker
	if runtime.GOOS == "windows" {
		running = RuntimeMicrosandbox
	}
	d := NewDispatcherWith(Config{}, map[Runtime]Backend{
		RuntimeDocker:       &fakeBackend{id: RuntimeDocker, running: running == RuntimeDocker},
		RuntimeMicrosandbox: &fakeBackend{id: RuntimeMicrosandbox, running: running == RuntimeMicrosandbox},
		RuntimeTart:         &fakeBackend{id: RuntimeTart},
	})
	if err := d.Prepare(context.Background(), running); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
}

func TestDispatcherPrepareRefusesConflict(t *testing.T) {
	if isTerminal(os.Stdin) {
		t.Skip("stdin is a TTY; the non-interactive refusal path is not testable here")
	}
	// On Windows docker is invisible to the sweep, so there is no conflict to
	// refuse; that platform-gated behavior is covered by the windows-specific
	// test above. Here we want the conflict path, which only exists off Windows.
	if runtime.GOOS == "windows" {
		t.Skip("windows sweep only probes microsandbox; covered by TestDispatcherSingleOnWindows")
	}
	d := NewDispatcherWith(Config{}, map[Runtime]Backend{
		RuntimeDocker:       &fakeBackend{id: RuntimeDocker, running: true},
		RuntimeMicrosandbox: &fakeBackend{id: RuntimeMicrosandbox},
		RuntimeTart:         &fakeBackend{id: RuntimeTart},
	})
	err := d.Prepare(context.Background(), RuntimeTart)
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("expected conflict error, got %v", err)
	}
}
