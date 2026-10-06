package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/etalab-ia/just-code/internal/justcode"
)

type albertCodeImportOptions struct {
	root            string
	apply           bool
	importAlbertKey bool
}

var resolveImportSkillsFn = justcode.ResolveProjectSkills
var verifyImportSkillsFn = func(ids []string, locks map[string]justcode.SkillLock) error {
	_, err := justcode.LoadLockedSkillPackages(ids, locks)
	return err
}
var bumpImportedCredentialGenerationFn = justcode.BumpCredentialGeneration
var importCredentialStoreFn = justcode.DefaultCredentialStore
var applyImportedProjectFn = func(wizard justcode.InitWizard, plan justcode.InitPlan) error {
	return wizard.Apply(plan, false)
}
var repairImportedProjectFn = func(wizard justcode.InitWizard, plan justcode.InitPlan) error {
	return wizard.Apply(plan, true)
}

func parseAlbertCodeImportArgs(args []string) (albertCodeImportOptions, error) {
	opts := albertCodeImportOptions{root: "."}
	if len(args) == 0 || args[0] != "albert-code" {
		return opts, fmt.Errorf("usage: just-code import albert-code [--root PATH] [--apply] [--import-albert-key]")
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--apply":
			if opts.apply {
				return opts, fmt.Errorf("--apply may be given only once")
			}
			opts.apply = true
		case "--import-albert-key":
			if opts.importAlbertKey {
				return opts, fmt.Errorf("--import-albert-key may be given only once")
			}
			opts.importAlbertKey = true
		case "--root":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return opts, fmt.Errorf("--root requires a directory path")
			}
			i++
			opts.root = args[i]
		default:
			return opts, fmt.Errorf("unknown import option %q", args[i])
		}
	}
	if opts.importAlbertKey && !opts.apply {
		return opts, fmt.Errorf("--import-albert-key requires --apply; preview does not store credentials")
	}
	return opts, nil
}

func albertCodeImportCmd(args []string) (int, error) {
	if (len(args) == 1 && args[0] == "--help") || (len(args) == 2 && args[0] == "albert-code" && args[1] == "--help") {
		fmt.Println("Usage: just-code import albert-code [--root PATH] [--apply] [--import-albert-key]")
		fmt.Println("Preview is read-only. --apply writes project configuration; --import-albert-key separately imports a supported literal key from .env.")
		return 0, nil
	}
	opts, err := parseAlbertCodeImportArgs(args)
	if err != nil {
		return 2, err
	}
	plan, err := justcode.DiscoverAlbertCodeImport(opts.root)
	if err != nil {
		return 1, err
	}
	if len(plan.SkillNames) > 0 {
		catalogue, err := projectSkillCatalogueFn(context.Background())
		if err != nil {
			if opts.apply {
				return 1, fmt.Errorf("resolve legacy skills against the pinned official catalogue: %w", err)
			}
			plan.SkillCatalogueUnavailable = true
			plan.ReviewItems = append(plan.ReviewItems, "official skills catalogue unavailable; legacy skills cannot be resolved")
			sort.Strings(plan.ReviewItems)
		} else {
			plan.ResolveSkills(catalogue)
		}
	}
	if opts.importAlbertKey && !plan.CredentialAvailable {
		if plan.CredentialNeedsReview {
			return 2, fmt.Errorf(".env contains an unsupported or repeated ALBERT_API_KEY assignment; reduce it to one literal assignment before importing")
		}
		return 2, fmt.Errorf("--import-albert-key requires a literal ALBERT_API_KEY in the project .env")
	}
	if opts.importAlbertKey {
		fmt.Printf("This will store the ALBERT_API_KEY from %s/.env in the native credential store (%s); the value will not be displayed.\n", plan.Root, justcode.StoreAvailability())
	}
	fmt.Print(plan.Format())
	if !opts.apply {
		fmt.Println("No changes made.")
		return 0, nil
	}
	if len(plan.SourceFiles) == 0 {
		return 1, fmt.Errorf("no supported Albert Code project files were detected")
	}

	credentialRef := ""
	if opts.importAlbertKey {
		credentialRef = "albert"
	}

	answers := justcode.InitAnswers{
		Root:          plan.Root,
		Runtime:       justcode.RuntimeMicrosandbox,
		Isolation:     justcode.IsolationFull,
		Model:         plan.Model,
		CredentialRef: credentialRef,
		MCPConnectors: plan.MCPConnectors,
		MCPsSet:       true,
	}
	if plan.HasSkillsFile {
		answers.Skills, answers.SkillsSet = plan.Skills, true
	}
	wizard := justcode.InitWizard{
		FS:            justcode.DefaultFS,
		ResolveSkills: resolveImportSkillsFn,
		VerifySkills:  verifyImportSkillsFn,
	}
	setupPlan, err := wizard.Plan(answers)
	if err != nil {
		return 1, fmt.Errorf("build just-code import plan: %w", err)
	}
	for _, warning := range setupPlan.Warnings {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
	}
	existingMatches := false
	if existing, err := justcode.ReadProjectManifest(justcode.DefaultFS, setupPlan.ManifestPath); err == nil {
		rawManifest, readErr := readExistingImportManifest(setupPlan.ManifestPath)
		if readErr != nil {
			return 1, readErr
		}
		unknownFields, fieldErr := manifestHasUnknownFields(rawManifest)
		if fieldErr != nil {
			return 1, fieldErr
		}
		lock, lockErr := justcode.ReadLockfile(justcode.DefaultFS, setupPlan.LockPath)
		if lockErr != nil {
			return 1, lockErr
		}
		if unknownFields || !importMatchesManifest(setupPlan.Answers, existing) ||
			justcode.ValidateDependencySet(existing, lock) != nil ||
			!importSkillLocksMatch(setupPlan.SkillLocks, lock.Skills) {
			return 1, fmt.Errorf("%s already contains a just-code setup that differs from the import; review it with 'just-code init' rather than overwriting it", setupPlan.ManifestPath)
		}
		existingMatches = true
	} else if !os.IsNotExist(err) {
		return 1, err
	}

	if !existingMatches && plan.Model != "" {
		warning, err := catalogueModelWarningFn(plan.Root, plan.Model)
		if err != nil {
			return 1, fmt.Errorf("validate imported model: %w", err)
		}
		if warning != "" {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
		}
	}
	apply := func() error {
		return applyAlbertCodeImport(plan, wizard, setupPlan, opts.importAlbertKey, existingMatches)
	}
	if opts.importAlbertKey {
		if err := withAlbertCredentialImportLock(apply); err != nil {
			return 1, err
		}
	} else if err := apply(); err != nil {
		return 1, err
	}
	return 0, nil
}

