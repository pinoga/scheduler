package main

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// BuildItemPlanGroups resolves consumption plans into groups of candidate ItemPlans.
// Substance mode may produce multiple candidates (one per matching item).
// Item mode always produces exactly one candidate.
func BuildItemPlanGroups(input Input, effectiveStock map[string]int) ([]ItemPlanGroup, []ScheduleError) {
	itemByID := make(map[string]Item, len(input.Items))
	for _, item := range input.Items {
		itemByID[item.ID] = item
	}

	// Build substance -> items index.
	itemsBySubstance := make(map[string][]Item)
	for _, item := range input.Items {
		if item.Substance != "" {
			itemsBySubstance[item.Substance] = append(itemsBySubstance[item.Substance], item)
		}
	}

	var groups []ItemPlanGroup
	var errs []ScheduleError

	for _, cp := range input.ConsumptionPlans {
		pc, err := ParseCron(cp.Frequency)
		if err != nil {
			label := cp.ItemID
			if label == "" {
				label = cp.Substance
			}
			errs = append(errs, ScheduleError{ItemID: label, Message: fmt.Sprintf("invalid frequency: %v", err)})
			continue
		}
		activeFrac := ActiveDayFraction(pc)
		dosesPerDay := float64(cp.TimesPerDay) * activeFrac

		if cp.Substance != "" {
			// Substance mode: find all items matching the substance with valid integer division.
			items := itemsBySubstance[cp.Substance]
			var candidates []ItemPlan
			for _, item := range items {
				ratio := cp.Dosage / item.DosagePerUnit
				capsPerDose := int(math.Round(ratio))
				if capsPerDose < 1 || math.Abs(ratio-float64(capsPerDose)) > 1e-9 {
					continue // this item can't divide the dosage evenly
				}

				consumptionRate := float64(capsPerDose) * dosesPerDay
				currentCaps := 0
				if effectiveStock != nil {
					currentCaps = effectiveStock[item.ID]
				}

				candidates = append(candidates, ItemPlan{
					Item:            item,
					Plan:            cp,
					CapsulesPerDose: capsPerDose,
					Fraction:        1.0,
					DosesPerDay:     dosesPerDay,
					ConsumptionRate: consumptionRate,
					CurrentCapsules: currentCaps,
				})
			}

			if len(candidates) == 0 {
				errs = append(errs, ScheduleError{
					ItemID:  cp.Substance,
					Message: "no item's dosage_per_unit divides dosage evenly",
				})
				continue
			}

			groups = append(groups, ItemPlanGroup{
				Candidates: candidates,
				Label:      cp.Substance,
			})
		} else {
			// Item mode: resolve directly.
			item, ok := itemByID[cp.ItemID]
			if !ok {
				errs = append(errs, ScheduleError{ItemID: cp.ItemID, Message: "item not found"})
				continue
			}

			consumptionRate := float64(cp.CapsulesPerDose) * cp.Fraction * dosesPerDay
			currentCaps := 0
			if effectiveStock != nil {
				currentCaps = effectiveStock[item.ID]
			}

			groups = append(groups, ItemPlanGroup{
				Candidates: []ItemPlan{{
					Item:            item,
					Plan:            cp,
					CapsulesPerDose: cp.CapsulesPerDose,
					Fraction:        cp.Fraction,
					DosesPerDay:     dosesPerDay,
					ConsumptionRate: consumptionRate,
					CurrentCapsules: currentCaps,
				}},
				Label: cp.ItemID,
			})
		}
	}

	return groups, errs
}

// computeProductMetrics calculates supply metrics for a product given an item's consumption rate.
func computeProductMetrics(itemPlan ItemPlan, product Product, ce CatalogEntry, supplier Supplier) ProductSupplierChoice {
	daysPerBox := float64(product.CapsulesPerBox) / itemPlan.ConsumptionRate
	maxBoxes := int(math.Floor(float64(itemPlan.Item.MaxStockDays) * itemPlan.ConsumptionRate / float64(product.CapsulesPerBox)))
	if maxBoxes < 1 {
		maxBoxes = 1
	}
	maxSupplyDays := float64(maxBoxes*product.CapsulesPerBox) / itemPlan.ConsumptionRate
	costPerCapsule := ce.Price / float64(product.CapsulesPerBox)
	costPerDose := costPerCapsule * float64(itemPlan.CapsulesPerDose) * itemPlan.Fraction

	return ProductSupplierChoice{
		Product:       product,
		Supplier:      supplier,
		CatalogEntry:  ce,
		MaxBoxes:      maxBoxes,
		MaxSupplyDays: maxSupplyDays,
		DaysPerBox:    daysPerBox,
		CostPerDose:   costPerDose,
	}
}

