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

// computeProductMetrics calculates supply metrics for a product given an item's consumption rate.
func computeProductMetrics(itemPlan ItemPlan, product Product, ce CatalogEntry, supplier Supplier) ProductSupplierChoice {
	daysPerBox := float64(product.CapsulesPerBox) / itemPlan.ConsumptionRate
	maxBoxes := int(math.Floor(float64(itemPlan.Item.MaxStockDays) * itemPlan.ConsumptionRate / float64(product.CapsulesPerBox)))
	if maxBoxes < 1 {
		maxBoxes = 1
	}
	maxSupplyDays := float64(maxBoxes*product.CapsulesPerBox) / itemPlan.ConsumptionRate
	costPerCapsule := ce.Price / float64(product.CapsulesPerBox)
	costPerDose := costPerCapsule * float64(itemPlan.CapsulesPerDose)

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

// SelectProductAndSupplier picks the best product+supplier for an item using the given strategy.
// Products are implicitly prioritized by their order in the products array.
func SelectProductAndSupplier(itemPlan ItemPlan, products []Product, suppliers []Supplier, strategy SelectionStrategy) *ProductSupplierChoice {
	if itemPlan.ConsumptionRate <= 0 {
		return nil
	}

	// Build a catalog index: product_id -> [(supplier, catalog_entry)]
	type supplierOffer struct {
		Supplier     Supplier
		CatalogEntry CatalogEntry
	}
	offersByProduct := make(map[string][]supplierOffer)
	for _, sup := range suppliers {
		for _, ce := range sup.Catalog {
			offersByProduct[ce.ProductID] = append(offersByProduct[ce.ProductID], supplierOffer{sup, ce})
		}
	}

	switch strategy {
	case StrategyPreferred:
		// Walk products in array order (user's priority). Pick the first product
		// that has any supplier, then choose the cheapest supplier for it.
		for _, prod := range products {
			if prod.ItemID != itemPlan.Item.ID {
				continue
			}
			offers := offersByProduct[prod.ID]
			if len(offers) == 0 {
				continue
			}
			// Pick cheapest supplier for this product.
			var best *ProductSupplierChoice
			for _, o := range offers {
				choice := computeProductMetrics(itemPlan, prod, o.CatalogEntry, o.Supplier)
				if best == nil || choice.CostPerDose < best.CostPerDose {
					best = &choice
				}
			}
			return best
		}
		return nil

	case StrategyCheapest:
		// Find the product+supplier combo with the lowest cost per dose.
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
		// Find the product+supplier combo with shortest delivery, tie-break by cost.
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

type itemAssignment struct {
	ItemPlan ItemPlan
	Choice   ProductSupplierChoice
}

// BuildSchedule constructs a single schedule using the given selection strategy.
func BuildSchedule(scheduleID int, description string, itemPlans []ItemPlan, products []Product, suppliers []Supplier, headroomDays int, today time.Time, strategy SelectionStrategy) Schedule {
	// Phase 1: Select product+supplier for each item.
	var assignments []itemAssignment
	var schedErrors []ScheduleError

	for _, ip := range itemPlans {
		choice := SelectProductAndSupplier(ip, products, suppliers, strategy)
		if choice == nil {
			schedErrors = append(schedErrors, ScheduleError{
				ItemID:  ip.Item.ID,
				Message: "no product+supplier available for this item",
			})
			continue
		}
		assignments = append(assignments, itemAssignment{ItemPlan: ip, Choice: *choice})
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
func BuildAllSchedules(input Input, itemPlans []ItemPlan, today time.Time) []Schedule {
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
		sched := BuildSchedule(i+1, s.Description, itemPlans, input.Products, input.Suppliers, input.HeadroomDays, today, s.Strategy)
		schedules = append(schedules, sched)
	}
	return schedules
}
