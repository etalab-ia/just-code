package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// TestInitMultiSelectsAnnounceSelectionKeys pins #156: the wizard's
// MultiSelects must say how to toggle an item, because Enter only confirms
// the list and testers completed init believing Enter had selected the
// highlighted item. The assertion runs against the rendered group content —
// the text the user sees — not the source constants.
func TestInitMultiSelectsAnnounceSelectionKeys(t *testing.T) {
	assertHint := func(name string, field huh.Field) {
		t.Helper()
		content := huh.NewGroup(field).Content()
		for _, want := range []string{"Space or x toggles an item", "Enter confirms the list"} {
			if !strings.Contains(content, want) {
				t.Fatalf("%s must announce %q in its description, got:\n%s", name, want, content)
			}
		}
	}
	skillsValue := []string{}
	assertHint("Project skills", newProjectSkillsMultiSelect(
		[]huh.Option[string]{huh.NewOption("pdf", "pdf")}, &skillsValue, "[1/3] "))
	mcpValue := []string{}
	assertHint("MCPs", newProjectMCPsMultiSelect(
		[]huh.Option[string]{huh.NewOption("data-gouv", "data-gouv")}, &mcpValue))
	dotenvValue := []string{}
	assertHint("Dotenv files", newDotenvFilesMultiSelect(
		[]huh.Option[string]{huh.NewOption(".env", ".env")}, &dotenvValue))
}

func TestInitSkillsAreTheSixDescribedOfficialEntries(t *testing.T) {
	catalogue := []justcode.ProjectSkill{
		{ID: "experimental/presentation-docs", Experimental: true},
		{ID: "official/datagouv-apis"},
		{ID: "official/lasuite-ui-kit"},
		{ID: "official/react-dsfr"},
		{ID: "official/rgaa"},
		{ID: "official/anssi-guides"},
		{ID: "official/securite-developpement"},
	}
	options := initSkillOptions(catalogue)
	if len(options) != 6 {
		t.Fatalf("skill option count = %d, want 6", len(options))
	}
	for i, option := range options {
		if option.Value != initSkillOrder[i] || !strings.Contains(option.Key, initSkillDescriptions[option.Value]) {
			t.Errorf("option %d = %q (%q), want %q with its short description", i, option.Key, option.Value, initSkillOrder[i])
		}
	}
	selected := []string{"official/securite-developpement"}
	localOnly := false
	content := newInitSkillsGroup(options, &selected, &localOnly, true, true, "[1/3] ").Content()
	for _, want := range []string{"Space or x toggles", "Enter confirms", "règles, références, exemples"} {
		if !strings.Contains(content, want) {
			t.Errorf("skills screen missing %q: %s", want, content)
		}
	}
	if got := initSkillsTitle(1, "[1/3] "); got != "[1/3] Skills (facultatif) — 1 skill sélectionnée" {
		t.Errorf("one-skill title = %q", got)
	}
	if got := initSkillsTitle(0, ""); !strings.Contains(got, "0 skills sélectionnées") {
		t.Errorf("zero-skill title = %q", got)
	}
	for _, want := range []string{"Où enregistrer les skills ?", "Avec le projet", "Sur ce poste uniquement", "rien n'est ajouté au dépôt"} {
		if !strings.Contains(content, want) {
			t.Errorf("skills screen missing storage detail %q: %s", want, content)
		}
	}
}

func TestInitHuhWizardDefaultsSecuritySkillForNewProject(t *testing.T) {
	root := initTestProject(t)
	originalCatalogue, originalHuhRun := projectSkillCatalogueFn, huhRun
	projectSkillCatalogueFn = func(context.Context) ([]justcode.ProjectSkill, error) {
		return initSkillFixtureCatalogue(), nil
	}
	huhRun = func(*huh.Form) error { return nil }
	t.Cleanup(func() {
		projectSkillCatalogueFn, huhRun = originalCatalogue, originalHuhRun
	})
	set := map[string]bool{
		"root": true, "runtime": true, "isolation": true, "model": true,
		"cpus": true, "memory-mb": true, "credential-ref": true,
		"github": true, "mcps": true, "skills-storage": true,
	}
	opts := initOptions{Set: set, hostCPUs: 8, hostMemoryMB: 16384, maxCPUs: 8, maxMemoryMB: 8192}
	got, err := askInitQuestionsHuh(opts, justcode.InitAnswers{Root: root})
	if err != nil {
		t.Fatalf("askInitQuestionsHuh: %v", err)
	}
	if !got.SkillsSet || !equalStringSlices(got.Skills, []string{"official/securite-developpement"}) {
		t.Fatalf("new-project default selection = %+v", got)
	}
}