func applyAlbertCodeImport(plan justcode.AlbertCodeImport, wizard justcode.InitWizard, setupPlan justcode.InitPlan, importKey, existingMatches bool) error {
	var store justcode.CredentialStore
	var credentialCreated bool
	var importedCredential string
	if importKey {
		store = importCredentialStoreFn()
		if store == nil {
			return fmt.Errorf("the native credential store is unavailable; use 'just-code auth add albert' to configure credentials manually")
		}
		data, err := readAlbertImportDotenv(filepath.Join(plan.Root, ".env"))
		if err != nil {
			return err
		}
		var available bool
		importedCredential, available = justcode.ReadLegacyAlbertCredential(string(data))
		if !available {
			if justcode.LegacyAlbertCredentialNeedsManualReview(string(data)) {
				return fmt.Errorf(".env contains an unsupported or repeated ALBERT_API_KEY assignment; reduce it to one literal assignment before importing")
			}
			return fmt.Errorf("no non-empty literal ALBERT_API_KEY was found in .env")
		}
		credentialCreated, err = storeImportedAlbertCredential(context.Background(), store, importedCredential)
		if err != nil {
			return err
		}
	}
	if existingMatches {
		if !setupPlan.InstructionsChanged {
			fmt.Println("This Albert Code project has already been imported; no project files changed.")
			return nil
		}
		if err := repairImportedProjectFn(wizard, setupPlan); err != nil {
			if removeErr := rollbackImportedCredential(context.Background(), store, credentialCreated, importedCredential); removeErr != nil {
				return fmt.Errorf("repair managed skill instructions: %v; newly imported credential could not be removed: %w", err, removeErr)
			}
			return fmt.Errorf("repair managed skill instructions: %w", err)
		}
		fmt.Printf("Reconciled the existing import and managed skill instructions in %s.\n", setupPlan.ManifestPath)
		return nil
	}
	if err := applyImportedProjectFn(wizard, setupPlan); err != nil {
		if removeErr := rollbackImportedCredential(context.Background(), store, credentialCreated, importedCredential); removeErr != nil {
			return fmt.Errorf("apply project import: %v; newly imported credential could not be removed: %w", err, removeErr)
		}
		return fmt.Errorf("apply project import: %w", err)
	}
	fmt.Printf("Imported the recognized settings into %s. Legacy configuration files were left untouched.\n", setupPlan.ManifestPath)
	if len(plan.Skills) > 0 {
		fmt.Println("AGENTS.md was updated only through the just-code managed skills section; existing text was preserved.")
	}
	return nil
}

