package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/etalab-ia/just-code/internal/justcode"
)

// modelsCmd implements `just-code models` (P10): the validated Albert model
// catalogue. A network failure falls back to the last-known-good cache (a
// stale list beats no list); the model selection is validated against
// whichever list won, and stale selections are called out rather than
// silently kept.
func modelsCmd(args []string) (int, error) {
	if len(args) == 0 {
		// Default view: list the usable models and validate the current
		// selection against the catalogue.
		return modelsListCmd()
	}
	switch args[0] {
	case "list":
		return modelsListCmd()
	default:
		return 2, fmt.Errorf("Unknown models command: %s (expected list)", args[0])
	}
}

func modelsListCmd() (int, error) {
	ctx := context.Background()
	apiKey, err := resolveAlbertKeyForCatalogue()
	if err != nil {
		return 1, err
	}
	cache := justcode.CatalogueCache{
		FS:  justcode.DefaultFS,
		Dir: justcode.DefaultStateDir(),
		Fetch: func(ctx context.Context) (justcode.Catalogue, error) {
			return justcode.FetchCatalogue(ctx, nil, apiKey, justcode.AlbertCatalogueURL())
		},
	}
	catalogue, fresh, err := cache.Load(ctx)
	if err != nil {
		return 1, fmt.Errorf("cannot load the model catalogue and no last-known-good cache exists: %w", err)
	}
	if !fresh {
		fmt.Fprintln(os.Stderr, "Warning: the catalogue could not be fetched; showing the last-known-good list.")
	}
	selection := resolveModelSelection(".")
	models := catalogue.TextGenerationModels()
	if len(models) == 0 {
		fmt.Println("No text-generation models in the catalogue.")
		return 0, nil
	}
	fmt.Printf("Albert text-generation models (%s):\n", sourceLabel(fresh))
	for _, m := range models {
		marker := "  "
		if selection == "albert/"+m.ID || matchesAlias(selection, m.Alias) {
			marker = "->"
		}
		fmt.Printf("%s %-36s context: %d\n", marker, m.ID, m.MaxContextLength)
	}
	if selection != "" {
		if _, ok := catalogue.Find(selection); !ok {
			fmt.Fprintf(os.Stderr, "Warning: the selected model %q is not in this catalogue; it may have been removed. Pick a listed model (JUST_CODE_MODEL, project manifest or user settings).\n", selection)
		}
	}
	return 0, nil
}

func matchesAlias(selection, alias string) bool {
	if alias == "" {
		return false
	}
	id := strings.TrimPrefix(selection, "albert/")
	for _, a := range strings.Split(alias, ",") {
		if a == id {
			return true
		}
	}
	return false
}

func sourceLabel(fresh bool) string {
	if fresh {
		return "live"
	}
	return "last-known-good"
}

// resolveAlbertKeyForCatalogue obtains a key for the catalogue request via
// the normal resolution chain (credentialRef > legacy env > store), without
// exposing it.
func resolveAlbertKeyForCatalogue() (string, error) {
	return resolveAlbertKeyForCatalogueAt(resolveProjectRootForAux())
}

// resolveAlbertKeyForCatalogueAt resolves the key for a specific project root,
// so a command configuring one directory does not read another's
// credentialRef (the reference lives in the project manifest).
func resolveAlbertKeyForCatalogueAt(projectRoot string) (string, error) {
	cfg := justcode.Config{
		CredentialRef: resolveCredentialRef(projectRoot),
		APIKey:        os.Getenv("ALBERT_API_KEY"),
	}
	v, _, _, _, err := justcode.ResolveAlbert(ctxForCatalogue(), cfg, "")
	if err != nil {
		return "", fmt.Errorf("an Albert credential is needed to fetch the model catalogue: %w", err)
	}
	return v, nil
}

func ctxForCatalogue() context.Context { return context.Background() }

// resolveProjectRootForAux discovers the project root for commands that run
// before the main flow's discovery. Best-effort: "." when discovery fails.
func resolveProjectRootForAux() string {
	if pc, err := justcode.DiscoverProject("."); err == nil {
		return pc.Root
	}
	return "."
}
