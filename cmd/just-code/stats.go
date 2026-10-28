package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/etalab-ia/just-code/internal/justcode"
)

// statsCmd implements the consent and inspection surface of issue #181:
//
//	stats on    # explicit opt-in
//	stats off   # stop sending immediately and drop the queue
//	stats show  # exactly what is (or would be) sent
//
// `stats` alone is `stats show`.
func statsCmd(args []string) (int, error) {
	sub := "show"
	if len(args) > 0 {
		sub = args[0]
		if len(args) > 1 {
			return 2, fmt.Errorf("unknown stats argument: %s (expected on, off or show)", args[1])
		}
	}
	path, err := justcode.UserSettingsPath()
	if err != nil {
		return 1, err
	}
	fs := justcode.DefaultFS
	stateDir := justcode.DefaultStateDir()
	switch sub {
	case "on":
		if err := justcode.WriteStatsConsent(fs, path, true, true); err != nil {
			return 1, err
		}
		fmt.Println("Statistiques d'usage : partagées.")
		if justcode.StatsEndpoint() == "" {
			fmt.Println("Aucun endpoint n'est configuré dans cette version : les événements restent locaux et ne sont pas envoyés.")
		}
		justcode.StatsFlushSpawn()
		return 0, nil
	case "off":
		if err := justcode.WriteStatsConsent(fs, path, false, true); err != nil {
			return 1, err
		}
		if err := justcode.ClearStatsQueue(fs, stateDir); err != nil {
			return 1, err
		}
		// Invalidate a pending setup decision too: resuming an interrupted
		// wizard must not resurrect a consent the user just withdrew.
		if j, err := justcode.ReadSetupJournal(fs, stateDir); err == nil && j.StatsEnabled {
			j.StatsEnabled = false
			_ = justcode.WriteSetupJournal(fs, stateDir, j)
		}
		fmt.Println("Statistiques d'usage : non partagées. Aucun envoi ne sera effectué.")
		return 0, nil
	case "show":
		return statsShowCmd(fs, stateDir, path)
	default:
		return 2, fmt.Errorf("Unknown stats command: %s (expected on, off or show)", sub)
	}
}

func statsShowCmd(fs justcode.FS, stateDir, settingsPath string) (int, error) {
	enabled, asked := justcode.ReadStatsConsent(fs, settingsPath)
	fmt.Println("Statistiques d'usage just-code")
	fmt.Println()
	switch {
	case !asked:
		fmt.Println("  Consentement        non demandé (désactivées)")
	case enabled:
		fmt.Println("  Consentement        accepté")
	default:
		fmt.Println("  Consentement        refusé")
	}
	endpoint := justcode.StatsEndpoint()
	if endpoint == "" {
		fmt.Println("  Envoi               désactivé (aucun endpoint configuré)")
	} else if enabled {
		fmt.Printf("  Envoi               activé vers %s\n", endpoint)
	} else {
		fmt.Println("  Envoi               désactivé")
	}
	if id := justcode.ReadStatsMachineID(fs, stateDir); id != "" {
		fmt.Printf("  Identifiant poste   %s\n", id)
	} else {
		fmt.Println("  Identifiant poste   pas encore généré")
	}
	if root := resolveProjectRootForAux(); root != "" {
		if id := justcode.ReadStatsProjectID(fs, stateDir, root); id != "" {
			fmt.Printf("  Identifiant projet  %s\n", id)
		}
	}

	events, err := justcode.PendingStatsEvents(fs, stateDir)
	if err != nil {
		return 1, err
	}
	fmt.Println()
	if len(events) == 0 {
		fmt.Println("Aucun envoi en attente.")
		return 0, nil
	}
	fmt.Printf("File d'envoi : %d événement(s)\n", len(events))
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			return 1, err
		}
		fmt.Println(string(data))
	}
	if !enabled {
		fmt.Fprintln(os.Stderr, "Les événements en file ne seront pas envoyés : le consentement est refusé.")
	}
	return 0, nil
}