// selectForCandidate finds the best product+supplier for a single ItemPlan candidate.
func selectForCandidate(itemPlan ItemPlan, products []Product, offersByProduct map[string][]supplierOffer, strategy SelectionStrategy) *ProductSupplierChoice {
	if itemPlan.ConsumptionRate <= 0 {
		return nil
	}

	switch strategy {
	case StrategyPreferred:
		for _, prod := range products {
			if prod.ItemID != itemPlan.Item.ID {
				continue
			}
			offers := offersByProduct[prod.ID]
			if len(offers) == 0 {
				continue
			}
			var best *ProductSupplierChoice
			for _, o := range offers {
				choice := computeProductMetrics(itemPlan, prod, o.CatalogEntry, o.Supplier)
				if best == nil || choice.CostPerDose < best.CostPerDose {
					best = &choice
				}
			}
			return best
		}

	case StrategyCheapest:
		var best *ProductSupplierChoice
		for _, prod := range products {
			if prod.ItemID != itemPlan.Item.ID {
				continue
			}
			for _, o := range offersByProduct[prod.ID] {
				choice := computeProductMetrics(itemPlan, prod, o.CatalogEntry, o.Supplier)
				if best == nil || choice.CostPerDose < best.CostPerDose {
					best = &choice
				}
			}
		}
		return best

	case StrategyFastest:
		var best *ProductSupplierChoice
		for _, prod := range products {
			if prod.ItemID != itemPlan.Item.ID {
				continue
			}
			for _, o := range offersByProduct[prod.ID] {
				choice := computeProductMetrics(itemPlan, prod, o.CatalogEntry, o.Supplier)
				if best == nil ||
					choice.CatalogEntry.DeliveryDays < best.CatalogEntry.DeliveryDays ||
					(choice.CatalogEntry.DeliveryDays == best.CatalogEntry.DeliveryDays && choice.CostPerDose < best.CostPerDose) {
					best = &choice
				}
			}
		}
		return best
	}

	return nil
}

type supplierOffer struct {
	Supplier     Supplier
	CatalogEntry CatalogEntry
}

type itemAssignment struct {
	ItemPlan ItemPlan
	Choice   ProductSupplierChoice
}

