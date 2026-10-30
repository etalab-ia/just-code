package main

import (
	"bufio"
	"context"
	"errors"
	"strings"
	"testing"

	"charm.land/huh/v2"
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

func TestChooseSetupPlatformsTextMultiSelectAndEOF(t *testing.T) {
	forceNonTTY(t)
	chosen, err := chooseSetupPlatforms(nil, bufio.NewReader(strings.NewReader("2,3\n")))
	if err != nil {
		t.Fatalf("choose platforms: %v", err)
	}
	if len(chosen) != 2 || chosen[0].Kind != string(justcode.ProviderMistral) || chosen[1].Kind != string(justcode.ProviderOpenAICompatible) {
		t.Fatalf("selection = %+v", chosen)
	}
	if _, err := chooseSetupPlatforms(nil, bufio.NewReader(strings.NewReader(""))); err == nil {
		t.Fatal("EOF must cancel instead of accepting the fresh Albert default")
	}
}

func TestChooseSetupPlatformsTextCanRemoveAlbert(t *testing.T) {
	forceNonTTY(t)
	chosen, err := chooseSetupPlatforms([]justcode.SetupPlatform{{Kind: string(justcode.ProviderAlbert)}, {Kind: string(justcode.ProviderMistral)}}, bufio.NewReader(strings.NewReader("2\n")))
	if err != nil {
		t.Fatalf("choose platforms: %v", err)
	}
	if len(chosen) != 1 || chosen[0].Kind != string(justcode.ProviderMistral) {
		t.Fatalf("Albert was not removed: %+v", chosen)
	}
	if _, err := chooseSetupPlatforms([]justcode.SetupPlatform{{Kind: string(justcode.ProviderAlbert)}}, bufio.NewReader(strings.NewReader("\n"))); err != nil {
		t.Fatalf("an explicit empty-line confirmation should retain current choices: %v", err)
	}
}

func TestChooseSetupPlatformsHuhRunsMultiSelect(t *testing.T) {
	originalHuh, originalRun := wizardHuhFn, huhRun
	wizardHuhFn = func() bool { return true }
	calls := 0
	huhRun = func(form *huh.Form) error {
		calls++
		if form == nil {
			t.Fatal("provider selection form was not built")
		}
		return nil
	}
	t.Cleanup(func() { wizardHuhFn, huhRun = originalHuh, originalRun })
	chosen, err := chooseSetupPlatforms(nil, bufio.NewReader(strings.NewReader("")))
	if err != nil || calls != 1 {
		t.Fatalf("Huh selector: calls=%d err=%v", calls, err)
	}
	if len(chosen) != 1 || chosen[0].Kind != string(justcode.ProviderAlbert) {
		t.Fatalf("Huh default selection = %+v", chosen)
	}
}
