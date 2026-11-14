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
//
// A payment with riel in it is checked in riel too: the riel amount due is
// rounded to ៛100 (USDCentsToRiel), so handing over exactly that, e.g.
// ៛12,900 for $3.15 at ៛4,100, pays the sale although it comes to a cent or
// two under the dollars. Such a payment gets no change. All-dollar payments
// are still checked to the cent.
func CalculateChange(dueCents, receivedUSDCents, receivedKHRRiel, rateRielPerUSD int64) (ChangeResult, error) {
	if rateRielPerUSD <= 0 {
		return ChangeResult{}, fmt.Errorf("invalid exchange rate: %d", rateRielPerUSD)
	}

	receivedTotalCents := receivedUSDCents + RielToUSDCents(receivedKHRRiel, rateRielPerUSD)
	changeCents := receivedTotalCents - dueCents

	if changeCents < 0 {
		if receivedKHRRiel > 0 && CoversRielDue(dueCents, receivedUSDCents, receivedKHRRiel, rateRielPerUSD) {
			return ChangeResult{}, nil
		}
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

// CoversRielDue reports whether a tender is worth at least the riel amount
// due, as the till shows it (rounded to ៛100). Compared in hundredths of a
// riel so nothing is rounded on the way.
func CoversRielDue(dueCents, receivedUSDCents, receivedKHRRiel, rateRielPerUSD int64) bool {
	return receivedKHRRiel*100+receivedUSDCents*rateRielPerUSD >= USDCentsToRiel(dueCents, rateRielPerUSD)*100
}

// CalculateChangeKHR is the change rule for a company that sells in riel:
// the amount due is whole riel, the cash due is that rounded to ៛100, dollars
// handed over count at the rate, and all change is given in riel, rounded to
// ៛100. ShortCents is the riel still owed.
func CalculateChangeKHR(dueRiel, receivedUSDCents, receivedKHRRiel, rateRielPerUSD int64) (ChangeResult, error) {
	if rateRielPerUSD <= 0 {
		return ChangeResult{}, fmt.Errorf("invalid exchange rate: %d", rateRielPerUSD)
	}
	cashDue := RoundRielTo100(dueRiel)
	received := receivedKHRRiel + receivedUSDCents*rateRielPerUSD/100
	if received < cashDue {
		return ChangeResult{ShortCents: cashDue - received}, nil
	}
	change := RoundRielTo100(received - cashDue)
	return ChangeResult{ChangeKHRRiel: change, ChangeAllKHRRiel: change}, nil
}

// CalculateChangeIn is the change rule for a company's main currency: due is
// in its minor unit (cents or riel).
func CalculateChangeIn(currency string, due, receivedUSDCents, receivedKHRRiel, rateRielPerUSD int64) (ChangeResult, error) {
	if currency == "KHR" {
		return CalculateChangeKHR(due, receivedUSDCents, receivedKHRRiel, rateRielPerUSD)
	}
	return CalculateChange(due, receivedUSDCents, receivedKHRRiel, rateRielPerUSD)
}

// RielIn is an amount of a company's main currency in riel: dollars at the
// rate rounded to ៛100, riel as it is.
func RielIn(currency string, amount, rateRielPerUSD int64) int64 {
	if currency == "KHR" {
		return amount
	}
	return USDCentsToRiel(amount, rateRielPerUSD)
}

// LoyaltyUnit is the amount loyalty points are earned per, in the main
// currency's minor unit: $1 (100 cents) or ៛1,000.
func LoyaltyUnit(currency string) int64 {
	if currency == "KHR" {
		return 1000
	}
	return 100
}
