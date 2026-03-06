package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

func FormatOutput(schedules []Schedule, inputFile string, now time.Time) Output {
	return Output{
		GeneratedAt: now.Format(time.RFC3339),
		InputFile:   inputFile,
		Schedules:   schedules,
	}
}

func WriteOutput(output Output, w io.Writer) error {
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling output: %w", err)
	}
	_, err = w.Write(data)
	if err != nil {
		return fmt.Errorf("writing output: %w", err)
	}
	_, err = w.Write([]byte("\n"))
	return err
}

func PrintOutput(output Output) error {
	return WriteOutput(output, os.Stdout)
}

// PrintSummaryToStderr prints a human-readable summary table to stderr.
func PrintSummaryToStderr(output Output) {
	for _, sched := range output.Schedules {
		fmt.Fprintf(os.Stderr, "\nSchedule %d (%s)\n", sched.ID, sched.Description)
		fmt.Fprintf(os.Stderr, "  Monthly cost: %s %.2f\n", sched.Summary.Currency, sched.Summary.MonthlyCost)

		if len(sched.InitialPurchases) > 0 {
			for _, ip := range sched.InitialPurchases {
				fmt.Fprintf(os.Stderr, "  Next purchase: %s from %s (%s %.2f)\n",
					ip.Date, ip.SupplierName, ip.Currency, ip.TotalCost)
				for _, p := range ip.Products {
					fmt.Fprintf(os.Stderr, "    - %s x%d (%s %.2f each)\n",
						p.ProductID, p.Quantity, p.Currency, p.UnitPrice)
				}
			}
		}

		if len(sched.RecurringCheckouts) > 0 {
			fmt.Fprintf(os.Stderr, "  Recurring checkouts: %d planned\n", len(sched.RecurringCheckouts))
			// Show first 3 dates.
			limit := 3
			if len(sched.RecurringCheckouts) < limit {
				limit = len(sched.RecurringCheckouts)
			}
			for _, rc := range sched.RecurringCheckouts[:limit] {
				fmt.Fprintf(os.Stderr, "    %s from %s (%s %.2f)\n",
					rc.Date, rc.SupplierName, rc.Currency, rc.TotalCost)
			}
			if len(sched.RecurringCheckouts) > 3 {
				fmt.Fprintf(os.Stderr, "    ... and %d more\n", len(sched.RecurringCheckouts)-3)
			}
		}

		if len(sched.Summary.PricePerDose) > 0 {
			fmt.Fprintf(os.Stderr, "  Price per dose:\n")
			for itemID, ppd := range sched.Summary.PricePerDose {
				fmt.Fprintf(os.Stderr, "    %s: %s %.2f\n", itemID, sched.Summary.Currency, ppd)
			}
		}

		if len(sched.Errors) > 0 {
			fmt.Fprintf(os.Stderr, "  Errors:\n")
			for _, e := range sched.Errors {
				fmt.Fprintf(os.Stderr, "    %s: %s\n", e.ItemID, e.Message)
			}
		}
	}
	fmt.Fprintln(os.Stderr)
}