// BuildSchedule constructs a single schedule using the given selection strategy.
func BuildSchedule(scheduleID int, description string, groups []ItemPlanGroup, products []Product, suppliers []Supplier, headroomDays int, today time.Time, strategy SelectionStrategy) Schedule {
	// Build catalog index once.
	offersByProduct := make(map[string][]supplierOffer)
	for _, sup := range suppliers {
		for _, ce := range sup.Catalog {
			offersByProduct[ce.ProductID] = append(offersByProduct[ce.ProductID], supplierOffer{sup, ce})
		}
	}

	// Phase 1: For each group, try all candidates and pick the best.
	var assignments []itemAssignment
	var schedErrors []ScheduleError

	for _, group := range groups {
		var bestAssignment *itemAssignment

		for _, candidate := range group.Candidates {
			choice := selectForCandidate(candidate, products, offersByProduct, strategy)
			if choice == nil {
				continue
			}
			a := itemAssignment{ItemPlan: candidate, Choice: *choice}

			if bestAssignment == nil {
				bestAssignment = &a
			} else {
				// Pick better candidate based on strategy.
				switch strategy {
				case StrategyPreferred:
					// First candidate with any match wins (array order = priority).
					// Already set, skip.
				case StrategyCheapest:
					if a.Choice.CostPerDose < bestAssignment.Choice.CostPerDose {
						bestAssignment = &a
					}
				case StrategyFastest:
					if a.Choice.CatalogEntry.DeliveryDays < bestAssignment.Choice.CatalogEntry.DeliveryDays ||
						(a.Choice.CatalogEntry.DeliveryDays == bestAssignment.Choice.CatalogEntry.DeliveryDays &&
							a.Choice.CostPerDose < bestAssignment.Choice.CostPerDose) {
						bestAssignment = &a
					}
				}
			}
		}

		if bestAssignment == nil {
			schedErrors = append(schedErrors, ScheduleError{
				ItemID:  group.Label,
				Message: "no product+supplier available",
			})
		} else {
			assignments = append(assignments, *bestAssignment)
		}
	}

	// Phase 2: Group assignments by supplier.
	type supplierGroup struct {
		Supplier    Supplier
		Assignments []itemAssignment
	}
	groupMap := make(map[string]*supplierGroup)
	for _, a := range assignments {
		g, ok := groupMap[a.Choice.Supplier.ID]
		if !ok {
			g = &supplierGroup{Supplier: a.Choice.Supplier}
			groupMap[a.Choice.Supplier.ID] = g
		}
		g.Assignments = append(g.Assignments, a)
	}

	// Deterministic order by supplier ID.
	var supGroups []*supplierGroup
	for _, g := range groupMap {
		supGroups = append(supGroups, g)
	}
	sort.Slice(supGroups, func(i, j int) bool {
		return supGroups[i].Supplier.ID < supGroups[j].Supplier.ID
	})

	var initialPurchases []PurchaseEvent
	var recurringCheckouts []PurchaseEvent
	totalMonthlyCost := 0.0
	pricePerDose := make(map[string]float64)
	currency := ""

	// Compute horizon for recurring dates.
	maxStockDays := 0
	for _, group := range groups {
		for _, c := range group.Candidates {
			if c.Item.MaxStockDays > maxStockDays {
				maxStockDays = c.Item.MaxStockDays
			}
		}
	}

	for _, g := range supGroups {
		// Checkout interval = min(maxSupplyDays) across items at this supplier.
		checkoutInterval := math.Inf(1)
		for _, a := range g.Assignments {
			if a.Choice.MaxSupplyDays < checkoutInterval {
				checkoutInterval = a.Choice.MaxSupplyDays
			}
		}
		intervalDays := int(math.Floor(checkoutInterval))
		if intervalDays < 1 {
			intervalDays = 1
		}

		// Boxes per checkout for each item.
		type itemOrder struct {
			Assignment itemAssignment
			Boxes      int
		}
		var orders []itemOrder
		for _, a := range g.Assignments {
			capsNeeded := a.ItemPlan.ConsumptionRate * float64(intervalDays)
			boxes := int(math.Ceil(capsNeeded / float64(a.Choice.Product.CapsulesPerBox)))
			if boxes > a.Choice.MaxBoxes {
				boxes = a.Choice.MaxBoxes
			}
			if boxes < 1 {
				boxes = 1
			}
			orders = append(orders, itemOrder{Assignment: a, Boxes: boxes})
		}

		// Initial purchase date = earliest reorder among this supplier's items.
		earliestReorder := math.Inf(1)
		for _, o := range orders {
			deliveryDays := o.Assignment.Choice.CatalogEntry.DeliveryDays
			daysOfStock := 0.0
			if o.Assignment.ItemPlan.ConsumptionRate > 0 {
				daysOfStock = float64(o.Assignment.ItemPlan.CurrentCapsules) / o.Assignment.ItemPlan.ConsumptionRate
			}
			daysUntilReorder := daysOfStock - float64(headroomDays) - float64(deliveryDays)
			if daysUntilReorder < earliestReorder {
				earliestReorder = daysUntilReorder
			}
		}
		if earliestReorder < 0 {
			earliestReorder = 0
		}
		purchaseDate := today.AddDate(0, 0, int(math.Floor(earliestReorder)))

		// Build initial purchase event.
		if currency == "" && len(orders) > 0 {
			currency = orders[0].Assignment.Choice.CatalogEntry.Currency
		}
		supCurrency := ""
		if len(orders) > 0 {
			supCurrency = orders[0].Assignment.Choice.CatalogEntry.Currency
		}

		var initialProducts []ProductOrder
		for _, o := range orders {
			daysUntilPurchase := math.Floor(earliestReorder)
			capsConsumedWaiting := o.Assignment.ItemPlan.ConsumptionRate * daysUntilPurchase
			remainingAtPurchase := float64(o.Assignment.ItemPlan.CurrentCapsules) - capsConsumedWaiting
			if remainingAtPurchase < 0 {
				remainingAtPurchase = 0
			}

			totalCoverageDays := float64(intervalDays) + float64(o.Assignment.Choice.CatalogEntry.DeliveryDays)
			capsNeeded := o.Assignment.ItemPlan.ConsumptionRate*totalCoverageDays - remainingAtPurchase
			initialBoxes := int(math.Ceil(capsNeeded / float64(o.Assignment.Choice.Product.CapsulesPerBox)))
			if initialBoxes < 1 {
				initialBoxes = 1
			}
			if initialBoxes > o.Assignment.Choice.MaxBoxes {
				initialBoxes = o.Assignment.Choice.MaxBoxes
			}

			initialProducts = append(initialProducts, ProductOrder{
				ProductID:  o.Assignment.Choice.Product.ID,
				ItemID:     o.Assignment.ItemPlan.Item.ID,
				Quantity:   initialBoxes,
				UnitPrice:  o.Assignment.Choice.CatalogEntry.Price,
				TotalPrice: float64(initialBoxes) * o.Assignment.Choice.CatalogEntry.Price,
				Currency:   o.Assignment.Choice.CatalogEntry.Currency,
			})
		}

		initialTotal := 0.0
		for _, po := range initialProducts {
			initialTotal += po.TotalPrice
		}

		initialPurchases = append(initialPurchases, PurchaseEvent{
			Date:         purchaseDate.Format("2006-01-02"),
			SupplierID:   g.Supplier.ID,
			SupplierName: g.Supplier.Name,
			Products:     initialProducts,
			TotalCost:    math.Round(initialTotal*100) / 100,
			Currency:     supCurrency,
		})

		// Recurring checkout products.
		var recurringProducts []ProductOrder
		for _, o := range orders {
			recurringProducts = append(recurringProducts, ProductOrder{
				ProductID:  o.Assignment.Choice.Product.ID,
				ItemID:     o.Assignment.ItemPlan.Item.ID,
				Quantity:   o.Boxes,
				UnitPrice:  o.Assignment.Choice.CatalogEntry.Price,
				TotalPrice: float64(o.Boxes) * o.Assignment.Choice.CatalogEntry.Price,
				Currency:   o.Assignment.Choice.CatalogEntry.Currency,
			})
		}

		recurringTotal := 0.0
		for _, po := range recurringProducts {
			recurringTotal += po.TotalPrice
		}
		recurringTotal = math.Round(recurringTotal*100) / 100

		// Generate concrete recurring dates up to horizon.
		horizon := today.AddDate(0, 0, maxStockDays)
		for n := 1; ; n++ {
			d := purchaseDate.AddDate(0, 0, intervalDays*n)
			if d.After(horizon) {
				break
			}
			recurringCheckouts = append(recurringCheckouts, PurchaseEvent{
				Date:         d.Format("2006-01-02"),
				SupplierID:   g.Supplier.ID,
				SupplierName: g.Supplier.Name,
				Products:     recurringProducts,
				TotalCost:    recurringTotal,
				Currency:     supCurrency,
			})
		}

		// Summary contributions.
		checkoutsPerMonth := avgDaysPerMonth / float64(intervalDays)
		totalMonthlyCost += recurringTotal * checkoutsPerMonth

		for _, o := range orders {
			pricePerDose[o.Assignment.ItemPlan.Item.ID] = math.Round(o.Assignment.Choice.CostPerDose*100) / 100
		}
	}

	return Schedule{
		ID:                 scheduleID,
		Description:        description,
		InitialPurchases:   initialPurchases,
		RecurringCheckouts: recurringCheckouts,
		Summary: ScheduleSummary{
			MonthlyCost:  math.Round(totalMonthlyCost*100) / 100,
			Currency:     currency,
			PricePerDose: pricePerDose,
		},
		Errors: schedErrors,
	}
}

// BuildAllSchedules generates three schedules: preferred, cheapest, fastest.
func BuildAllSchedules(input Input, groups []ItemPlanGroup, today time.Time) []Schedule {
	strategies := []struct {
		Strategy    SelectionStrategy
		Description string
	}{
		{StrategyPreferred, "Preferred products, cheapest supplier"},
		{StrategyCheapest, "Cheapest cost per dose"},
		{StrategyFastest, "Fastest delivery"},
	}

	var schedules []Schedule
	for i, s := range strategies {
		sched := BuildSchedule(i+1, s.Description, groups, input.Products, input.Suppliers, input.HeadroomDays, today, s.Strategy)
		schedules = append(schedules, sched)
	}
	return schedules
}
