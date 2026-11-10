// Package cpi keeps INDEC's consumer price index (plan 5.4) so reports can
// show spending in today's pesos. The index is national, nivel general,
// base December 2016 = 100, one value per month.
package cpi

import (
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

// Point is one month's index. Month is the first day of the month, UTC.
type Point struct {
	Month time.Time
	Value decimal.Decimal
}

// Series is the stored index, oldest month first.
type Series struct {
	points []Point
}

// NewSeries sorts points by month. Values must be positive.
func NewSeries(points []Point) Series {
	p := append([]Point(nil), points...)
	sort.Slice(p, func(i, j int) bool { return p[i].Month.Before(p[j].Month) })
	return Series{points: p}
}

// Empty reports whether no month is stored yet.
func (s Series) Empty() bool { return len(s.points) == 0 }

// Through is the last published month.
func (s Series) Through() time.Time {
	if s.Empty() {
		return time.Time{}
	}
	return s.points[len(s.points)-1].Month
}

// Factor turns pesos spent in a month into today's pesos: the last
// published index over the month's. A month not published yet counts as
// the last one (factor 1: no inflation is assumed after it), and a month
// before the series as the first.
func (s Series) Factor(month time.Time) decimal.Decimal {
	if s.Empty() {
		return decimal.NewFromInt(1)
	}
	m := MonthOf(month)
	i := sort.Search(len(s.points), func(i int) bool { return s.points[i].Month.After(m) })
	base := s.points[0].Value
	if i > 0 {
		base = s.points[i-1].Value
	}
	return s.points[len(s.points)-1].Value.Div(base)
}

// MonthOf is the first day of t's month, UTC.
func MonthOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
