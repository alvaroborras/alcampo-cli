package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/alvaroborras/alcampo-cli/internal/client"
	"github.com/alvaroborras/alcampo-cli/internal/config"
)

// ---- spending guard ----

// resolveMax returns the spending cap in € from (in order) the --max flag, the
// ALCAMPO_MAX_EUR env var, or [limits].max_eur in config.toml. 0 = no limit.
func resolveMax(flagMax float64) (float64, error) {
	if flagMax > 0 {
		return flagMax, nil
	}
	if s := os.Getenv("ALCAMPO_MAX_EUR"); s != "" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid ALCAMPO_MAX_EUR %q: %v", s, err)
		}
		return v, nil
	}
	cfg, _ := config.LoadConfig()
	return cfg.Limits.MaxEUR, nil
}

// checkBudget fails when a known total exceeds the cap. With failClosed an
// unreadable total is also a failure — used before the irreversible submit, where
// "can't verify the total" must mean "don't spend". maxEUR <= 0 disables the guard.
func checkBudget(total float64, haveTotal bool, maxEUR float64, action string, failClosed bool) error {
	if maxEUR <= 0 {
		return nil
	}
	if !haveTotal {
		if failClosed {
			return fmt.Errorf("refusing to %s: couldn't read the total to enforce the %.2f€ limit (set ALCAMPO_MAX_EUR=0 to disable the guard)", action, maxEUR)
		}
		return nil
	}
	if total > maxEUR {
		return fmt.Errorf("BUDGET EXCEEDED: %s total %.2f€ is over the %.2f€ limit — refusing (raise with --max, ALCAMPO_MAX_EUR, or [limits].max_eur)", action, total, maxEUR)
	}
	return nil
}

// budgetCheckRaw enforces the configured cap against a total parsed from a cart or
// checkout response (used by the non-irreversible steps; failClosed=false).
func budgetCheckRaw(raw json.RawMessage, flagMax float64, action string) error {
	maxEUR, err := resolveMax(flagMax)
	if err != nil {
		return err
	}
	total, ok := client.ExtractTotal(raw)
	return checkBudget(total, ok, maxEUR, action, false)
}
