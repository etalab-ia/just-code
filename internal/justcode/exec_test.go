package justcode

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestStartDeliversStdinToDetachedChild is a regression test for a race where a
// detached child read empty stdin: os/exec copies a non-*os.File Stdin through a
// goroutine, and releasing the process let the CLI exit before that goroutine
// finished, so the guest fell back to the default password and an empty key.
func TestStartDeliversStdinToDetachedChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("OSStarter exercises Unix detached-process semantics via /bin/sh; the detach path is Tart-only (macOS), so this is not a Windows behavior")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "received")
	script := filepath.Join(dir, "read-stdin.sh")
	// Read one line of stdin and persist it, so the test can observe delivery.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nread -r line\nprintf '%s' \"$line\" > "+out+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "child.log")

	if err := (OSStarter{}).Start(strings.NewReader("s3cret\n"), logPath, "/bin/sh", script); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(out)
		if err == nil {
			if string(data) != "s3cret" {
				t.Fatalf("detached child received %q, want %q", data, "s3cret")
			}
			return
		}
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(logPath)
			t.Fatalf("detached child never wrote the received payload; log:\n%s", log)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStartRejectsOversizedStdin(t *testing.T) {
	dir := t.TempDir()
	payload := strings.Repeat("x", maxStdinPayload+1)
	err := (OSStarter{}).Start(strings.NewReader(payload), filepath.Join(dir, "log"), "/bin/cat")
	if err == nil || !strings.Contains(err.Error(), "stdin payload exceeds") {
		t.Fatalf("expected oversized-payload error, got %v", err)
	}
}

func TestStartHandlesNilStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("OSStarter exercises Unix detached-process semantics via /bin/sh; the detach path is Tart-only (macOS), so this is not a Windows behavior")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	// With no stdin, `true` should still start and exit 0; just assert no error
	// from starting the detached process itself.
	if err := (OSStarter{}).Start(nil, filepath.Join(dir, "child.log"), "/bin/sh", "-c", "printf ok > "+out); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(out); err == nil {
			if string(data) != "ok" {
				t.Fatalf("child output = %q", data)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestIsBatchFile(t *testing.T) {
	cases := map[string]bool{
		`C:\tools\opencode.cmd`:   true,
		`C:\tools\opencode.bat`:   true,
		`C:\tools\OPENCODE.CMD`:   true,
		`C:\tools\opencode.exe`:   false,
		`C:\tools\opencode`:       false,
		"/usr/local/bin/opencode": false,
	}
	for path, want := range cases {
		if got := isBatchFile(path); got != want {
			t.Errorf("isBatchFile(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestInteractiveCommandNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows behavior")
	}
	cmd := interactiveCommand("opencode", "attach", "http://localhost:4096")
	if cmd.Args[0] != "opencode" {
		t.Errorf("off Windows, interactiveCommand should not wrap through cmd.exe; args[0] = %q", cmd.Args[0])
	}
}

// TestInteractiveCommandWindowsBatch exercises the batch-shim path on real
// Windows: a program whose resolved PATH entry is a .cmd must be invoked
// through the command processor, not passed to CreateProcess directly.
func TestInteractiveCommandWindowsBatch(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only batch shim behavior")
	}
	dir := t.TempDir()
	shim := filepath.Join(dir, "opencode.cmd")
	if err := os.WriteFile(shim, []byte("@echo off\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd := interactiveCommand("opencode", "attach", "http://localhost:4096")
	if filepath.Base(cmd.Args[0]) != "cmd.exe" {
		t.Fatalf("expected cmd.exe wrapper for a .cmd shim; args[0] = %q", cmd.Args[0])
	}
	if len(cmd.Args) < 3 || cmd.Args[1] != "/c" || !strings.EqualFold(cmd.Args[2], shim) {
		t.Errorf("expected `cmd.exe /c <shim>`; args = %v", cmd.Args)
	}
}