// Regression test for the zero-skills retry loop: the MCP and storage
// choices submitted in the same pass as an empty skill selection must be
// committed before the user declines the confirmation, because the next
// form pass reseeds from answers and would otherwise reset them.
func TestCommitInitHuhSubmissionKeepsSiblingChoicesForEmptySkillsRetry(t *testing.T) {
	options := initSkillOptions(initSkillFixtureCatalogue())
	answers := justcode.InitAnswers{
		Skills:          []string{"official/rgaa"},
		MCPConnectors:   []string{"github"},
		SkillsLocalOnly: false,
	}
	opts := initOptions{Set: map[string]bool{}}
	needsSkillsConfirm, retryMCPs, err := commitInitHuhSubmission(opts, &answers, nil, []string{"github", "browser"}, true, options, false)
	if err != nil {
		t.Fatal(err)
	}
	if retryMCPs {
		t.Fatal("a non-empty MCP submission must not reopen the form")
	}
	if !needsSkillsConfirm {
		t.Fatal("an empty skills submission must request the zero-skills confirmation")
	}
	if !answers.SkillsSet || len(answers.Skills) != 0 {
		t.Fatalf("empty submission = %+v", answers)
	}
	if !answers.MCPsSet || !equalStringSlices(answers.MCPConnectors, []string{"github", "browser"}) {
		t.Fatalf("sibling MCP choice lost before the retry: %+v", answers)
	}
	if !answers.SkillsLocalOnlySet || !answers.SkillsLocalOnly {
		t.Fatalf("sibling storage choice lost before the retry: %+v", answers)
	}
}

func TestCommitInitHuhSubmissionRecordsRetainedSkills(t *testing.T) {
	options := initSkillOptions(initSkillFixtureCatalogue())
	answers := justcode.InitAnswers{Skills: []string{"official/rgaa", "official/ancienne"}}
	// The MCP answer is flag-provided so the zero-MCP confirmation cannot
	// run: this test pins the skills write-back, not the MCP gate.
	opts := initOptions{Set: map[string]bool{"mcps": true}}
	needsSkillsConfirm, retryMCPs, err := commitInitHuhSubmission(opts, &answers, []string{"official/rgaa", "official/ancienne"}, nil, false, options, false)
	if err != nil {
		t.Fatal(err)
	}
	if needsSkillsConfirm || retryMCPs {
		t.Fatal("a non-empty skills submission must not request any confirmation")
	}
	if answers.SkillsSet {
		t.Fatalf("an unchanged submission must not mark the selection explicit: %+v", answers)
	}
	if !equalStringSlices(answers.RetainedSkills, []string{"official/ancienne"}) {
		t.Fatalf("retained skills = %v, want the out-of-catalogue selection", answers.RetainedSkills)
	}
}

// Declining the zero-MCP confirmation must reopen the form without writing
// the empty MCP selection back and without committing the storage choice
// submitted in the same pass: the next pass reseeds from answers, like the
// line wizard.
func TestCommitInitHuhSubmissionDeclinedMCPConfirmationKeepsPreviousSelection(t *testing.T) {
	originalHuhRun := huhRun
	// confirmNoMCPsHuh binds a false keep value, so a no-op stub declines.
	huhRun = func(*huh.Form) error { return nil }
	t.Cleanup(func() { huhRun = originalHuhRun })
	options := initSkillOptions(initSkillFixtureCatalogue())
	answers := justcode.InitAnswers{
		Skills:        []string{"official/rgaa"},
		MCPConnectors: []string{"github"},
	}
	opts := initOptions{Set: map[string]bool{}}
	needsSkillsConfirm, retryMCPs, err := commitInitHuhSubmission(opts, &answers, []string{"official/rgaa"}, nil, true, options, false)
	if err != nil {
		t.Fatal(err)
	}
	if !retryMCPs {
		t.Fatal("declining the zero-MCP confirmation must reopen the form")
	}
	if needsSkillsConfirm {
		t.Fatal("a non-empty skills submission must not request the zero-skills confirmation")
	}
	if !equalStringSlices(answers.MCPConnectors, []string{"github"}) {
		t.Fatalf("declining must keep the previous MCP selection: %+v", answers)
	}
	if answers.MCPsSet {
		t.Fatalf("declining must not mark the MCP selection explicit: %+v", answers)
	}
	if answers.SkillsLocalOnlySet || answers.SkillsLocalOnly {
		t.Fatalf("declining must not commit the storage choice: %+v", answers)
	}
}

