package main

import "time"

// Unit represents the measurement unit for a supplement dosage.
type Unit string

const (
	UnitMg       Unit = "mg"
	UnitMcg      Unit = "mcg"
	UnitG        Unit = "g"
	UnitMl       Unit = "ml"
	UnitCapsules Unit = "capsules"
)

// ---------- Input domain types ----------

type Item struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Substance     string  `json:"substance,omitempty"` // optional grouping key for cross-item equivalence
	DosagePerUnit float64 `json:"dosage_per_unit"`
	Unit          Unit    `json:"unit"`
	MaxStockDays  int     `json:"max_stock_days"`
}

type Product struct {
	ID          string `json:"id"`
	ItemID      string `json:"item_id"`
	UnitsPerBox int    `json:"units_per_box"`
}

type CatalogEntry struct {
	ProductID    string  `json:"product_id"`
	Price        float64 `json:"price"`
	Currency     string  `json:"currency"`
	DeliveryDays int     `json:"delivery_days"`
}

type Supplier struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Catalog []CatalogEntry `json:"catalog"`
}

// ConsumptionPlan has two mutually exclusive modes:
//
// Substance mode: Substance + Dosage. Engine finds any item matching the substance
// whose dosage_per_unit divides the dosage evenly.
//
// Item mode: ItemID + Units. References a specific item directly.
// Units defaults to 1.0.
type ConsumptionPlan struct {
	// Mode selection (exactly one must be set)
	Substance string `json:"substance,omitempty"` // substance mode
	ItemID    string `json:"item_id,omitempty"`   // item mode

	// Substance mode fields
	Dosage float64 `json:"dosage,omitempty"` // required in substance mode

	// Item mode fields
	Units float64 `json:"units,omitempty"` // units per active day; default 1.0

	// Common fields
	Frequency string `json:"frequency"` // 3-field cron: "dom month dow"
}

type StockEntry struct {
	ItemID string `json:"item_id"`
	Units  int    `json:"units"`
}

type Input struct {
	Items            []Item            `json:"items"`
	Products         []Product         `json:"products"`
	Suppliers        []Supplier        `json:"suppliers"`
	ConsumptionPlans []ConsumptionPlan `json:"consumption_plans"`
	CurrentStock     []StockEntry      `json:"current_stock"`
	HeadroomDays     int               `json:"headroom_days"`
}

// ---------- Parsed cron (internal) ----------

type ParsedCron struct {
	DoM       []int
	Months    []int
	DoW       []int
	DomWild   bool
	MonthWild bool
	DowWild   bool
}

// ---------- Computed intermediates (not serialized) ----------

type ItemPlan struct {
	Item            Item
	Plan            ConsumptionPlan
	Units           float64 // units consumed per active day
	ConsumptionRate float64 // units per day (= units * active_day_fraction)
	CurrentStock    int
}

// ItemPlanGroup holds one or more candidate ItemPlans for a single consumption plan.
// Substance mode may produce multiple candidates (one per matching item).
// Item mode always produces exactly one candidate.
type ItemPlanGroup struct {
	Candidates []ItemPlan
	Label      string // for error messages: substance name or item ID
}

// SelectionStrategy determines how products and suppliers are chosen.
type SelectionStrategy int

const (
	StrategyPreferred SelectionStrategy = iota // first matching product in array order, cheapest supplier
	StrategyCheapest                           // lowest cost per dose across all product+supplier combos
	StrategyFastest                            // shortest delivery time, tie-break by cost
)

type ProductSupplierChoice struct {
	Product       Product
	Supplier      Supplier
	CatalogEntry  CatalogEntry
	MaxBoxes      int
	MaxSupplyDays float64
	DaysPerBox    float64
	CostPerDose   float64
}

// ---------- Output types ----------

type ProductOrder struct {
	ProductID  string  `json:"product_id"`
	ItemID     string  `json:"item_id"`
	Quantity   int     `json:"quantity"`
	UnitPrice  float64 `json:"unit_price"`
	TotalPrice float64 `json:"total_price"`
	Currency   string  `json:"currency"`
}

type PurchaseEvent struct {
	Date         string         `json:"date"` // YYYY-MM-DD
	SupplierID   string         `json:"supplier_id"`
	SupplierName string         `json:"supplier_name"`
	Products     []ProductOrder `json:"products"`
	TotalCost    float64        `json:"total_cost"`
	Currency     string         `json:"currency"`
}

type ScheduleSummary struct {
	MonthlyCost  float64            `json:"monthly_cost"`
	Currency     string             `json:"currency"`
	PricePerDose map[string]float64 `json:"price_per_dose"` // keyed by item_id
}

type ScheduleError struct {
	ItemID  string `json:"item_id"`
	Message string `json:"message"`
}

type Schedule struct {
	ID                 int             `json:"id"`
	Description        string          `json:"description"`
	InitialPurchases   []PurchaseEvent `json:"initial_purchases"`
	RecurringCheckouts []PurchaseEvent `json:"recurring_checkouts"`
	Summary            ScheduleSummary `json:"summary"`
	Errors             []ScheduleError `json:"errors,omitempty"`
}

type Output struct {
	GeneratedAt string     `json:"generated_at"`
	InputFile   string     `json:"input_file"`
	Schedules   []Schedule `json:"schedules"`
}

// ---------- State types ----------

type State struct {
	LastRunDate    time.Time    `json:"last_run_date"`
	StockAtLastRun []StockEntry `json:"stock_at_last_run"`
	InputHash      string       `json:"input_hash"`
}
