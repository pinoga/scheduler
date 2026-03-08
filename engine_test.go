package main

import (
	"testing"
	"time"
)

// TestCheckoutIntervalDrivenByShortestItem verifies that when a supplier
// sells item A (60-unit box) and item B (30-unit box), both consumed at
// 1 unit/day, the engine buys 1 A and 2 Bs per checkout to sync their
// supply durations.
func TestCheckoutIntervalDrivenByShortestItem(t *testing.T) {
	items := []Item{
		{ID: "item-a", Name: "Item A", DosagePerUnit: 100, Unit: UnitMg, MaxStockDays: 60},
		{ID: "item-b", Name: "Item B", DosagePerUnit: 100, Unit: UnitMg, MaxStockDays: 60},
	}
	products := []Product{
		{ID: "prod-a-60", ItemID: "item-a", UnitsPerBox: 60}, // 60 days per box
		{ID: "prod-b-30", ItemID: "item-b", UnitsPerBox: 30}, // 30 days per box
	}
	suppliers := []Supplier{
		{
			ID:   "supplier-1",
			Name: "Supplier 1",
			Catalog: []CatalogEntry{
				{ProductID: "prod-a-60", Price: 10, Currency: "USD", DeliveryDays: 0},
				{ProductID: "prod-b-30", Price: 5, Currency: "USD", DeliveryDays: 0},
			},
		},
	}

	// Both items consumed at 1 unit/day, every day.
	groups := []ItemPlanGroup{
		{
			Candidates: []ItemPlan{{
				Item:            items[0],
				Units:           1,
				ConsumptionRate: 1,
				CurrentStock:    0,
			}},
			Label: "item-a",
		},
		{
			Candidates: []ItemPlan{{
				Item:            items[1],
				Units:           1,
				ConsumptionRate: 1,
				CurrentStock:    0,
			}},
			Label: "item-b",
		},
	}

	today := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sched := BuildSchedule(1, "test", groups, products, suppliers, 0, today, StrategyPreferred)

	if len(sched.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", sched.Errors)
	}

	if len(sched.InitialPurchases) != 1 {
		t.Fatalf("expected 1 initial purchase, got %d", len(sched.InitialPurchases))
	}

	// Recurring checkouts should be every 60 days (both items synced to 60-day supply).
	if len(sched.RecurringCheckouts) == 0 {
		t.Fatal("expected recurring checkouts")
	}

	initial := sched.InitialPurchases[0]
	recurring := sched.RecurringCheckouts[0]

	initialDate, _ := time.Parse("2006-01-02", initial.Date)
	recurringDate, _ := time.Parse("2006-01-02", recurring.Date)
	interval := int(recurringDate.Sub(initialDate).Hours() / 24)
	if interval != 60 {
		t.Errorf("expected 60-day checkout interval, got %d", interval)
	}

	// Each recurring checkout: 1 box of A (60 units), 2 boxes of B (60 units).
	if len(recurring.Products) != 2 {
		t.Fatalf("expected 2 products in recurring checkout, got %d", len(recurring.Products))
	}

	quantities := make(map[string]int)
	for _, po := range recurring.Products {
		quantities[po.ProductID] = po.Quantity
	}

	if quantities["prod-a-60"] != 1 {
		t.Errorf("expected 1 box of prod-a-60, got %d", quantities["prod-a-60"])
	}
	if quantities["prod-b-30"] != 2 {
		t.Errorf("expected 2 boxes of prod-b-30, got %d", quantities["prod-b-30"])
	}
}
