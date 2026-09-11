package justcode

import (
	"context"
	"os"
	"strings"
	"testing"
)

type fakeBackend struct {
	id      Runtime
	running bool
	stopped bool
}

func (f *fakeBackend) ID() Runtime                              { return f.id }
func (f *fakeBackend) Start(context.Context) error              { return nil }
func (f *fakeBackend) Stop(context.Context) error               { f.stopped = true; return nil }
func (f *fakeBackend) Build(context.Context) error              { return nil }
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
	if len(running) != 2 || running[0] != RuntimeMicrosandbox || running[1] != RuntimeTart {
		t.Fatalf("Running = %v", running)
	}
	if _, err := d.SingleRunning(context.Background()); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("SingleRunning expected multiple error, got %v", err)
	}
}

func TestDispatcherSingle(t *testing.T) {
	d := NewDispatcherWith(Config{}, map[Runtime]Backend{
		RuntimeDocker:       &fakeBackend{id: RuntimeDocker, running: true},
		RuntimeMicrosandbox: &fakeBackend{id: RuntimeMicrosandbox},
		RuntimeTart:         &fakeBackend{id: RuntimeTart},
	})
	rt, err := d.SingleRunning(context.Background())
	if err != nil || rt != RuntimeDocker {
		t.Fatalf("SingleRunning = %v, %v", rt, err)
	}
}

func TestDispatcherStopAll(t *testing.T) {
	msb := &fakeBackend{id: RuntimeMicrosandbox, running: true}
	tart := &fakeBackend{id: RuntimeTart, running: true}
	d := NewDispatcherWith(Config{}, map[Runtime]Backend{
		RuntimeDocker:       &fakeBackend{id: RuntimeDocker},
		RuntimeMicrosandbox: msb,
		RuntimeTart:         tart,
	})
	if err := d.StopAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !msb.stopped || !tart.stopped {
		t.Fatalf("expected both running backends to be stopped")
	}
}

func TestDispatcherPrepareNoConflict(t *testing.T) {
	d := NewDispatcherWith(Config{}, map[Runtime]Backend{
		RuntimeDocker:       &fakeBackend{id: RuntimeDocker, running: true},
		RuntimeMicrosandbox: &fakeBackend{id: RuntimeMicrosandbox},
		RuntimeTart:         &fakeBackend{id: RuntimeTart},
	})
	if err := d.Prepare(context.Background(), RuntimeDocker); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
}

func TestDispatcherPrepareRefusesConflict(t *testing.T) {
	if isTerminal(os.Stdin) {
		t.Skip("stdin is a TTY; the non-interactive refusal path is not testable here")
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
