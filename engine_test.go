package main

import (
	"testing"
	"time"
)

var testToday = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// helper to build a minimal single-supplier input.
func singleSupplierInput(items []Item, products []Product, catalog []CatalogEntry, plans []ConsumptionPlan) Input {
	return Input{
		Items:    items,
		Products: products,
		Suppliers: []Supplier{{
			ID:      "sup",
			Name:    "Supplier",
			Catalog: catalog,
		}},
		ConsumptionPlans: plans,
		HeadroomDays:     0,
	}
}

// TestCheckoutIntervalSync verifies that when item A has a 60-unit box and
// item B has a 30-unit box (both consumed at 1/day), the engine syncs them
// to a 60-day checkout cycle: 1 box of A, 2 boxes of B.
func TestCheckoutIntervalSync(t *testing.T) {
	input := singleSupplierInput(
		[]Item{
			{ID: "a", Name: "A", DosagePerUnit: 1, Unit: UnitMg, MaxStockDays: 60},
			{ID: "b", Name: "B", DosagePerUnit: 1, Unit: UnitMg, MaxStockDays: 60},
		},
		[]Product{
			{ID: "a-60", ItemID: "a", UnitsPerBox: 60},
			{ID: "b-30", ItemID: "b", UnitsPerBox: 30},
		},
		[]CatalogEntry{
			{ProductID: "a-60", Price: 10, Currency: "USD", DeliveryDays: 0},
			{ProductID: "b-30", Price: 5, Currency: "USD", DeliveryDays: 0},
		},
		[]ConsumptionPlan{
			{ItemID: "a", Units: 1, Frequency: "* * *"},
			{ItemID: "b", Units: 1, Frequency: "* * *"},
		},
	)

	schedules, _ := ComputeSchedules(input, nil, testToday)
	sched := schedules[0] // preferred

	if len(sched.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", sched.Errors)
	}
	if len(sched.RecurringCheckouts) == 0 {
		t.Fatal("expected recurring checkouts")
	}

	initial := sched.InitialPurchases[0]
	recurring := sched.RecurringCheckouts[0]

	initialDate, _ := time.Parse("2006-01-02", initial.Date)
	recurringDate, _ := time.Parse("2006-01-02", recurring.Date)
	interval := int(recurringDate.Sub(initialDate).Hours() / 24)
	if interval != 60 {
		t.Errorf("expected 60-day interval, got %d", interval)
	}

	quantities := make(map[string]int)
	for _, po := range recurring.Products {
		quantities[po.ProductID] = po.Quantity
	}
	if quantities["a-60"] != 1 {
		t.Errorf("expected 1 box of a-60, got %d", quantities["a-60"])
	}
	if quantities["b-30"] != 2 {
		t.Errorf("expected 2 boxes of b-30, got %d", quantities["b-30"])
	}
}

// TestSubstanceModeResolution verifies that substance mode picks the item
// whose dosage_per_unit divides the requested dosage evenly.
func TestSubstanceModeResolution(t *testing.T) {
	input := singleSupplierInput(
		[]Item{
			{ID: "ala-250", Name: "ALA 250mg", Substance: "ala", DosagePerUnit: 250, Unit: UnitMg, MaxStockDays: 90},
			{ID: "ala-500", Name: "ALA 500mg", Substance: "ala", DosagePerUnit: 500, Unit: UnitMg, MaxStockDays: 90},
		},
		[]Product{
			{ID: "ala-250-60", ItemID: "ala-250", UnitsPerBox: 60},
			{ID: "ala-500-60", ItemID: "ala-500", UnitsPerBox: 60},
		},
		[]CatalogEntry{
			{ProductID: "ala-250-60", Price: 10, Currency: "USD", DeliveryDays: 0},
			{ProductID: "ala-500-60", Price: 15, Currency: "USD", DeliveryDays: 0},
		},
		[]ConsumptionPlan{
			{Substance: "ala", Dosage: 250, Frequency: "* * *"},
		},
	)

	schedules, _ := ComputeSchedules(input, nil, testToday)
	sched := schedules[0]

	if len(sched.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", sched.Errors)
	}

	// Should pick ala-250 (250/250=1 unit, integer) over ala-500 (250/500=0.5, not integer).
	found := false
	for _, pe := range sched.InitialPurchases {
		for _, po := range pe.Products {
			if po.ItemID == "ala-250" {
				found = true
			}
			if po.ItemID == "ala-500" {
				t.Error("should not have selected ala-500 for 250mg dosage")
			}
		}
	}
	if !found {
		t.Error("expected ala-250 to be selected")
	}
}

