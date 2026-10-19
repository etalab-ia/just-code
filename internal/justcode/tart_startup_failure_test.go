package justcode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTartStartupFailureRecognisesTheVZRefusal pins the parser against the
// real log lines a headless Mac produces. The wording varies across macOS
// versions, so the markers are the error domains, not a full sentence.
func TestTartStartupFailureRecognisesTheVZRefusal(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		match bool
	}{
		{"vz domain with code", `VZErrorDomain Code=-9 "The virtual machine encountered a security error"`, true},
		{"host key failure", `NSPOSIXErrorDomain Code=22 "Failed to create new HostKey"`, true},
		{"tart prefix variant", `tart: VZErrorDomain Code=-9 "The virtual machine encountered a security error"`, true},
		// A normal boot must not be mistaken for a failure: reporting a false
		// cause would be worse than the timeout it replaces.
		{"ordinary boot line", "Booting opencode-tahoe-base-latest", false},
		{"guest talking", "opencode-tahoe-base-latest: login: ", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTartStartupFailure(tc.line); got != tc.match {
				t.Fatalf("isTartStartupFailure(%q) = %v, want %v", tc.line, got, tc.match)
			}
		})
	}
}

// TestWaitForAgentNamesTheVZFailureInsteadOfTimingOut pins #206: a VM that
// refused to boot must be reported as such, immediately, rather than waiting
// the full timeout and claiming the guest agent never answered.
func TestWaitForAgentNamesTheVZFailureInsteadOfTimingOut(t *testing.T) {
	// The exec probe never succeeds: the guest is up in name only, exactly as
	// when Virtualization.framework refused to start it.
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		return ExecResult{ExitCode: 1, Stderr: "no such process"}
	}}
	tt := newTestTart(t, r)
	tt.Config.Isolation = IsolationFull
	// Small seam so the early-exit assertion does not depend on a full timeout
	// run: without it this test takes a real minute when the fix is absent.
	tt.AgentWaitInterval = time.Millisecond
	tt.AgentWaitPolls = 3
	if err := os.MkdirAll(filepath.Dir(tt.LogPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tt.LogPath(), []byte(
		"Booting vm\n"+
			`VZErrorDomain Code=-9 "The virtual machine encountered a security error"`+"\n"+
			`NSPOSIXErrorDomain Code=22 "Failed to create new HostKey"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := tt.waitForAgent(context.Background())
	if err == nil {
		t.Fatal("a VM that refused to boot must be an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "security error") {
		t.Fatalf("the error must carry the VZ cause, got: %v", err)
	}
	if !strings.Contains(msg, "keychain") {
		t.Fatalf("the error must name the actionable cause, got: %v", err)
	}
	// The whole point is that it stops as soon as the log names the failure
	// rather than polling to exhaustion. With polls=3, the fixed code exits on
	// the first probe; an implementation that only checks the log after the
	// loop must consume all three.
	if len(r.calls) > 2 {
		t.Fatalf("the VM log must stop the poll early, got %d probes", len(r.calls))
	}
}

// TestWaitForAgentKeepsThePlainTimeoutWithoutALogFailure pins the other half:
// a guest that simply is not ready yet must still be polled, and must keep the
// original message when the log shows no startup failure.
func TestWaitForAgentKeepsThePlainTimeoutWithoutALogFailure(t *testing.T) {
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		return ExecResult{ExitCode: 1, Stderr: "not ready"}
	}}
	tt := newTestTart(t, r)
	tt.AgentWaitInterval = time.Millisecond
	tt.AgentWaitPolls = 5
	if err := os.MkdirAll(filepath.Dir(tt.LogPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tt.LogPath(), []byte("Booting vm\nstill booting\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The poll must run its full course and keep the original wording: no
	// failure may be invented from a healthy log.
	err := tt.waitForAgent(context.Background())
	if err == nil {
		t.Fatal("a guest that never answers must time out")
	}
	if !strings.Contains(err.Error(), "timed out waiting for") {
		t.Fatalf("a healthy log must keep the plain timeout, got: %v", err)
	}
	if strings.Contains(err.Error(), "VZErrorDomain") || strings.Contains(err.Error(), "keychain") {
		t.Fatalf("no startup failure may be invented: %v", err)
	}
	if len(r.calls) != 5 {
		t.Fatalf("the wait must poll the configured number of times, got %d", len(r.calls))
	}
}

// TestNewTartWiresTheAgentWaitSeam pins that the production constructor sets
// the wait seam, so the defaults are not only the loop's fallback.
func TestNewTartWiresTheAgentWaitSeam(t *testing.T) {
	tt := NewTart(Config{TartVM: "vm"})
	if tt.AgentWaitPolls != 60 {
		t.Fatalf("AgentWaitPolls = %d, want 60", tt.AgentWaitPolls)
	}
	if tt.AgentWaitInterval != time.Second {
		t.Fatalf("AgentWaitInterval = %v, want 1s", tt.AgentWaitInterval)
	}
}

// TestTartStartupFailureToleratesAMissingLog pins the safety property: the log
// is written by a live process, so an absent or unreadable file must stay
// silent rather than turn a slow boot into a false error.
func TestTartStartupFailureToleratesAMissingLog(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.log")
	if reason := tartStartupFailure(missing); reason != "" {
		t.Fatalf("a missing log must not report a failure, got %q", reason)
	}
}

// TestLoginKeychainLockedReadsSecurityOutput pins the doctor probe: only a
// locked keychain is a warning, and a probe that cannot run is not.
func TestLoginKeychainLockedReadsSecurityOutput(t *testing.T) {
	cases := []struct {
		name   string
		res    ExecResult
		locked bool
	}{
		{"unlocked", ExecResult{ExitCode: 0}, false},
		{"locked", ExecResult{ExitCode: 1, Stderr: "The specified keychain could not be found."}, false},
		{"locked wording", ExecResult{ExitCode: 1, Stderr: "keychain is locked"}, true},
		{"locked stdout", ExecResult{ExitCode: 1, Stdout: "The keychain is locked."}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRunner{onRun: func(string, []string) ExecResult { return tc.res }}
			got, _ := loginKeychainLocked(context.Background(), r)
			if got != tc.locked {
				t.Fatalf("loginKeychainLocked = %v, want %v", got, tc.locked)
			}
		})
	}
}

// TestLoginKeychainLockedIsSilentWhenSecurityIsAbsent pins that a probe which
// cannot run never blocks doctor or invents a warning.
func TestLoginKeychainLockedIsSilentWhenSecurityIsAbsent(t *testing.T) {
	r := &fakeRunner{onRun: func(string, []string) ExecResult {
		return ExecResult{ExitCode: 127, Stderr: "security: command not found"}
	}}
	if locked, detail := loginKeychainLocked(context.Background(), r); locked {
		t.Fatalf("an unusable probe must not warn, got %v (%s)", locked, detail)
	}
}