func TestHuhDiagnosticsPreservesLiteralPaths(t *testing.T) {
	path := `C:\Users\Luis\project_with_under`
	got := huhDiagnosticsDescription(justcode.DiagnosticsReport{
		Sections: []justcode.DiagnosticSection{{
			Title: "Host capability",
			Items: []justcode.DiagnosticItem{{Name: "Project path", Value: path, Status: justcode.DiagnosticInfo}},
		}},
	})
	if !strings.Contains(got, path) {
		t.Fatalf("TUI report changed a literal path: %q", got)
	}
	if strings.Contains(got, `\\_`) || strings.Contains(got, `\\\\`) {
		t.Fatalf("TUI report Markdown-escaped copied support data: %q", got)
	}
}

func TestHuhDiagnosticsPreservesWindowsSeparatorsAndUnderscores(t *testing.T) {
	backslash := string(rune(92))
	path := strings.Join([]string{"C:", "Users", "Luis", "project_with_under"}, backslash)
	got := huhDiagnosticsDescription(justcode.DiagnosticsReport{
		Sections: []justcode.DiagnosticSection{{
			Title: "Host capability",
			Items: []justcode.DiagnosticItem{{Name: "Project path", Value: path, Status: justcode.DiagnosticInfo}},
		}},
	})
	if !strings.Contains(got, path) {
		t.Fatalf("TUI report changed a Windows path: %q", got)
	}
	if strings.Contains(got, backslash+backslash) || strings.Contains(got, backslash+"_") {
		t.Fatalf("TUI report escaped literal support data: %q", got)
	}
}

func TestHuhResourceInputsCanRestoreImplicitDefaults(t *testing.T) {
	for _, input := range []string{"", "  ", "default", "DEFAULT"} {
		cpus, useDefault, err := parseGuestCPUsInput(input, 8)
		if err != nil || !useDefault || cpus != 0 {
			t.Errorf("CPU input %q = %d, %v, %v; want implicit default", input, cpus, useDefault, err)
		}
		memory, useDefault, err := parseGuestMemoryInput(input, 8192)
		if err != nil || !useDefault || memory != 0 {
			t.Errorf("memory input %q = %d, %v, %v; want implicit default", input, memory, useDefault, err)
		}
	}
	if cpus, reset, err := parseGuestCPUsInput("4", 8); err != nil || reset || cpus != 4 {
		t.Fatalf("explicit CPU input = %d, %v, %v; want 4", cpus, reset, err)
	}
	if memory, reset, err := parseGuestMemoryInput("2.5G", 8192); err != nil || reset || memory != 2560 {
		t.Fatalf("explicit memory input = %d, %v, %v; want 2560 MiB", memory, reset, err)
	}
}

func TestHuhReviewDescriptionUsesTableAndKeepsWarningsSeparate(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	plan := justcode.InitPlan{
		Answers: justcode.InitAnswers{
			Root: "/tmp/example_project", Runtime: justcode.RuntimeMicrosandbox,
			Isolation: justcode.IsolationFull, CredentialRef: "work",
			MCPConnectors: []string{"context7"}, Skills: []string{"official/rgaa"},
		},
		GuestCPUs: 2, GuestMemoryMB: 2560,
		ManifestPath: "/tmp/example-project/.just-code/project.json",
		LockPath:     "/tmp/example-project/.just-code/lock.json",
		Warnings:     []string{"the guest will use all 2 logical host CPUs"},
	}
	got := huhReviewDescription(plan)
	for _, want := range []string{"Setting", "Selection", `example\_project`, "2.5G", "context7", "official/rgaa", "Warnings", "all 2 logical host CPUs"} {
		if !strings.Contains(got, want) {
			t.Errorf("Huh recap table lacks %q: %s", want, got)
		}
	}
	t.Setenv("TERM", "dumb")
	if accessible := huhReviewDescription(plan); !strings.Contains(accessible, "example_project") || strings.Contains(accessible, `example\_project`) {
		t.Fatalf("accessible recap must retain the literal project path: %s", accessible)
	}
}

