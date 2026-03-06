package main

import (
	"fmt"
	"strconv"
	"strings"
)

// parseField parses a single cron field into a list of integer values.
// Returns the values, whether the field was a wildcard, and any error.
// Supported syntax: *, */N, N, N-M, N-M/S, comma-separated combinations.
func parseField(field string, min, max int) ([]int, bool, error) {
	if field == "*" {
		vals := make([]int, 0, max-min+1)
		for i := min; i <= max; i++ {
			vals = append(vals, i)
		}
		return vals, true, nil
	}

	seen := make(map[int]bool)
	parts := strings.Split(field, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)

		if strings.HasPrefix(part, "*/") {
			stepStr := part[2:]
			step, err := strconv.Atoi(stepStr)
			if err != nil || step <= 0 {
				return nil, false, fmt.Errorf("invalid step in %q", part)
			}
			for i := min; i <= max; i += step {
				seen[i] = true
			}
			continue
		}

		if strings.Contains(part, "-") {
			rangeParts := strings.SplitN(part, "/", 2)
			step := 1
			if len(rangeParts) == 2 {
				var err error
				step, err = strconv.Atoi(rangeParts[1])
				if err != nil || step <= 0 {
					return nil, false, fmt.Errorf("invalid step in range %q", part)
				}
			}
			bounds := strings.SplitN(rangeParts[0], "-", 2)
			lo, err := strconv.Atoi(bounds[0])
			if err != nil {
				return nil, false, fmt.Errorf("invalid range start in %q", part)
			}
			hi, err := strconv.Atoi(bounds[1])
			if err != nil {
				return nil, false, fmt.Errorf("invalid range end in %q", part)
			}
			if lo < min || hi > max || lo > hi {
				return nil, false, fmt.Errorf("range %d-%d out of bounds [%d,%d]", lo, hi, min, max)
			}
			for i := lo; i <= hi; i += step {
				seen[i] = true
			}
			continue
		}

		val, err := strconv.Atoi(part)
		if err != nil {
			return nil, false, fmt.Errorf("invalid value %q", part)
		}
		if val < min || val > max {
			return nil, false, fmt.Errorf("value %d out of bounds [%d,%d]", val, min, max)
		}
		seen[val] = true
	}

	vals := make([]int, 0, len(seen))
	for v := range seen {
		vals = append(vals, v)
	}
	return vals, false, nil
}

// ParseCron parses a 3-field cron expression: "dom month dow".
// Fields: day-of-month (1-31), month (1-12), day-of-week (0-6, 0=Sunday).
func ParseCron(expr string) (ParsedCron, error) {
	fields := strings.Fields(expr)
	if len(fields) != 3 {
		return ParsedCron{}, fmt.Errorf("expected 3 fields (dom month dow), got %d in %q", len(fields), expr)
	}

	dom, domWild, err := parseField(fields[0], 1, 31)
	if err != nil {
		return ParsedCron{}, fmt.Errorf("day-of-month field: %w", err)
	}

	months, monthWild, err := parseField(fields[1], 1, 12)
	if err != nil {
		return ParsedCron{}, fmt.Errorf("month field: %w", err)
	}

	dow, dowWild, err := parseField(fields[2], 0, 6)
	if err != nil {
		return ParsedCron{}, fmt.Errorf("day-of-week field: %w", err)
	}

	return ParsedCron{
		DoM:       dom,
		Months:    months,
		DoW:       dow,
		DomWild:   domWild,
		MonthWild: monthWild,
		DowWild:   dowWild,
	}, nil
}

const avgDaysPerMonth = 30.44

// ActiveDayFraction computes the fraction of days per year the cron is active.
// This is an O(1) approximation suitable for supplement scheduling.
func ActiveDayFraction(pc ParsedCron) float64 {
	dayFraction := 1.0

	if !pc.DowWild && !pc.DomWild {
		// Both specified: cron semantics is union (fires on matching dow OR dom).
		// Approximate: min(1.0, dow_frac + dom_frac)
		dowFrac := float64(len(pc.DoW)) / 7.0
		domFrac := float64(len(pc.DoM)) / avgDaysPerMonth
		dayFraction = dowFrac + domFrac
		if dayFraction > 1.0 {
			dayFraction = 1.0
		}
	} else if !pc.DowWild {
		dayFraction = float64(len(pc.DoW)) / 7.0
	} else if !pc.DomWild {
		dayFraction = float64(len(pc.DoM)) / avgDaysPerMonth
	}

	monthFraction := 1.0
	if !pc.MonthWild {
		monthFraction = float64(len(pc.Months)) / 12.0
	}

	return dayFraction * monthFraction
}
