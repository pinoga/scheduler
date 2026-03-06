package main

import "time"

// Unit represents the measurement unit for a supplement dosage.
type Unit string

const (
	UnitMg  Unit = "mg"
	UnitMcg Unit = "mcg"
	UnitG   Unit = "g"
	UnitMl  Unit = "ml"
)

// ---------- Input domain types ----------

type Item struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	DosagePerUnit float64 `json:"dosage_per_unit"`
	Unit          Unit    `json:"unit"`
	MaxStockDays  int     `json:"max_stock_days"`
}

type Product struct {
	ID             string `json:"id"`
	ItemID         string `json:"item_id"`
	CapsulesPerBox int    `json:"capsules_per_box"`
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

type ConsumptionPlan struct {
	ItemID      string  `json:"item_id"`
	Dosage      float64 `json:"dosage"`
	Frequency   string  `json:"frequency"`      // 3-field cron: "dom month dow"
	TimesPerDay int     `json:"times_per_day"`   // defaults to 1 if omitted
}

type StockEntry struct {
	ItemID   string `json:"item_id"`
	Capsules int    `json:"capsules"`
}

type Input struct {
	Items            []Item            `json:"items"`
	Products         []Product         `json:"products"`
	Suppliers        []Supplier        `json:"suppliers"` // index 0 = highest priority
	ConsumptionPlans []ConsumptionPlan `json:"consumption_plans"`
	CurrentStock     []StockEntry      `json:"current_stock"`
	HeadroomDays     int               `json:"headroom_days"`
}

// ---------- Parsed cron (internal) ----------

type ParsedCron struct {
	DoM      []int
	Months   []int
	DoW      []int
	DomWild  bool
	MonthWild bool
	DowWild  bool
}

// ---------- Computed intermediates (not serialized) ----------

type ItemPlan struct {
	Item            Item
	Plan            ConsumptionPlan
	CapsulesPerDose int
	DosesPerDay     float64
	ConsumptionRate float64 // capsules per day
	CurrentCapsules int
}

type SupplierProductChoice struct {
	Product       Product
	CatalogEntry  CatalogEntry
	MaxBoxes      int
	MaxSupplyDays float64
	DaysPerBox    float64
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
	ID                 int              `json:"id"`
	Description        string           `json:"description"`
	InitialPurchases   []PurchaseEvent  `json:"initial_purchases"`
	RecurringCheckouts []PurchaseEvent  `json:"recurring_checkouts"`
	Summary            ScheduleSummary  `json:"summary"`
	Errors             []ScheduleError  `json:"errors,omitempty"`
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