// TestFractionalUnits verifies that units=0.25 with a 30-unit box yields
// a supply of 120 days (30 / 0.25).
func TestFractionalUnits(t *testing.T) {
	input := singleSupplierInput(
		[]Item{
			{ID: "med", Name: "Med", DosagePerUnit: 15, Unit: UnitMg, MaxStockDays: 180},
		},
		[]Product{
			{ID: "med-30", ItemID: "med", UnitsPerBox: 30},
		},
		[]CatalogEntry{
			{ProductID: "med-30", Price: 8, Currency: "USD", DeliveryDays: 0},
		},
		[]ConsumptionPlan{
			{ItemID: "med", Units: 0.25, Frequency: "* * *"},
		},
	)

	schedules, _ := ComputeSchedules(input, nil, testToday)
	sched := schedules[0]

	if len(sched.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", sched.Errors)
	}
	if len(sched.RecurringCheckouts) == 0 {
		t.Fatal("expected recurring checkouts")
	}

	initial := sched.InitialPurchases[0]
	recurring := sched.RecurringCheckouts[0]

	initialDate, _ := time.Parse("2006-01-02", initial.Date)
	recurringDate, _ := time.Parse("2006-01-02", recurring.Date)
	interval := int(recurringDate.Sub(initialDate).Hours() / 24)

	// 30 units / 0.25 per day = 120 days per box.
	// MaxStockDays=180, so maxBoxes = floor(180*0.25/30) = 1, supply = 120 days.
	if interval != 120 {
		t.Errorf("expected 120-day interval, got %d", interval)
	}
}

// TestCheapestStrategyPicksCheaperSupplier verifies that schedule 2
// (cheapest cost per dose) selects the cheaper supplier.
func TestCheapestStrategyPicksCheaperSupplier(t *testing.T) {
	input := Input{
		Items: []Item{
			{ID: "x", Name: "X", DosagePerUnit: 100, Unit: UnitMg, MaxStockDays: 90},
		},
		Products: []Product{
			{ID: "x-60", ItemID: "x", UnitsPerBox: 60},
		},
		Suppliers: []Supplier{
			{
				ID:   "expensive",
				Name: "Expensive",
				Catalog: []CatalogEntry{
					{ProductID: "x-60", Price: 30, Currency: "USD", DeliveryDays: 3},
				},
			},
			{
				ID:   "cheap",
				Name: "Cheap",
				Catalog: []CatalogEntry{
					{ProductID: "x-60", Price: 10, Currency: "USD", DeliveryDays: 7},
				},
			},
		},
		ConsumptionPlans: []ConsumptionPlan{
			{ItemID: "x", Units: 1, Frequency: "* * *"},
		},
	}

	schedules, _ := ComputeSchedules(input, nil, testToday)

	// Schedule 2 = cheapest strategy.
	cheapest := schedules[1]
	if len(cheapest.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", cheapest.Errors)
	}
	if cheapest.InitialPurchases[0].SupplierID != "cheap" {
		t.Errorf("expected cheapest schedule to use 'cheap' supplier, got %q",
			cheapest.InitialPurchases[0].SupplierID)
	}

	// Schedule 3 = fastest strategy should pick expensive (3 days vs 7).
	fastest := schedules[2]
	if fastest.InitialPurchases[0].SupplierID != "expensive" {
		t.Errorf("expected fastest schedule to use 'expensive' supplier, got %q",
			fastest.InitialPurchases[0].SupplierID)
	}
}

// TestCurrentStockDefersInitialPurchase verifies that existing stock pushes
// the initial purchase date into the future.
func TestCurrentStockDefersInitialPurchase(t *testing.T) {
	input := singleSupplierInput(
		[]Item{
			{ID: "v", Name: "V", DosagePerUnit: 1, Unit: UnitMg, MaxStockDays: 90},
		},
		[]Product{
			{ID: "v-30", ItemID: "v", UnitsPerBox: 30},
		},
		[]CatalogEntry{
			{ProductID: "v-30", Price: 10, Currency: "USD", DeliveryDays: 0},
		},
		[]ConsumptionPlan{
			{ItemID: "v", Units: 1, Frequency: "* * *"},
		},
	)
	input.CurrentStock = []StockEntry{{ItemID: "v", Units: 30}}

	schedules, _ := ComputeSchedules(input, nil, testToday)
	sched := schedules[0]

	if len(sched.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", sched.Errors)
	}

	purchaseDate, _ := time.Parse("2006-01-02", sched.InitialPurchases[0].Date)
	daysDeferred := int(purchaseDate.Sub(testToday).Hours() / 24)

	// 30 units / 1 per day = 30 days of stock, headroom=0, delivery=0.
	if daysDeferred != 30 {
		t.Errorf("expected purchase deferred by 30 days, got %d", daysDeferred)
	}
}
