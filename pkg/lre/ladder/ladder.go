package ladder

import (
	"sort"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
)

// Ladder 是一组头寸展开后的现金流阶梯。
// day 为排序后的唯一日期，net[day] 为该日带符号净现金流（流入为正）。
type Ladder struct {
	days []domain.Date
	net  map[int]int64 // key = Date 序数
}

func New() *Ladder { return &Ladder{net: make(map[int]int64)} }

// AddFlow 加入一笔现金流；同日多笔自动累加。
func (l *Ladder) AddFlow(f domain.Flow) {
	if f.Amount == 0 || f.Date.IsZero() {
		return
	}
	key := ord(f.Date)
	if _, ok := l.net[key]; !ok {
		l.days = append(l.days, f.Date)
	}
	l.net[key] += f.Amount
}

// NetAt 返回 (from, at] 区间（含 at，不含 from）内的净现金流。
// from 为零值时从最早日期开始累计。
func (l *Ladder) NetAt(from, at domain.Date) int64 {
	var total int64
	for key, v := range l.net {
		d := fromOrd(key)
		if (!from.IsZero() && !d.After(from)) || d.After(at) {
			continue
		}
		total += v
	}
	return total
}

// DayNet 返回指定日期当日的净现金流。
func (l *Ladder) DayNet(d domain.Date) int64 { return l.net[ord(d)] }

// Days 返回按升序排列、存在现金流的日期集合。
func (l *Ladder) Days() []domain.Date {
	out := append([]domain.Date(nil), l.days...)
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// SortedFlows 仅用于解释性输出/测试。
func (l *Ladder) SortedFlows() []struct {
	Date   domain.Date
	Amount int64
} {
	days := l.Days()
	out := make([]struct {
		Date   domain.Date
		Amount int64
	}, len(days))
	for i, d := range days {
		out[i] = struct {
			Date   domain.Date
			Amount int64
		}{d, l.net[ord(d)]}
	}
	return out
}

func ord(d domain.Date) int { return d.DaysSince(domain.MustDate(1970, 1, 1)) }

func fromOrd(n int) domain.Date { return domain.MustDate(1970, 1, 1).AddDays(n) }