func withAlbertCredentialImportLock(action func() error) error {
	lock := &justcode.ProjectLock{
		Path: filepath.Join(justcode.DefaultStateDir(), "albert-import-credential.lock"),
		FS:   justcode.DefaultFS,
	}
	release, err := lock.Acquire()
	if err != nil {
		return fmt.Errorf("acquire Albert credential import lock: %w", err)
	}
	defer release()
	return action()
}

func storeImportedAlbertCredential(ctx context.Context, store justcode.CredentialStore, value string) (bool, error) {
	stored, err := store.Get(ctx, justcode.CredentialAlbert)
	if err == nil {
		if stored != value {
			return false, fmt.Errorf("an Albert credential is already stored; refusing to replace it during import")
		}
		return false, nil
	}
	if !justcode.IsCredentialNotFound(err) {
		return false, fmt.Errorf("read native Albert credential status: %w", err)
	}
	if err := store.Put(ctx, justcode.CredentialAlbert, value); err != nil {
		return false, fmt.Errorf("store Albert credential: %w", err)
	}
	bumpImportedCredentialGenerationFn(justcode.CredentialAlbert)
	return true, nil
}

func rollbackImportedCredential(ctx context.Context, store justcode.CredentialStore, created bool, expected string) error {
	if !created {
		return nil
	}
	stored, err := store.Get(ctx, justcode.CredentialAlbert)
	if err != nil {
		if justcode.IsCredentialNotFound(err) {
			return nil
		}
		return err
	}
	if stored != expected {
		return fmt.Errorf("the Albert credential changed during import; refusing to remove it")
	}
	if err := store.Remove(ctx, justcode.CredentialAlbert); err != nil && !justcode.IsCredentialNotFound(err) {
		return err
	}
	bumpImportedCredentialGenerationFn(justcode.CredentialAlbert)
	return nil
}

func importMatchesManifest(expected justcode.InitAnswers, existing justcode.ProjectManifest) bool {
	if existing.Project != "" || existing.Storage != "" {
		return false
	}
	if existing.Runtime != string(expected.Runtime) || existing.Isolation != string(expected.Isolation) {
		return false
	}
	if existing.Model != expected.Model || existing.CPUs != expected.CPUs || existing.MemoryMB != expected.MemoryMB {
		return false
	}
	if existing.SkillsLocalOnly || !sameStrings(existing.Skills, expected.Skills) {
		return false
	}
	if !sameStrings(existing.MCPConnectors, expected.MCPConnectors) {
		return false
	}
	return existing.CredentialRef == expected.CredentialRef
}

func importSkillLocksMatch(expected, actual map[string]justcode.SkillLock) bool {
	for id, expectedLock := range expected {
		if actualLock, ok := actual[id]; !ok || actualLock != expectedLock {
			return false
		}
	}
	return true
}

func manifestHasUnknownFields(raw []byte) (bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return false, fmt.Errorf("read existing project manifest fields: %w", err)
	}
	known := map[string]bool{
		"schemaVersion": true, "project": true, "runtime": true, "isolation": true,
		"model": true, "cpus": true, "memoryMB": true, "credentialRef": true,
		"storage": true, "skills": true, "skillsLocalOnly": true,
		"mcpConnectors": true, "dependencySetId": true,
	}
	for name := range fields {
		if !known[name] {
			return true, nil
		}
	}
	return false, nil
}

func readExistingImportManifest(path string) ([]byte, error) {
	data, err := justcode.ReadAlbertCodeImportFile(path)
	if err != nil {
		return nil, fmt.Errorf("read existing import manifest %s: %w", path, err)
	}
	return data, nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func readAlbertImportDotenv(path string) ([]byte, error) {
	data, err := justcode.ReadAlbertCodeImportFile(path)
	if err != nil {
		return nil, fmt.Errorf("read project .env: %w", err)
	}
	return data, nil
}
