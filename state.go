package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"time"
)

// LoadState loads the state file. Returns nil, nil if the file does not exist.
func LoadState(filePath string) (*State, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading state file: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing state file: %w", err)
	}
	return &state, nil
}

// SaveState writes the state to disk atomically (write tmp + rename).
func SaveState(filePath string, state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	tmpPath := filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("writing temp state file: %w", err)
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		return fmt.Errorf("renaming state file: %w", err)
	}
	return nil
}

// ComputeInputHash returns the SHA-256 hex digest of the input file.
func ComputeInputHash(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("reading input for hash: %w", err)
	}
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h), nil
}

// ComputeEffectiveStock determines current stock levels.
// Priority:
//  1. If inputStock is non-empty, use it (user override).
//  2. If state exists, auto-decrement from last known stock.
//  3. Otherwise, return empty map (all items at 0).
func ComputeEffectiveStock(state *State, itemPlans []ItemPlan, inputStock []StockEntry, today time.Time) map[string]int {
	stock := make(map[string]int)

	// Case 1: user provided current_stock.
	if len(inputStock) > 0 {
		for _, se := range inputStock {
			stock[se.ItemID] = se.Units
		}
		return stock
	}

	// Case 2: no state file.
	if state == nil {
		return stock
	}

	// Case 3: auto-decrement from state.
	elapsedDays := daysBetween(state.LastRunDate, today)
	if elapsedDays < 0 {
		elapsedDays = 0
	}

	// Build consumption rate lookup.
	rateByItem := make(map[string]float64, len(itemPlans))
	for _, ip := range itemPlans {
		rateByItem[ip.Item.ID] = ip.ConsumptionRate
	}

	for _, se := range state.StockAtLastRun {
		rate := rateByItem[se.ItemID]
		consumed := int(math.Ceil(rate * float64(elapsedDays)))
		remaining := se.Units - consumed
		if remaining < 0 {
			remaining = 0
		}
		stock[se.ItemID] = remaining
	}

	return stock
}

func daysBetween(a, b time.Time) int {
	duration := b.Sub(a)
	return int(duration.Hours() / 24)
}
