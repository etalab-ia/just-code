package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"charm.land/huh/v2"
	"github.com/etalab-ia/just-code/internal/justcode"
)

// The huh form itself needs a TTY, so the regression tests pin the decision
// logic around it: which fields the form must omit so a hidden or failed
// input can never clobber an existing project selection.

func TestShouldSkipSkillsField(t *testing.T) {
	cases := []struct {
		name         string
		skillsFlag   bool
		catalogueErr error
		want         bool
	}{
		{"flag supplied", true, nil, true},
		{"catalogue ok", false, nil, false},
		{"catalogue failed", false, errors.New("offline"), true},
		{"flag supplied even on catalogue failure", true, errors.New("offline"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := initOptions{Set: map[string]bool{"skills": tc.skillsFlag}}
			if got := shouldSkipSkillsField(opts, tc.catalogueErr); got != tc.want {
				t.Fatalf("shouldSkipSkillsField = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRetainedSkillOptions(t *testing.T) {
	catalogue := []justcode.ProjectSkill{{ID: "official/rgaa"}, {ID: "official/react-dsfr"}}
	options := []huh.Option[string]{
		huh.NewOption("official/rgaa", "official/rgaa"),
		huh.NewOption("official/react-dsfr", "official/react-dsfr"),
	}
	// A pinned ID missing from today's catalogue is appended as a
	// retained option; catalogue IDs are not duplicated.
	got := retainedSkillOptions(options, catalogue, []string{"official/rgaa", "official/ghost-skill"})
	if len(got) != 3 {
		t.Fatalf("expected 3 options, got %d", len(got))
	}
	if got[2].Value != "official/ghost-skill" {
		t.Fatalf("expected retained option value official/ghost-skill, got %q", got[2].Value)
	}
	if got[2].Key != "official/ghost-skill (pinned at an older revision)" {
		t.Fatalf("unexpected retained option label %q", got[2].Key)
	}
	// The input options slice must not be mutated.
	if len(options) != 2 {
		t.Fatalf("input options mutated: %d", len(options))
	}
}

func TestEqualStringSets(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{"identical", []string{"a", "b"}, []string{"a", "b"}, true},
		{"reordered", []string{"b", "a"}, []string{"a", "b"}, true},
		{"different members", []string{"a", "c"}, []string{"a", "b"}, false},
		{"different lengths", []string{"a"}, []string{"a", "b"}, false},
		{"both empty", nil, nil, true},
		{"duplicates differ", []string{"a", "a"}, []string{"a", "b"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := equalStringSets(tc.a, tc.b); got != tc.want {
				t.Fatalf("equalStringSets(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
	// Inputs must not be mutated by the comparison.
	a := []string{"b", "a"}
	equalStringSets(a, []string{"a", "b"})
	if a[0] != "b" || a[1] != "a" {
		t.Fatalf("input mutated: %v", a)
	}
}

func TestEqualStringSlices(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"empty vs nil", []string{}, nil, true},
		{"same order", []string{"a", "b"}, []string{"a", "b"}, true},
		{"different order", []string{"a", "b"}, []string{"b", "a"}, false},
		{"different length", []string{"a"}, []string{"a", "b"}, false},
		{"different values", []string{"a", "b"}, []string{"a", "c"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := equalStringSlices(tc.a, tc.b); got != tc.want {
				t.Fatalf("equalStringSlices(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// A reseed after an interactive root change must keep flag-provided values
// explicit: seedInitAnswersFromManifest only overwrites fields the flags did
// not supply, so --skill/--mcp selections survive pointing the wizard at a
// different project.
func TestSeedInitAnswersKeepsFlagValues(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, justcode.ProjectManifest{Skills: []string{"manifest-skill"}, MCPConnectors: []string{"browser"}})
	opts := initOptions{Set: map[string]bool{
		"skills":         true,
		"mcps":           true,
		"skills-storage": true,
	}}
	answers := justcode.InitAnswers{
		Root:            root,
		Skills:          []string{"flagged-skill"},
		SkillsSet:       true,
		MCPConnectors:   []string{"github"},
		MCPsSet:         true,
		SkillsLocalOnly: true,
	}
	seeded := seedInitAnswersFromManifest(opts, answers)
	if !seeded.SkillsSet {
		t.Fatal("flag-supplied skills must stay explicit across a reseed")
	}
	if !equalStringSlices(seeded.Skills, []string{"flagged-skill"}) {
		t.Fatalf("flag-supplied skills overwritten by reseed: %v", seeded.Skills)
	}
	if !seeded.MCPsSet {
		t.Fatal("flag-supplied MCPs must stay explicit across a reseed")
	}
	if !equalStringSlices(seeded.MCPConnectors, []string{"github"}) {
		t.Fatalf("flag-supplied MCPs overwritten by reseed: %v", seeded.MCPConnectors)
	}
	if !seeded.SkillsLocalOnly {
		t.Fatal("flag-supplied storage mode must stay explicit across a reseed")
	}
}

// Without a storage flag, the reseed must load the target manifest's storage
// mode so the form's default matches the project instead of silently
// migrating host-local skills into versioned storage.
func TestSeedInitAnswersLoadsStorageMode(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, justcode.ProjectManifest{SkillsLocalOnly: true})
	opts := initOptions{Set: map[string]bool{}}
	answers := justcode.InitAnswers{Root: root}
	seeded := seedInitAnswersFromManifest(opts, answers)
	if !seeded.SkillsLocalOnly {
		t.Fatal("storage mode not seeded from target manifest")
	}
}

func writeManifest(t *testing.T, root string, manifest justcode.ProjectManifest) {
	t.Helper()
	manifest.SchemaVersion = 4
	dir := filepath.Join(root, ".just-code")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "project.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
