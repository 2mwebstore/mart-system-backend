// Package utils' money helpers are the single place all currency math
// happens. USD is always integer cents, KHR is always integer riel — never
// float64 — per the business rules in docs/DECISIONS.md / build spec §3.
package utils

import "fmt"

// RielPerNoteUnit is the smallest riel note in daily circulation; all KHR
// cash amounts (change, totals shown "all in riel") round to this unit.
const RielPerNoteUnit int64 = 100

// RoundToNearest rounds v to the nearest multiple of unit, half away from
// zero. unit must be > 0.
func RoundToNearest(v, unit int64) int64 {
	if unit <= 0 {
		return v
	}
	if v >= 0 {
		return ((v + unit/2) / unit) * unit
	}
	return -((-v + unit/2) / unit) * unit
}

// RoundRielTo100 rounds a riel amount to the nearest ៛100 note.
func RoundRielTo100(riel int64) int64 {
	return RoundToNearest(riel, RielPerNoteUnit)
}

// USDCentsToRiel converts USD cents to riel at the given exchange rate
// (riel per 1 USD), rounded to the nearest ៛100.
func USDCentsToRiel(cents, rateRielPerUSD int64) int64 {
	riel := cents * rateRielPerUSD / 100
	return RoundRielTo100(riel)
}

// RielToUSDCents converts riel to USD cents at the given exchange rate.
// Used only to evaluate whether a mixed-currency payment covers the amount
// due — the authoritative amount tendered stays split by currency.
func RielToUSDCents(riel, rateRielPerUSD int64) int64 {
	if rateRielPerUSD == 0 {
		return 0
	}
	return riel * 100 / rateRielPerUSD
}

// ChangeResult is the full breakdown returned to the till for a cash sale.
type ChangeResult struct {
	// ChangeUSDCents / ChangeKHRRiel: the recommended split — whole dollars
	// in USD, remainder in riel rounded to ៛100.
	ChangeUSDCents int64
	ChangeKHRRiel  int64
	// ChangeAllKHRRiel: the same change amount expressed entirely in riel,
	// for a cashier who wants to give it all in one currency.
	ChangeAllKHRRiel int64
	// ShortCents is > 0 when the amount tendered does not cover the amount
	// due; ChangeUSDCents/ChangeKHRRiel are zero in that case.
	ShortCents int64
}

// CalculateChange applies the change rule from build spec §3: change is
// computed in USD, given as whole dollars in USD plus the remainder in
// riel rounded to the nearest ៛100. Payment can be tendered as USD and/or
// KHR simultaneously; both are converted to a USD-cents total to check
// against the amount due.
func CalculateChange(dueCents, receivedUSDCents, receivedKHRRiel, rateRielPerUSD int64) (ChangeResult, error) {
	if rateRielPerUSD <= 0 {
		return ChangeResult{}, fmt.Errorf("invalid exchange rate: %d", rateRielPerUSD)
	}

	receivedTotalCents := receivedUSDCents + RielToUSDCents(receivedKHRRiel, rateRielPerUSD)
	changeCents := receivedTotalCents - dueCents

	if changeCents < 0 {
		return ChangeResult{ShortCents: -changeCents}, nil
	}

	wholeDollarCents := (changeCents / 100) * 100
	remainderCents := changeCents - wholeDollarCents

	return ChangeResult{
		ChangeUSDCents:   wholeDollarCents,
		ChangeKHRRiel:    USDCentsToRiel(remainderCents, rateRielPerUSD),
		ChangeAllKHRRiel: USDCentsToRiel(changeCents, rateRielPerUSD),
	}, nil
}
