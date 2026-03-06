package main

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// BuildItemPlans computes per-item consumption data from the input.
// effectiveStock may be nil (first pass to get consumption rates for stock decay).
func BuildItemPlans(input Input, effectiveStock map[string]int) ([]ItemPlan, []ScheduleError) {
	itemByID := make(map[string]Item, len(input.Items))
	for _, item := range input.Items {
		itemByID[item.ID] = item
	}

	var plans []ItemPlan
	var errs []ScheduleError

	for _, cp := range input.ConsumptionPlans {
		item, ok := itemByID[cp.ItemID]
		if !ok {
			errs = append(errs, ScheduleError{ItemID: cp.ItemID, Message: "item not found"})
			continue
		}

		ratio := cp.Dosage / item.DosagePerUnit
		capsPerDose := int(math.Round(ratio))
		if capsPerDose < 1 || math.Abs(ratio-float64(capsPerDose)) > 1e-9 {
			errs = append(errs, ScheduleError{
				ItemID:  cp.ItemID,
				Message: fmt.Sprintf("dosage %.4g / dosage_per_unit %.4g = %.4g is not a positive integer", cp.Dosage, item.DosagePerUnit, ratio),
			})
			continue
		}

		pc, err := ParseCron(cp.Frequency)
		if err != nil {
			errs = append(errs, ScheduleError{ItemID: cp.ItemID, Message: fmt.Sprintf("invalid frequency: %v", err)})
			continue
		}

		activeFrac := ActiveDayFraction(pc)
		dosesPerDay := float64(cp.TimesPerDay) * activeFrac
		consumptionRate := float64(capsPerDose) * dosesPerDay

		currentCaps := 0
		if effectiveStock != nil {
			currentCaps = effectiveStock[item.ID]
		}

		plans = append(plans, ItemPlan{
			Item:            item,
			Plan:            cp,
			CapsulesPerDose: capsPerDose,
			DosesPerDay:     dosesPerDay,
			ConsumptionRate: consumptionRate,
			CurrentCapsules: currentCaps,
		})
	}

	return plans, errs
}

// SelectBestProduct picks the product for an item at a supplier that maximizes max_supply_days.
func SelectBestProduct(itemPlan ItemPlan, products []Product, catalog []CatalogEntry) *SupplierProductChoice {
	// Index catalog by product ID.
	catByProd := make(map[string]CatalogEntry, len(catalog))
	for _, ce := range catalog {
		catByProd[ce.ProductID] = ce
	}

	var best *SupplierProductChoice

	for _, prod := range products {
		if prod.ItemID != itemPlan.Item.ID {
			continue
		}
		ce, ok := catByProd[prod.ID]
		if !ok {
			continue
		}
		if itemPlan.ConsumptionRate <= 0 {
			continue
		}

		daysPerBox := float64(prod.CapsulesPerBox) / itemPlan.ConsumptionRate
		maxBoxes := int(math.Floor(float64(itemPlan.Item.MaxStockDays) * itemPlan.ConsumptionRate / float64(prod.CapsulesPerBox)))
		if maxBoxes < 1 {
			maxBoxes = 1
		}
		maxSupplyDays := float64(maxBoxes*prod.CapsulesPerBox) / itemPlan.ConsumptionRate

		choice := &SupplierProductChoice{
			Product:       prod,
			CatalogEntry:  ce,
			MaxBoxes:      maxBoxes,
			MaxSupplyDays: maxSupplyDays,
			DaysPerBox:    daysPerBox,
		}

		if best == nil || maxSupplyDays > best.MaxSupplyDays {
			best = choice
		} else if maxSupplyDays == best.MaxSupplyDays {
			// Tie-break: lower price per capsule.
			bestPPC := best.CatalogEntry.Price / float64(best.Product.CapsulesPerBox)
			choicePPC := ce.Price / float64(prod.CapsulesPerBox)
			if choicePPC < bestPPC {
				best = choice
			}
		}
	}

	return best
}

type supplierAssignment struct {
	Supplier Supplier
	ItemPlan ItemPlan
	Choice   SupplierProductChoice
}

