package justcode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// This file implements the P11 setup journal: the persisted progress record
// that makes an interrupted setup resumable. The wizard runs ordered stages
// (preflight, credential, identity, apply); each stage's inputs and outcome
// are journaled to host state, so a retry starts where it stopped instead of
// re-asking for the Albert key it already holds. The journal carries no
// secret values — a stored credential lives in the P08 store, referenced by
// kind only.

// SetupStage is one wizard stage.
type SetupStage string

const (
	// StagePreflight checks platform, virtualization and disk. Read-only.
	StagePreflight SetupStage = "preflight"
	// StageCredential stores the Albert credential (and optionally GitHub).
	StageCredential SetupStage = "credential"
	// StageIdentity records the git identity.
	StageIdentity SetupStage = "identity"
	// StageSettings writes the resolved global settings.
	StageSettings SetupStage = "settings"
	// StageRuntime installs the managed Microsandbox runtime.
	StageRuntime SetupStage = "runtime"
	// StageDone marks a completed setup (the journal is removed).
	StageDone SetupStage = "done"
)

// setupStageOrder is the execution order; the journal resumes at the first
// incomplete stage.
var setupStageOrder = []SetupStage{
	StagePreflight, StageCredential, StageIdentity, StageSettings, StageRuntime,
}

// SetupJournal is the persisted wizard state.
type SetupJournal struct {
	SchemaVersion int `json:"schemaVersion"`
	// Stage is the next stage to run.
	Stage SetupStage `json:"stage"`
	// Preflight records the preflight outcome, so a retry does not re-run
	// a fatal check the user already saw.
	Preflight SetupPreflight `json:"preflight,omitempty"`
	// CredentialKinds lists the kinds stored so far (never values).
	CredentialKinds []string `json:"credentialKinds,omitempty"`
	// GitName and GitEmail are the chosen identity.
	GitName  string `json:"gitName,omitempty"`
	GitEmail string `json:"gitEmail,omitempty"`
	// DefaultModel is the resolved model selection, when one was chosen.
	DefaultModel string `json:"defaultModel,omitempty"`
}

const setupJournalSchemaVersion = 1

// SetupPreflight is the read-only diagnosis result.
type SetupPreflight struct {
	// Platform is runtime.GOOS/GOARCH.
	Platform string
	// VirtualizationOK reports whether the runtime's own doctor accepts the
	// host (KVM on Linux, the framework on macOS).
	VirtualizationOK bool
	// VirtualizationDetail is the doctor's output or failure summary.
	VirtualizationDetail string
	// DiskFreeBytes is the free space of the runtime's state home.
	DiskFreeBytes int64
	// DiskOK reports whether DiskFreeBytes clears the minimum.
	DiskOK bool
	// DiskDetail carries a disk-probe failure (kept separate from the
	// virtualization detail: they are different failures).
	DiskDetail string
	// RuntimeInstalled reports whether the managed runtime is already
	// present and trusted.
	RuntimeInstalled bool
	// Fatal records whether any check makes installation impossible.
	Fatal bool
}

// SetupMinDiskBytes is the minimum free disk for the managed runtime plus
// guest state. The archive is bounded at 128 MiB; the extracted runtime plus
// a first guest easily reaches hundreds of MiB, so the gate is 1 GiB.
const SetupMinDiskBytes = 1 << 30

// setupJournalPath is the journal location, in host state.
func setupJournalPath(stateDir string) string {
	return filepath.Join(stateDir, "setup-journal.json")
}

// SetupJournalPath exposes the journal path for the CLI.
func SetupJournalPath(stateDir string) string { return setupJournalPath(stateDir) }

// ReadSetupJournal loads the journal; a missing file means a fresh start.
func ReadSetupJournal(fs FS, stateDir string) (SetupJournal, error) {
	data, err := fs.ReadFile(setupJournalPath(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return SetupJournal{SchemaVersion: setupJournalSchemaVersion, Stage: StagePreflight}, nil
		}
		return SetupJournal{}, err
	}
	var j SetupJournal
	if err := json.Unmarshal(data, &j); err != nil {
		return SetupJournal{}, fmt.Errorf("setup journal: %w", err)
	}
	if j.SchemaVersion > setupJournalSchemaVersion {
		return SetupJournal{}, fmt.Errorf("setup journal: schemaVersion %d is newer than this build supports (%d)", j.SchemaVersion, setupJournalSchemaVersion)
	}
	return j, nil
}

// WriteSetupJournal atomically persists the journal.
func WriteSetupJournal(fs FS, stateDir string, j SetupJournal) error {
	j.SchemaVersion = setupJournalSchemaVersion
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	if err := fs.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	return atomicWrite(fs, setupJournalPath(stateDir), append(data, '\n'), 0o600)
}

// RemoveSetupJournal drops the journal (setup done or cancelled).
func RemoveSetupJournal(fs FS, stateDir string) error {
	err := fs.Remove(setupJournalPath(stateDir))
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	return err
}

// sortedCredentialKinds returns the stored kinds in stable order.
func (j SetupJournal) sortedCredentialKinds() []string {
	out := append([]string(nil), j.CredentialKinds...)
	sort.Strings(out)
	return out
}

// ContainsCredentialKind reports whether the journal has stored the kind.
func ContainsCredentialKind(kinds []string, kind string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}
