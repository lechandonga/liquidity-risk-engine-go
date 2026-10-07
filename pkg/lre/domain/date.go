package domain

import (
	"fmt"
	"time"
)

// Date 是与时区无关的日历日，内部以 UTC 下距 1970-01-01 的天数表示。
// 所有现金流分桶都基于日历日，避免时区与夏令时带来的不确定性。
type Date struct {
	ord int
}

var dateEpoch = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

// NewDate 构造一个日历日；非法的年月日返回错误而不是静默归一化。
func NewDate(year, month, day int) (Date, error) {
	if month < 1 || month > 12 {
		return Date{}, fmt.Errorf("%w: month=%d", &RejectError{Code: CodeInvalidDate}, month)
	}
	if day < 1 || day > 31 {
		return Date{}, fmt.Errorf("%w: day=%d", &RejectError{Code: CodeInvalidDate}, day)
	}
	tm := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if tm.Year() != year || int(tm.Month()) != month || tm.Day() != day {
		return Date{}, fmt.Errorf("%w: %04d-%02d-%02d 不是合法日历日", &RejectError{Code: CodeInvalidDate}, year, month, day)
	}
	return Date{ord: int(tm.Sub(dateEpoch) / (24 * time.Hour))}, nil
}

// MustDate 用于测试与确定性常量；非法输入直接 panic。
func MustDate(year, month, day int) Date {
	d, err := NewDate(year, month, day)
	if err != nil {
		panic(err)
	}
	return d
}

// ParseDate 解析 YYYY-MM-DD。
func ParseDate(s string) (Date, error) {
	tm, err := time.Parse("2006-01-02", s)
	if err != nil {
		return Date{}, fmt.Errorf("%w: %q: %v", &RejectError{Code: CodeInvalidDate}, s, err)
	}
	return Date{ord: int(tm.Sub(dateEpoch) / (24 * time.Hour))}, nil
}

func (d Date) IsZero() bool { return d.ord == 0 }

func (d Date) AddDays(n int) Date { return Date{ord: d.ord + n} }

// DaysSince 返回 d - other 的整数天数；d 早于 other 时为负数。
func (d Date) DaysSince(other Date) int { return d.ord - other.ord }

func (d Date) String() string {
	if d.ord == 0 {
		return ""
	}
	tm := dateEpoch.Add(time.Duration(d.ord) * 24 * time.Hour)
	return tm.Format("2006-01-02")
}

// MarshalText / UnmarshalText 保证 JSON 持久化使用 YYYY-MM-DD。
func (d Date) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *Date) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		*d = Date{}
		return nil
	}
	parsed, err := ParseDate(string(b))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

func (d Date) Before(o Date) bool { return d.ord < o.ord }
func (d Date) After(o Date) bool  { return d.ord > o.ord }
func (d Date) Equal(o Date) bool  { return d.ord == o.ord }
