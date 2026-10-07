// Package liquidity implements a fully local liquidity-risk measurement and
// stress-testing engine. Positions are expanded into time-bucket cashflow
// ladders, versioned scenarios apply behavioural assumptions, and an
// append-only event log with MVCC snapshots guarantees reproducible,
// race-free results without any external service.
package liquidity

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Money is an amount in minor currency units (cents). Integer arithmetic
// keeps bucketing results deterministic regardless of registration order.
type Money int64

// Date is a calendar date (no time-of-day) used for all business dates.
type Date struct {
	time.Time
}

func NewDate(y int, m time.Month, d int) Date {
	return Date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

func ParseDate(s string) (Date, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return Date{}, fmt.Errorf("invalid date %q: %w", s, err)
	}
	return Date{t}, nil
}

// DaysTo returns whole calendar days from d to other (negative if other<d).
func (d Date) DaysTo(other Date) int {
	return int(other.Sub(d.Time).Hours() / 24)
}

func (d Date) String() string { return d.Format("2006-01-02") }

// PositionType enumerates the supported balance-sheet instruments.
type PositionType string

const (
	DemandDeposit     PositionType = "DEMAND_DEPOSIT"
	TermDeposit       PositionType = "TERM_DEPOSIT"
	WholesaleFunding  PositionType = "WHOLESALE_FUNDING"
	UndrawnCreditLine PositionType = "UNDRAWN_CREDIT_LINE"
	Security          PositionType = "SECURITY"
	LoanRepayment     PositionType = "LOAN_REPAYMENT"
)

// Direction is the contractual cashflow direction of a position from the
// bank's perspective.
type Direction string

const (
	Inflow  Direction = "INFLOW"
	Outflow Direction = "OUTFLOW"
)

// expectedDirection defines the accounting convention for each position
// type: funding instruments are outflows at maturity, assets are inflows.
func expectedDirection(t PositionType) (Direction, bool) {
	switch t {
	case DemandDeposit, TermDeposit, WholesaleFunding:
		return Outflow, true
	case UndrawnCreditLine:
		// A committed line we extended: outflow when drawn.
		return Outflow, true
	case Security, LoanRepayment:
		return Inflow, true
	}
	return "", false
}

// Position is a single booked instrument.
type Position struct {
	ID           string
	Type         PositionType
	Direction    Direction
	Counterparty string
	Currency     string
	Amount       Money
	StartDate    Date
	MaturityDate Date // zero for demand deposits / undrawn lines
	RateBps      int  // contractual rate, basis points; informational
}

// RejectCode classifies input rejections so callers can distinguish causes.
type RejectCode string

const (
	CodeMissingField      RejectCode = "MISSING_FIELD"
	CodeInvalidAmount     RejectCode = "INVALID_AMOUNT"
	CodeInvalidTenor      RejectCode = "INVALID_TENOR"
	CodeDirectionMismatch RejectCode = "DIRECTION_MISMATCH"
	CodeDuplicatePosition RejectCode = "DUPLICATE_POSITION"
	CodeUnknownPosition   RejectCode = "UNKNOWN_POSITION"
	CodeUnknownEvent      RejectCode = "UNKNOWN_EVENT"
	CodeDuplicateBatch    RejectCode = "DUPLICATE_BATCH"
	CodeScenarioInvalid   RejectCode = "SCENARIO_INVALID"
	CodeUnknownScenario   RejectCode = "UNKNOWN_SCENARIO"
	CodeUnknownAssumption RejectCode = "UNKNOWN_ASSUMPTION"
)

// RejectError is a typed, distinguishable rejection of an input or command.
type RejectError struct {
	Code   RejectCode
	Field  string
	Detail string
}

func (e *RejectError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: %s: %s", e.Code, e.Field, e.Detail)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

// IsReject reports whether err is a rejection with the given code.
func IsReject(err error, code RejectCode) bool {
	var re *RejectError
	return errors.As(err, &re) && re.Code == code
}

// validate checks a position against the contract convention; nothing is
// silently defaulted or zeroed.
func (p *Position) validate() error {
	if p.ID == "" {
		return &RejectError{Code: CodeMissingField, Field: "ID", Detail: "position id is required"}
	}
	if _, ok := expectedDirection(p.Type); !ok {
		return &RejectError{Code: CodeMissingField, Field: "Type", Detail: fmt.Sprintf("unknown position type %q", p.Type)}
	}
	if p.Direction == "" {
		return &RejectError{Code: CodeMissingField, Field: "Direction", Detail: "direction is required"}
	}
	if want, _ := expectedDirection(p.Type); p.Direction != want {
		return &RejectError{Code: CodeDirectionMismatch, Field: "Direction",
			Detail: fmt.Sprintf("%s expects %s, got %s", p.Type, want, p.Direction)}
	}
	if p.Currency == "" {
		return &RejectError{Code: CodeMissingField, Field: "Currency", Detail: "currency is required"}
	}
	if p.Amount <= 0 {
		return &RejectError{Code: CodeInvalidAmount, Field: "Amount",
			Detail: fmt.Sprintf("amount must be positive, got %d", p.Amount)}
	}
	if p.StartDate.IsZero() {
		return &RejectError{Code: CodeMissingField, Field: "StartDate", Detail: "start date is required"}
	}
	switch p.Type {
	case DemandDeposit, UndrawnCreditLine:
		// Open-tenor instruments: maturity must be empty, not invented.
		if !p.MaturityDate.IsZero() {
			return &RejectError{Code: CodeInvalidTenor, Field: "MaturityDate",
				Detail: fmt.Sprintf("%s is open-tenor and must not carry a maturity", p.Type)}
		}
	default:
		if p.MaturityDate.IsZero() {
			return &RejectError{Code: CodeMissingField, Field: "MaturityDate",
				Detail: fmt.Sprintf("%s requires a maturity date", p.Type)}
		}
		if !p.MaturityDate.After(p.StartDate.Time) {
			return &RejectError{Code: CodeInvalidTenor, Field: "MaturityDate",
				Detail: fmt.Sprintf("maturity %s must be after start %s", p.MaturityDate, p.StartDate)}
		}
	}
	return nil
}

// sortedPositions returns positions ordered by ID so any downstream
// aggregation is independent of registration order.
func sortedPositions(m map[string]*Position) []*Position {
	out := make([]*Position, 0, len(m))
	for _, p := range m {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
