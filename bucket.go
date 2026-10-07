package liquidity

import "fmt"

// Bucket boundaries in whole days from the as-of date. The last bucket is
// open-ended.
var bucketBounds = []struct {
	Name    string
	MinDays int // inclusive
	MaxDays int // exclusive; -1 means unbounded
}{
	{"OVERNIGHT", 0, 1},
	{"D2_7", 1, 7},
	{"D8_14", 7, 14},
	{"D15_30", 14, 30},
	{"D31_90", 30, 90},
	{"D91_180", 90, 180},
	{"D181_365", 180, 365},
	{"GT_1Y", 365, -1},
}

// BucketCount is the number of time buckets.
const BucketCount = 8

// BucketNames exposes the bucket labels in order.
func BucketNames() []string {
	names := make([]string, BucketCount)
	for i, b := range bucketBounds {
		names[i] = b.Name
	}
	return names
}

// bucketIndex maps a day offset to a bucket; ok=false for past dates.
func bucketIndex(days int) (int, bool) {
	if days < 0 {
		return 0, false
	}
	for i, b := range bucketBounds {
		if days >= b.MinDays && (b.MaxDays < 0 || days < b.MaxDays) {
			return i, true
		}
	}
	return BucketCount - 1, true
}

// Ladder is a time-bucket cashflow ladder. Flows are signed: positive is a
// net inflow to the bank.
type Ladder struct {
	AsOf  Date
	Flows [BucketCount]Money
}

// Add records a signed flow at the given day offset.
func (l *Ladder) Add(days int, amount Money) {
	idx, ok := bucketIndex(days)
	if !ok {
		return // flows before the as-of date are not projected
	}
	l.Flows[idx] += amount
}

// NetAt returns the net cashflow in the bucket containing the given date.
func (l *Ladder) NetAt(date Date) (Money, error) {
	idx, ok := bucketIndex(l.AsOf.DaysTo(date))
	if !ok {
		return 0, fmt.Errorf("date %s is before as-of %s", date, l.AsOf)
	}
	return l.Flows[idx], nil
}

// CumulativeNet returns cumulative net cashflow up to and including the
// bucket containing date.
func (l *Ladder) CumulativeNet(date Date) (Money, error) {
	idx, ok := bucketIndex(l.AsOf.DaysTo(date))
	if !ok {
		return 0, fmt.Errorf("date %s is before as-of %s", date, l.AsOf)
	}
	var sum Money
	for i := 0; i <= idx; i++ {
		sum += l.Flows[i]
	}
	return sum, nil
}

// NetOutflow30D returns total net outflows (positive number) within 30 days,
// used by the LCR denominator.
func (l *Ladder) NetOutflow30D() Money {
	var net Money
	for i := 0; i < 4; i++ { // OVERNIGHT..D15_30
		net += l.Flows[i]
	}
	if net < 0 {
		return -net
	}
	return 0
}

// Cashflow is a single dated, signed flow produced by expanding a position.
type Cashflow struct {
	PositionID string
	Date       Date
	Amount     Money // signed: + inflow, - outflow
	Label      string
}

// signedAmount returns the contractual amount with the bank-perspective sign.
func signedAmount(p *Position) Money {
	if p.Direction == Inflow {
		return p.Amount
	}
	return -p.Amount
}
