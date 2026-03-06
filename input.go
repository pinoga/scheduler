package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
)

type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return strings.Join(e.Problems, "; ")
}

func (e *ValidationError) add(msg string) {
	e.Problems = append(e.Problems, msg)
}

func (e *ValidationError) hasErrors() bool {
	return len(e.Problems) > 0
}

func LoadInput(filePath string) (Input, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return Input{}, fmt.Errorf("reading input file: %w", err)
	}

	var input Input
	if err := json.Unmarshal(data, &input); err != nil {
		return Input{}, fmt.Errorf("parsing input JSON: %w", err)
	}

	// Default times_per_day to 1 if omitted (zero value).
	for i := range input.ConsumptionPlans {
		if input.ConsumptionPlans[i].TimesPerDay <= 0 {
			input.ConsumptionPlans[i].TimesPerDay = 1
		}
	}

	if err := ValidateInput(input); err != nil {
		return Input{}, err
	}

	return input, nil
}

func ValidateInput(input Input) error {
	ve := &ValidationError{}

	// Build lookup maps.
	itemByID := make(map[string]Item, len(input.Items))
	for _, item := range input.Items {
		if _, dup := itemByID[item.ID]; dup {
			ve.add(fmt.Sprintf("duplicate item id %q", item.ID))
		}
		itemByID[item.ID] = item
	}

	productByID := make(map[string]Product, len(input.Products))
	for _, prod := range input.Products {
		if _, dup := productByID[prod.ID]; dup {
			ve.add(fmt.Sprintf("duplicate product id %q", prod.ID))
		}
		productByID[prod.ID] = prod
	}

	// Validate items.
	for _, item := range input.Items {
		if item.ID == "" {
			ve.add("item has empty id")
		}
		if item.DosagePerUnit <= 0 {
			ve.add(fmt.Sprintf("item %q: dosage_per_unit must be > 0", item.ID))
		}
		if item.MaxStockDays <= 0 {
			ve.add(fmt.Sprintf("item %q: max_stock_days must be > 0", item.ID))
		}
		switch item.Unit {
		case UnitMg, UnitMcg, UnitG, UnitMl:
		default:
			ve.add(fmt.Sprintf("item %q: unknown unit %q (expected mg, mcg, g, or ml)", item.ID, item.Unit))
		}
	}

	// Validate products.
	for _, prod := range input.Products {
		if prod.ID == "" {
			ve.add("product has empty id")
		}
		if _, ok := itemByID[prod.ItemID]; !ok {
			ve.add(fmt.Sprintf("product %q: references unknown item %q", prod.ID, prod.ItemID))
		}
		if prod.CapsulesPerBox <= 0 {
			ve.add(fmt.Sprintf("product %q: capsules_per_box must be > 0", prod.ID))
		}
	}

	// Validate suppliers.
	supplierIDs := make(map[string]bool, len(input.Suppliers))
	for _, sup := range input.Suppliers {
		if sup.ID == "" {
			ve.add("supplier has empty id")
		}
		if _, dup := supplierIDs[sup.ID]; dup {
			ve.add(fmt.Sprintf("duplicate supplier id %q", sup.ID))
		}
		supplierIDs[sup.ID] = true

		for _, ce := range sup.Catalog {
			if _, ok := productByID[ce.ProductID]; !ok {
				ve.add(fmt.Sprintf("supplier %q: catalog references unknown product %q", sup.ID, ce.ProductID))
			}
			if ce.Price <= 0 {
				ve.add(fmt.Sprintf("supplier %q: product %q price must be > 0", sup.ID, ce.ProductID))
			}
			if ce.DeliveryDays < 0 {
				ve.add(fmt.Sprintf("supplier %q: product %q delivery_days must be >= 0", sup.ID, ce.ProductID))
			}
			if ce.Currency == "" {
				ve.add(fmt.Sprintf("supplier %q: product %q has empty currency", sup.ID, ce.ProductID))
			}
		}
	}

	// Validate consumption plans.
	for _, cp := range input.ConsumptionPlans {
		item, ok := itemByID[cp.ItemID]
		if !ok {
			ve.add(fmt.Sprintf("consumption_plan: references unknown item %q", cp.ItemID))
			continue
		}
		if cp.Dosage <= 0 {
			ve.add(fmt.Sprintf("consumption_plan for %q: dosage must be > 0", cp.ItemID))
			continue
		}

		// Check capsules_per_dose is an integer.
		ratio := cp.Dosage / item.DosagePerUnit
		rounded := math.Round(ratio)
		if math.Abs(ratio-rounded) > 1e-9 || rounded < 1 {
			ve.add(fmt.Sprintf("consumption_plan for %q: dosage %.4g / dosage_per_unit %.4g = %.4g (must be a positive integer)",
				cp.ItemID, cp.Dosage, item.DosagePerUnit, ratio))
		}

		if cp.TimesPerDay <= 0 {
			ve.add(fmt.Sprintf("consumption_plan for %q: times_per_day must be > 0", cp.ItemID))
		}

		// Validate cron expression.
		if _, err := ParseCron(cp.Frequency); err != nil {
			ve.add(fmt.Sprintf("consumption_plan for %q: invalid frequency %q: %v", cp.ItemID, cp.Frequency, err))
		}
	}

	// Validate current_stock.
	for _, se := range input.CurrentStock {
		if _, ok := itemByID[se.ItemID]; !ok {
			ve.add(fmt.Sprintf("current_stock: references unknown item %q", se.ItemID))
		}
		if se.Capsules < 0 {
			ve.add(fmt.Sprintf("current_stock for %q: capsules must be >= 0", se.ItemID))
		}
	}

	if input.HeadroomDays < 0 {
		ve.add("headroom_days must be >= 0")
	}

	if ve.hasErrors() {
		return ve
	}
	return nil
}
