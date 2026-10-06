package justcode

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	albertAgentsStart = "<!-- albert-code:agents:start -->"
	albertAgentsEnd   = "<!-- albert-code:agents:end -->"
	albertAPIEndpoint = "https://albert.api.etalab.gouv.fr/v1"
	maxImportFileSize = 1 << 20
)

var importedModelPattern = regexp.MustCompile(`^albert/[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
var errUnsafeImportFile = errors.New("not a regular file; refusing to read")

// AlbertCodeImport is a read-only interpretation of an existing Albert Code
// project. It deliberately carries names and decisions, never secret values
// or executable legacy configuration.
type AlbertCodeImport struct {
	Root                      string
	SkillNames                []string
	Skills                    []string
	MCPConnectors             []string
	Model                     string
	CredentialAvailable       bool
	CredentialNeedsReview     bool
	CredentialFile            string
	LegacyEnvKeys             []string
	RuntimeScriptPresent      bool
	ManagedAgentsPresent      bool
	MalformedAgentsMarkers    bool
	SourceFiles               []string
	ReviewItems               []string
	Warnings                  []string
	HasSkillsFile             bool
	SkillCatalogueUnavailable bool
	HasOpenCodeConfig         bool
	HasManagedAgentsFile      bool
}

// DiscoverAlbertCodeImport detects the documented legacy files and extracts
// only supported intent. It never writes, executes, or prints file contents.
func DiscoverAlbertCodeImport(root string) (AlbertCodeImport, error) {
	pc, err := DiscoverProject(root)
	if err != nil {
		return AlbertCodeImport{}, fmt.Errorf("project root: %w", err)
	}
	plan := AlbertCodeImport{Root: pc.Root}
	if !pc.IsGit {
		return plan, fmt.Errorf("project root %s is not a Git worktree; Albert Code import requires a Git worktree", pc.Root)
	}
	if err := ensureImportDirectory(filepath.Join(pc.Root, ".opencode")); err != nil {
		return plan, err
	}

	legacyStateDir := filepath.Join(pc.Root, ".albert-code")
	if info, err := os.Lstat(legacyStateDir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return plan, fmt.Errorf(".albert-code is not a regular directory; refusing import")
		}
	} else if !os.IsNotExist(err) {
		return plan, err
	}

	skillsPath := filepath.Join(pc.Root, ".albert-code", "skills.txt")
	if _, err := regularImportFile(skillsPath); err == nil {
		data, err := readImportFile(skillsPath)
		if err != nil {
			return plan, err
		}
		plan.HasSkillsFile = true
		plan.SourceFiles = append(plan.SourceFiles, ".albert-code/skills.txt")
		seen := map[string]bool{}
		for _, line := range strings.Split(string(data), "\n") {
			name := strings.TrimSpace(line)
			if name == "" {
				continue
			}
			if !skillNamePattern.MatchString(name) {
				plan.ReviewItems = append(plan.ReviewItems, ".albert-code/skills.txt contains an invalid skill name")
				continue
			}
			if !seen[name] {
				seen[name] = true
				plan.SkillNames = append(plan.SkillNames, name)
			}
		}
	} else if errors.Is(err, errUnsafeImportFile) {
		plan.Warnings = append(plan.Warnings, ".albert-code/skills.txt is not a regular file; it was not read")
	} else if !os.IsNotExist(err) {
		return plan, err
	} else if info, statErr := os.Lstat(filepath.Join(pc.Root, ".albert-code")); statErr == nil && info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
		plan.Warnings = append(plan.Warnings, ".albert-code exists but skills.txt is absent; partial setup will not be guessed")
	}

	selectedConfig := ""
	for _, rel := range opencodeConfigFiles {
		path := filepath.Join(pc.Root, rel)
		if _, err := regularImportFile(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if errors.Is(err, errUnsafeImportFile) {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s is not a regular file; it was not read", rel))
				continue
			}
			return plan, err
		}
		data, err := readImportFile(path)
		if err != nil {
			return plan, err
		}
		config, err := parseProjectOpenCodeConfig(data)
		if err != nil {
			return plan, fmt.Errorf("parse %s: %w", rel, err)
		}
		plan.HasOpenCodeConfig = true
		plan.SourceFiles = append(plan.SourceFiles, rel)
		selectedConfig = rel
		plan.importOpenCodeConfig(config)
		break
	}
	if plan.HasOpenCodeConfig {
		for _, rel := range opencodeConfigFiles {
			if rel == selectedConfig {
				continue
			}
			if _, err := os.Lstat(filepath.Join(pc.Root, rel)); err == nil {
				plan.ReviewItems = append(plan.ReviewItems, "multiple OpenCode config files exist; only the first file in discovery order is imported")
				break
			}
		}
	}

	agentsPath := filepath.Join(pc.Root, "AGENTS.md")
	if data, err := readImportFileIfPresent(agentsPath); errors.Is(err, errUnsafeImportFile) {
		plan.Warnings = append(plan.Warnings, "AGENTS.md is not a regular file; it was not read")
	} else if err != nil {
		return plan, err
	} else if data != nil {
		plan.HasManagedAgentsFile = true
		plan.SourceFiles = append(plan.SourceFiles, "AGENTS.md")
		starts, ends := strings.Count(string(data), albertAgentsStart), strings.Count(string(data), albertAgentsEnd)
		if starts == 1 && ends == 1 && strings.Index(string(data), albertAgentsStart) < strings.Index(string(data), albertAgentsEnd) {
			plan.ManagedAgentsPresent = true
		} else if starts > 0 || ends > 0 {
			plan.MalformedAgentsMarkers = true
			plan.ReviewItems = append(plan.ReviewItems, "AGENTS.md has unpaired or repeated Albert Code managed-zone markers; it will be left untouched")
		}
	}

	dotenvPath := filepath.Join(pc.Root, ".env")
	if data, err := readImportFileIfPresent(dotenvPath); errors.Is(err, errUnsafeImportFile) {
		plan.CredentialFile = ".env"
		plan.Warnings = append(plan.Warnings, ".env is not a regular file; it was not read or transferred")
	} else if err != nil {
		return plan, err
	} else if data != nil {
		plan.CredentialFile = ".env"
		plan.LegacyEnvKeys = ImportLegacyDotenv(string(data)).Keys
		_, plan.CredentialAvailable = ReadLegacyAlbertCredential(string(data))
		plan.CredentialNeedsReview = !plan.CredentialAvailable && LegacyAlbertCredentialNeedsManualReview(string(data))
		if plan.CredentialNeedsReview {
			plan.ReviewItems = append(plan.ReviewItems, ".env contains an unsupported or repeated ALBERT_API_KEY assignment; resolve it manually")
		}
		if containsMCPConnector(plan.MCPConnectors, "context7") {
			for _, name := range plan.LegacyEnvKeys {
				if name == "CONTEXT7_API_KEY" {
					plan.ReviewItems = append(plan.ReviewItems, "CONTEXT7_API_KEY remains host-only; the imported Context7 connector uses anonymous access")
					break
				}
			}
		}
		plan.SourceFiles = append(plan.SourceFiles, ".env (host-only; not transferred)")
	}
	if info, err := os.Lstat(filepath.Join(pc.Root, ".agent-vm.runtime.sh")); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			plan.Warnings = append(plan.Warnings, ".agent-vm.runtime.sh is not a regular file; it was not inspected")
		} else {
			plan.RuntimeScriptPresent = true
			plan.SourceFiles = append(plan.SourceFiles, ".agent-vm.runtime.sh (presence only; never read or executed)")
		}
	} else if !os.IsNotExist(err) {
		return plan, err
	}
	plan.ReviewItems = uniqueSortedImportStrings(plan.ReviewItems)
	plan.Warnings = uniqueSortedImportStrings(plan.Warnings)
	return plan, nil
}

// ResolveSkills maps legacy basenames only to official catalogue entries.
// Unknown names remain visible as review items and are never guessed.
func (p *AlbertCodeImport) ResolveSkills(catalogue []ProjectSkill) {
	byName := make(map[string]string, len(catalogue))
	for _, skill := range catalogue {
		if strings.HasPrefix(skill.ID, "official/") {
			byName[skill.Name] = skill.ID
		}
	}
	for _, name := range p.SkillNames {
		if id, ok := byName[name]; ok {
			p.Skills = append(p.Skills, id)
		} else {
			p.ReviewItems = append(p.ReviewItems, fmt.Sprintf("legacy skill %q is absent from the current official catalogue", name))
		}
	}
	sort.Strings(p.Skills)
	sort.Strings(p.ReviewItems)
}

func (p *AlbertCodeImport) importOpenCodeConfig(config map[string]any) {
	providerOK := false
	modelKnown := false
	if providers, ok := config["provider"].(map[string]any); ok {
		if albert, ok := providers["albert"].(map[string]any); ok {
			options, optionsOK := albert["options"].(map[string]any)
			if optionsOK && len(options) == 2 && options["baseURL"] == albertAPIEndpoint && options["apiKey"] == "{env:ALBERT_API_KEY}" && albert["npm"] == "@ai-sdk/openai-compatible" && albert["name"] != nil {
				providerOK = true
			}
			knownProviderKeys := map[string]bool{"npm": true, "name": true, "options": true, "models": true}
			for key := range albert {
				if !knownProviderKeys[key] {
					p.ReviewItems = append(p.ReviewItems, "provider.albert contains custom settings; values withheld")
				}
			}
			if models, ok := albert["models"].(map[string]any); ok {
				if model, ok := config["model"].(string); ok && importedModelPattern.MatchString(model) {
					_, modelKnown = models[strings.TrimPrefix(model, "albert/")]
				}
			}
			if albert["npm"] != "@ai-sdk/openai-compatible" || albert["name"] == nil || !providerOK {
				p.ReviewItems = append(p.ReviewItems, "provider.albert contains custom settings; values withheld")
			}
		}
		for name := range providers {
			if name != "albert" {
				p.ReviewItems = append(p.ReviewItems, fmt.Sprintf("provider.%q is custom or unsupported; values withheld", name))
			}
		}
	}
	if model, ok := config["model"].(string); ok && model != "" {
		if providerOK && modelKnown && importedModelPattern.MatchString(model) {
			p.Model = model
		} else {
			p.ReviewItems = append(p.ReviewItems, "model/provider selection is custom or unsupported; values withheld")
		}
	}
	if !providerOK && config["provider"] != nil {
		p.ReviewItems = append(p.ReviewItems, "provider configuration is custom or unsupported; values withheld")
	}

	if raw, ok := config["mcp"].(map[string]any); ok {
		for name, value := range raw {
			entry, ok := value.(map[string]any)
			if !ok {
				p.ReviewItems = append(p.ReviewItems, fmt.Sprintf("mcp.%q has an unsupported shape; values withheld", name))
				continue
			}
			enabled, _ := entry["enabled"].(bool)
			if !enabled {
				if !legacyMCPMatches(name, entry) {
					p.ReviewItems = append(p.ReviewItems, fmt.Sprintf("mcp.%q has custom/unsupported settings and will remain in the source config", name))
				}
				continue
			}
			if legacyMCPMatches(name, entry) {
				p.MCPConnectors = append(p.MCPConnectors, name)
			} else {
				p.ReviewItems = append(p.ReviewItems, fmt.Sprintf("mcp.%q is custom or unsupported; values withheld and not imported", name))
			}
		}
	}
	for _, key := range []string{"small_model", "permission"} {
		if _, ok := config[key]; ok {
			p.ReviewItems = append(p.ReviewItems, fmt.Sprintf("OpenCode setting %q is preserved but not imported", key))
		}
	}
	for key := range config {
		switch key {
		case "$schema", "model", "small_model", "provider", "mcp", "permission":
		default:
			p.ReviewItems = append(p.ReviewItems, fmt.Sprintf("OpenCode setting %q is preserved but not imported", key))
		}
	}
	sort.Strings(p.MCPConnectors)
}

func uniqueSortedImportStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	sort.Strings(values)
	unique := values[:1]
	for _, value := range values[1:] {
		if value != unique[len(unique)-1] {
			unique = append(unique, value)
		}
	}
	return unique
}

func legacyMCPMatches(name string, entry map[string]any) bool {
	allowedKeys := map[string]map[string]bool{
		"data-gouv":       {"type": true, "url": true, "enabled": true},
		"context7":        {"type": true, "url": true, "enabled": true, "headers": true},
		"playwright":      {"type": true, "command": true, "enabled": true},
		"chrome-devtools": {"type": true, "command": true, "enabled": true},
	}
	allowed, ok := allowedKeys[name]
	if !ok {
		return false
	}
	for key := range entry {
		if !allowed[key] {
			return false
		}
	}
	switch name {
	case "data-gouv":
		return entry["type"] == "remote" && entry["url"] == "https://mcp.data.gouv.fr/mcp"
	case "context7":
		if entry["type"] != "remote" || entry["url"] != "https://mcp.context7.com/mcp" {
			return false
		}
		if raw, ok := entry["headers"]; ok {
			headers, ok := raw.(map[string]any)
			return ok && len(headers) == 1 && headers["Authorization"] == "Bearer {env:CONTEXT7_API_KEY}"
		}
		return true
	case "playwright":
		return entry["type"] == "local" && commandMatches(entry["command"], "npx", "-y", "@playwright/mcp@latest")
	case "chrome-devtools":
		return entry["type"] == "local" && commandMatches(entry["command"], "npx", "-y", "chrome-devtools-mcp@latest", "--headless=true", "--isolated=true")
	default:
		return false
	}
}

func commandMatches(raw any, expected ...string) bool {
	command, ok := raw.([]any)
	if !ok || len(command) != len(expected) {
		return false
	}
	for i, item := range command {
		if item != expected[i] {
			return false
		}
	}
	return true
}

func (p AlbertCodeImport) Format() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Albert Code import preview for %s\n", p.Root)
	if len(p.SourceFiles) == 0 {
		b.WriteString("No supported Albert Code project files were detected.\n")
		for _, warning := range p.Warnings {
			fmt.Fprintf(&b, "Warning: %s\n", warning)
		}
		for _, item := range p.ReviewItems {
			fmt.Fprintf(&b, "Review: %s\n", item)
		}
		return b.String()
	}
	fmt.Fprintf(&b, "Sources detected: %s\n", strings.Join(p.SourceFiles, ", "))
	fmt.Fprintf(&b, "Target: Microsandbox, isolation full (sealed guest clone)\n")
	if p.Model != "" {
		fmt.Fprintf(&b, "Model: %s\n", p.Model)
	}
	fmt.Fprintf(&b, "Skills: %s\n", displayIDs(p.Skills))
	fmt.Fprintf(&b, "MCPs: %s\n", displayIDs(p.MCPConnectors))
	if p.SkillCatalogueUnavailable {
		fmt.Fprintf(&b, "Legacy skills not resolved (catalogue unavailable): %s\n", strings.Join(p.SkillNames, ", "))
	} else if p.HasSkillsFile && len(p.SkillNames) > 0 {
		b.WriteString("Legacy skill names resolve to the current pinned official catalogue; old guest installations are not copied.\n")
	}
	if p.ManagedAgentsPresent {
		b.WriteString("AGENTS.md: recognized Albert Code managed zone; the source file will be preserved.\n")
	}
	if len(p.Skills) > 0 {
		b.WriteString("AGENTS.md will receive only the just-code managed skills section; existing text and the Albert Code managed zone are preserved.\n")
	}
	if p.CredentialFile != "" {
		fmt.Fprintf(&b, "Host hygiene: %s remains on the host and is not transferred; review it for credentials and clean it up only as a separate action after verifying the native store.\n", p.CredentialFile)
		if len(p.LegacyEnvKeys) > 0 {
			fmt.Fprintf(&b, ".env variable names (values withheld; none are imported implicitly): %q\n", p.LegacyEnvKeys)
		}
	}
	if p.CredentialAvailable {
		b.WriteString("A literal Albert credential is present in .env; secret bytes are withheld. Use --import-albert-key to store it in the native credential store.\n")
	}
	for _, warning := range p.Warnings {
		fmt.Fprintf(&b, "Warning: %s\n", warning)
	}
	for _, item := range p.ReviewItems {
		fmt.Fprintf(&b, "Review: %s\n", item)
	}
	b.WriteString("The old VM, sessions, untracked guest files, and installed tools do not transfer. The guest receives a clone filtered by the default-deny transfer rules; .env is not copied. Legacy config files and .git/info/exclude are not changed.\n")
	b.WriteString("Preview only. Pass --apply to write the just-code project manifest and lockfile.\n")
	return b.String()
}

func displayIDs(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}

func readImportFile(path string) ([]byte, error) {
	file, info, err := openRegularImportFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info.Size() > maxImportFileSize {
		return nil, fmt.Errorf("%s exceeds the importer size limit", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxImportFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > maxImportFileSize {
		return nil, fmt.Errorf("%s exceeds the importer size limit", path)
	}
	return data, nil
}

// ReadAlbertCodeImportFile reads a bounded regular file for the import command.
func ReadAlbertCodeImportFile(path string) ([]byte, error) {
	return readImportFile(path)
}

func readImportFileIfPresent(path string) ([]byte, error) {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return readImportFile(path)
}

func regularImportFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", errUnsafeImportFile, path)
	}
	return info, nil
}

func openRegularImportFile(path string) (*os.File, os.FileInfo, error) {
	pathInfo, err := regularImportFile(path)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", path, err)
	}
	openedInfo, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, fmt.Errorf("inspect opened file %s: %w", path, err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		file.Close()
		return nil, nil, fmt.Errorf("%w: %s changed while opening", errUnsafeImportFile, path)
	}
	return file, openedInfo, nil
}

func ensureImportDirectory(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s is not a regular directory; refusing import", path)
	}
	return nil
}