func TestHuhReviewDescriptionEscapesMarkupOnlyForStyledRenderer(t *testing.T) {
	plan := justcode.InitPlan{
		Answers:   justcode.InitAnswers{Root: "/tmp/project_with_under"},
		GuestCPUs: 2, GuestMemoryMB: 4096,
	}
	t.Setenv("TERM", "xterm-256color")
	styled := huhReviewDescription(plan)
	if !strings.Contains(styled, `project\_with\_under`) {
		t.Fatalf("styled Huh note must escape underscore markup: %s", styled)
	}
	t.Setenv("TERM", "dumb")
	accessible := huhReviewDescription(plan)
	if !strings.Contains(accessible, "project_with_under") || strings.Contains(accessible, `project\_with\_under`) {
		t.Fatalf("accessible Huh note must preserve the raw path: %s", accessible)
	}
}

func TestIsTTYFileRejectsDevNull(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()
	if isTTYFile(devNull) {
		t.Fatalf("%s must not be accepted as an interactive output terminal", os.DevNull)
	}
}

func TestRetainedSkillOptionsPreserveUnavailablePins(t *testing.T) {
	catalogue := []justcode.ProjectSkill{{ID: "official/securite-developpement"}}
	options := retainedSkillOptions(initSkillOptions(catalogue), []string{
		"official/securite-developpement", "official/rgaa", "experimental/rag-parse",
	})
	if len(options) != 3 {
		t.Fatalf("options = %d, want one offered and two retained", len(options))
	}
	if options[0].Value != "official/securite-developpement" ||
		options[1].Value != "official/rgaa" || options[2].Value != "experimental/rag-parse" {
		t.Fatalf("retained option values = %v, %v, %v", options[0].Value, options[1].Value, options[2].Value)
	}
	for _, option := range options[1:] {
		if !strings.Contains(option.Key, "déjà configurée, non proposée") {
			t.Errorf("retained option %q is not distinguished from new choices", option.Key)
		}
	}
	selected := []string{"official/securite-developpement", "official/rgaa", "experimental/rag-parse"}
	localOnly := false
	content := newInitSkillsGroup(options, &selected, &localOnly, true, false, "").Content()
	for _, id := range []string{"official/rgaa (déjà configurée, non proposée)", "experimental/rag-parse (déjà configurée, non proposée)"} {
		if !strings.Contains(content, id) {
			t.Errorf("retained option is missing from the form: %s", content)
		}
	}
}

func TestInitSkillsStepPrefixIsOmittedForPartialWizard(t *testing.T) {
	if got := initSkillsStepPrefix(initOptions{}); got != "[1/3] " {
		t.Fatalf("full wizard prefix = %q", got)
	}
	for _, field := range []string{"root", "runtime", "isolation", "model", "cpus", "memory-mb", "credential-ref", "github"} {
		if got := initSkillsStepPrefix(initOptions{Set: map[string]bool{field: true}}); got != "" {
			t.Errorf("prefix with %q supplied = %q", field, got)
		}
	}
}

func TestDecliningNoSkillsConfirmationKeepsTheReselectionEmpty(t *testing.T) {
	answers := justcode.InitAnswers{Skills: []string{"official/securite-developpement"}}
	keepEmptyInitSkillsForRetry(&answers)
	if !answers.SkillsSet || len(answers.Skills) != 0 {
		t.Fatalf("retry state = SkillsSet:%v Skills:%v; want explicit empty selection", answers.SkillsSet, answers.Skills)
	}
}

func TestRetainedMCPOptions(t *testing.T) {
	options := []huh.Option[string]{
		huh.NewOption("data-gouv — rechercher les données", "data-gouv"),
		huh.NewOption("chrome-devtools — navigateur", "chrome-devtools"),
	}
	// A withdrawn id still referenced by the manifest is appended as a
	// retained option labelled with its withdrawal reason; offered ids are
	// not duplicated and the input slice is not mutated.
	got := retainedMCPOptions(options, []string{"data-gouv", "context7"})
	if len(got) != 3 {
		t.Fatalf("expected 3 options, got %d", len(got))
	}
	if got[2].Value != "context7" {
		t.Fatalf("expected retained option value context7, got %q", got[2].Value)
	}
	if !strings.Contains(got[2].Key, "n'est plus proposé par just-code") {
		t.Fatalf("retained option label must name the withdrawal, got %q", got[2].Key)
	}
	if len(options) != 2 {
		t.Fatalf("input options mutated: %d", len(options))
	}
	// An unknown id (typo in a hand-edited manifest) is not offered for
	// re-selection: only currently or previously offered ids come back.
	if got = retainedMCPOptions(options, []string{"mcp/memory"}); len(got) != 2 {
		t.Fatalf("unknown id must not gain an option, got %d", len(got))
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
