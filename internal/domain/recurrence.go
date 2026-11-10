package domain

import "time"

// RecurrenceMatches reports whether a recurring series (expense or income)
// falls on date: monthly on dayOfMonth, weekly on dayOfWeek (0 = Sunday),
// yearly on dayOfMonth of monthOfYear. A day past the month's end falls on
// its last day (31 in February is the 28th or 29th).
func RecurrenceMatches(frequency string, dayOfMonth, dayOfWeek, monthOfYear *int, date time.Time) bool {
	year, month, day := date.Date()
	switch frequency {
	case "monthly":
		if dayOfMonth == nil {
			return false
		}
		return day == ClampDay(*dayOfMonth, year, int(month))
	case "weekly":
		if dayOfWeek == nil {
			return false
		}
		return int(date.Weekday()) == *dayOfWeek
	case "yearly":
		if dayOfMonth == nil || monthOfYear == nil {
			return false
		}
		return int(month) == *monthOfYear && day == ClampDay(*dayOfMonth, year, *monthOfYear)
	}
	return false
}

// ClampDay caps d at the last day of the month (dayOfMonth 31 in a
// non-leap February is 28).
func ClampDay(d, year, month int) int {
	last := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if d > last {
		return last
	}
	return d
}
