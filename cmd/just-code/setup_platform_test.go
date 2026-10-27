package main

import (
	"bufio"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/etalab-ia/just-code/internal/justcode"
)

// TestSetupPlatformStageUnreachableRetryThenContinue pins the retry answer for a
// non-Albert platform: C stores unvalidated, R re-asks.
func TestSetupPlatformStageUnreachableRetryThenContinue(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	fs := justcode.DefaultFS
	j := justcode.SetupJournal{SchemaVersion: 1, Stage: justcode.StageCredential, Platforms: []justcode.SetupPlatform{{Kind: "mistral"}}}
	store := &fakeSetupStore{}
	calls := 0
	w := justcode.SetupWizard{
		FS: fs, StateDir: stateDir,
		Store: func(context.Context) (justcode.SetupStore, error) { return store, nil },
		ValidateProvider: func(context.Context, justcode.InferenceProvider, string) justcode.CredentialProbe {
			calls++
			if calls == 1 {
				return justcode.CredentialProbe{Unreachable: true, Detail: "dial tcp: timeout"}
			}
			return justcode.CredentialProbe{}
		},
	}
	if _, err := setupPlatformStage(context.Background(), w, bufio.NewReader(strings.NewReader("k1\nr\nk2\n")), true, &j, false, fs, stateDir); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if store.puts[justcode.CredentialMistral] != "k2" || calls != 2 {
		t.Fatalf("retry path: puts=%+v calls=%d", store.puts, calls)
	}
}

func TestSetupPlatformStageAbortsOnUnansweredRetry(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	fs := justcode.DefaultFS
	j := justcode.SetupJournal{SchemaVersion: 1, Stage: justcode.StageCredential, Platforms: []justcode.SetupPlatform{{Kind: "mistral"}}}
	store := &fakeSetupStore{}
	w := justcode.SetupWizard{
		FS: fs, StateDir: stateDir,
		Store: func(context.Context) (justcode.SetupStore, error) { return store, nil },
		ValidateProvider: func(context.Context, justcode.InferenceProvider, string) justcode.CredentialProbe {
			return justcode.CredentialProbe{Unreachable: true, Detail: "down"}
		},
	}
	_, err := setupPlatformStage(context.Background(), w, bufio.NewReader(strings.NewReader("k\n")), true, &j, false, fs, stateDir)
	if err == nil {
		t.Fatal("EOF at the retry prompt must cancel, not store an unvalidated key")
	}
	if len(store.puts) != 0 {
		t.Fatalf("nothing may be stored: %+v", store.puts)
	}
	var rejected justcode.RejectedCredentialError
	if errors.As(err, &rejected) {
		t.Fatal("unreachable is not a rejection")
	}
}
