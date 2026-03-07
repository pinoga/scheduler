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

	// Apply defaults.
	for i := range input.ConsumptionPlans {
		cp := &input.ConsumptionPlans[i]
		// Item mode defaults.
		if cp.ItemID != "" && cp.Units <= 0 {
			cp.Units = 1.0
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

	// Build substance -> items index.
	itemsBySubstance := make(map[string][]Item)
	for _, item := range input.Items {
		if item.Substance != "" {
			itemsBySubstance[item.Substance] = append(itemsBySubstance[item.Substance], item)
		}
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
		case UnitMg, UnitMcg, UnitG, UnitMl, UnitCapsules:
		default:
			ve.add(fmt.Sprintf("item %q: unknown unit %q (expected mg, mcg, g, ml, or capsules)", item.ID, item.Unit))
		}
	}

	// Validate that items sharing a substance have the same unit.
	for substance, items := range itemsBySubstance {
		unit := items[0].Unit
		for _, item := range items[1:] {
			if item.Unit != unit {
				ve.add(fmt.Sprintf("substance %q: item %q has unit %q but item %q has unit %q",
					substance, items[0].ID, unit, item.ID, item.Unit))
			}
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
	for i, cp := range input.ConsumptionPlans {
		label := fmt.Sprintf("consumption_plan[%d]", i)

		// Mode validation: exactly one of substance or item_id must be set.
		if cp.Substance != "" && cp.ItemID != "" {
			ve.add(fmt.Sprintf("%s: cannot set both substance %q and item_id %q", label, cp.Substance, cp.ItemID))
			continue
		}
		if cp.Substance == "" && cp.ItemID == "" {
			ve.add(fmt.Sprintf("%s: must set either substance or item_id", label))
			continue
		}

		if _, err := ParseCron(cp.Frequency); err != nil {
			ve.add(fmt.Sprintf("%s: invalid frequency %q: %v", label, cp.Frequency, err))
		}

		if cp.Substance != "" {
			// Substance mode validation.
			label = fmt.Sprintf("consumption_plan for substance %q", cp.Substance)
			if cp.Dosage <= 0 {
				ve.add(fmt.Sprintf("%s: dosage must be > 0", label))
				continue
			}
			items := itemsBySubstance[cp.Substance]
			if len(items) == 0 {
				ve.add(fmt.Sprintf("%s: no items have this substance", label))
				continue
			}
			// Check that at least one item can divide the dosage evenly.
			anyValid := false
			for _, item := range items {
				ratio := cp.Dosage / item.DosagePerUnit
				rounded := math.Round(ratio)
				if math.Abs(ratio-rounded) <= 1e-9 && rounded >= 1 {
					anyValid = true
					break
				}
			}
			if !anyValid {
				ve.add(fmt.Sprintf("%s: no item's dosage_per_unit divides dosage %.4g evenly", label, cp.Dosage))
			}
		} else {
			// Item mode validation.
			label = fmt.Sprintf("consumption_plan for item %q", cp.ItemID)
			if _, ok := itemByID[cp.ItemID]; !ok {
				ve.add(fmt.Sprintf("%s: references unknown item", label))
				continue
			}
			if cp.Units <= 0 {
				ve.add(fmt.Sprintf("%s: units must be > 0", label))
			}
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
