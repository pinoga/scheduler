package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func main() {
	inputPath := flag.String("input", "", "Path to input JSON file (required)")
	statePath := flag.String("state", "", "Path to state file (default: .scheduler_state.json next to input)")
	outputPath := flag.String("output", "", "Path to output JSON file (default: stdout)")
	todayStr := flag.String("today", "", "Override today's date (YYYY-MM-DD, for testing)")
	flag.Parse()

	if *inputPath == "" {
		fmt.Fprintf(os.Stderr, "Usage: scheduler -input <path> [-state <path>] [-output <path>] [-today YYYY-MM-DD]\n")
		os.Exit(1)
	}

	today := time.Now().Truncate(24 * time.Hour)
	if *todayStr != "" {
		parsed, err := time.Parse("2006-01-02", *todayStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid -today value %q: %v\n", *todayStr, err)
			os.Exit(1)
		}
		today = parsed
	}

	if *statePath == "" {
		*statePath = filepath.Join(filepath.Dir(*inputPath), ".scheduler_state.json")
	}

	// Load and validate input.
	input, err := LoadInput(*inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Load existing state.
	state, err := LoadState(*statePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load state: %v\n", err)
	}

	// First pass: build groups with zero stock to get consumption rates for stock decay.
	groupsForRates, _ := BuildItemPlanGroups(input, nil)
	plansForRates := flattenGroups(groupsForRates)

	// Compute effective stock (auto-decrement from state or use input).
	effectiveStock := ComputeEffectiveStock(state, plansForRates, input.CurrentStock, today)

	// Second pass: build groups with actual stock.
	groups, planErrors := BuildItemPlanGroups(input, effectiveStock)

	// Build all schedule alternatives.
	schedules := BuildAllSchedules(input, groups, today)

	// Attach plan-level errors to each schedule.
	for i := range schedules {
		schedules[i].Errors = append(schedules[i].Errors, planErrors...)
	}

	// Format and write output.
	output := FormatOutput(schedules, *inputPath, today)

	if *outputPath != "" {
		f, err := os.Create(*outputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: creating output file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close() //nolint:errcheck // best-effort close on CLI exit
		if err := WriteOutput(output, f); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		if err := PrintOutput(output); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	// Print human-readable summary to stderr.
	PrintSummaryToStderr(output)

	// Save state.
	stockEntries := make([]StockEntry, 0, len(effectiveStock))
	for itemID, caps := range effectiveStock {
		stockEntries = append(stockEntries, StockEntry{ItemID: itemID, Units: caps})
	}
	inputHash, _ := ComputeInputHash(*inputPath)

	if err := SaveState(*statePath, State{
		LastRunDate:    today,
		StockAtLastRun: stockEntries,
		InputHash:      inputHash,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save state: %v\n", err)
	}
}

// flattenGroups extracts all candidate ItemPlans for stock decay computation.
func flattenGroups(groups []ItemPlanGroup) []ItemPlan {
	var plans []ItemPlan
	for _, g := range groups {
		plans = append(plans, g.Candidates...)
	}
	return plans
}