// BuildSchedule constructs a single schedule using the given supplier priority order.
func BuildSchedule(scheduleID int, supplierOrder []Supplier, itemPlans []ItemPlan, products []Product, headroomDays int, today time.Time) Schedule {
	// Phase 1: Assign items to suppliers.
	assigned := make(map[string]supplierAssignment)
	var schedErrors []ScheduleError

	for _, sup := range supplierOrder {
		for _, ip := range itemPlans {
			if _, ok := assigned[ip.Item.ID]; ok {
				continue // already assigned
			}
			choice := SelectBestProduct(ip, products, sup.Catalog)
			if choice != nil {
				assigned[ip.Item.ID] = supplierAssignment{
					Supplier: sup,
					ItemPlan: ip,
					Choice:   *choice,
				}
			}
		}
	}

	// Items that couldn't be assigned.
	for _, ip := range itemPlans {
		if _, ok := assigned[ip.Item.ID]; !ok {
			schedErrors = append(schedErrors, ScheduleError{
				ItemID:  ip.Item.ID,
				Message: "no supplier carries a product for this item",
			})
		}
	}

	// Group assignments by supplier.
	type supplierGroup struct {
		Supplier    Supplier
		Assignments []supplierAssignment
	}
	groupMap := make(map[string]*supplierGroup)
	for _, a := range assigned {
		g, ok := groupMap[a.Supplier.ID]
		if !ok {
			g = &supplierGroup{Supplier: a.Supplier}
			groupMap[a.Supplier.ID] = g
		}
		g.Assignments = append(g.Assignments, a)
	}

	// Deterministic order: by supplier ID.
	var groups []*supplierGroup
	for _, g := range groupMap {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Supplier.ID < groups[j].Supplier.ID
	})

	var initialPurchases []PurchaseEvent
	var recurringCheckouts []PurchaseEvent
	totalMonthlyCost := 0.0
	pricePerDose := make(map[string]float64)
	currency := ""

	// Compute horizon for recurring dates.
	maxStockDays := 0
	for _, ip := range itemPlans {
		if ip.Item.MaxStockDays > maxStockDays {
			maxStockDays = ip.Item.MaxStockDays
		}
	}

	for _, g := range groups {
		// Phase 2: Compute checkout interval = min(maxSupplyDays) across items.
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

		// Phase 3: Compute boxes per checkout for each item.
		type itemOrder struct {
			Assignment supplierAssignment
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

		// Phase 4: Initial purchase date = earliest reorder date among items.
		earliestReorder := math.Inf(1)
		maxDeliveryDays := 0
		for _, o := range orders {
			deliveryDays := o.Assignment.Choice.CatalogEntry.DeliveryDays
			if deliveryDays > maxDeliveryDays {
				maxDeliveryDays = deliveryDays
			}
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

		// Phase 5: Build initial purchase event.
		var initialProducts []ProductOrder
		if currency == "" && len(orders) > 0 {
			currency = orders[0].Assignment.Choice.CatalogEntry.Currency
		}
		supCurrency := ""
		if len(orders) > 0 {
			supCurrency = orders[0].Assignment.Choice.CatalogEntry.Currency
		}

		for _, o := range orders {
			// For initial purchase, compute how many boxes are needed to cover until
			// the first recurring delivery arrives.
			daysUntilPurchase := math.Floor(earliestReorder)
			capsConsumedWaiting := o.Assignment.ItemPlan.ConsumptionRate * daysUntilPurchase
			remainingAtPurchase := float64(o.Assignment.ItemPlan.CurrentCapsules) - capsConsumedWaiting
			if remainingAtPurchase < 0 {
				remainingAtPurchase = 0
			}

			// Need to cover checkout_interval + delivery_days from purchase date.
			totalCoverageDays := float64(intervalDays) + float64(o.Assignment.Choice.CatalogEntry.DeliveryDays)
			capsNeeded := o.Assignment.ItemPlan.ConsumptionRate*totalCoverageDays - remainingAtPurchase
			initialBoxes := int(math.Ceil(capsNeeded / float64(o.Assignment.Choice.Product.CapsulesPerBox)))
			if initialBoxes < 1 {
				initialBoxes = 1
			}
			if initialBoxes > o.Assignment.Choice.MaxBoxes {
				initialBoxes = o.Assignment.Choice.MaxBoxes
			}

			po := ProductOrder{
				ProductID:  o.Assignment.Choice.Product.ID,
				ItemID:     o.Assignment.ItemPlan.Item.ID,
				Quantity:   initialBoxes,
				UnitPrice:  o.Assignment.Choice.CatalogEntry.Price,
				TotalPrice: float64(initialBoxes) * o.Assignment.Choice.CatalogEntry.Price,
				Currency:   o.Assignment.Choice.CatalogEntry.Currency,
			}
			initialProducts = append(initialProducts, po)
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

		// Phase 5b: Build recurring checkout products (same boxes every time).
		var recurringProducts []ProductOrder
		for _, o := range orders {
			po := ProductOrder{
				ProductID:  o.Assignment.Choice.Product.ID,
				ItemID:     o.Assignment.ItemPlan.Item.ID,
				Quantity:   o.Boxes,
				UnitPrice:  o.Assignment.Choice.CatalogEntry.Price,
				TotalPrice: float64(o.Boxes) * o.Assignment.Choice.CatalogEntry.Price,
				Currency:   o.Assignment.Choice.CatalogEntry.Currency,
			}
			recurringProducts = append(recurringProducts, po)
		}

		recurringTotal := 0.0
		for _, po := range recurringProducts {
			recurringTotal += po.TotalPrice
		}
		recurringTotal = math.Round(recurringTotal*100) / 100

		// Generate concrete recurring dates.
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

		// Phase 6: Summary contributions.
		checkoutsPerMonth := avgDaysPerMonth / float64(intervalDays)
		totalMonthlyCost += recurringTotal * checkoutsPerMonth

		for _, o := range orders {
			costPerCapsule := o.Assignment.Choice.CatalogEntry.Price / float64(o.Assignment.Choice.Product.CapsulesPerBox)
			pricePerDose[o.Assignment.ItemPlan.Item.ID] = math.Round(costPerCapsule*float64(o.Assignment.ItemPlan.CapsulesPerDose)*100) / 100
		}
	}

	return Schedule{
		ID:                 scheduleID,
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

// BuildAllSchedules generates one schedule per supplier rotation.
func BuildAllSchedules(input Input, itemPlans []ItemPlan, today time.Time) []Schedule {
	n := len(input.Suppliers)
	if n == 0 {
		return nil
	}

	var schedules []Schedule
	for i := 0; i < n; i++ {
		// Rotate: start from supplier i.
		rotated := make([]Supplier, n)
		for j := 0; j < n; j++ {
			rotated[j] = input.Suppliers[(i+j)%n]
		}

		desc := "Primary: " + rotated[0].Name
		if len(rotated) > 1 {
			desc += ", fallback: " + rotated[1].Name
		}
		if len(rotated) > 2 {
			desc += ", ..."
		}

		sched := BuildSchedule(i+1, rotated, itemPlans, input.Products, input.HeadroomDays, today)
		sched.Description = desc
		schedules = append(schedules, sched)
	}

	return schedules
}
