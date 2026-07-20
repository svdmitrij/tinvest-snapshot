//go:build !ci

package main

import "testing"

func TestDynamicFiltersCompareValuesAndCombineWithAnd(t *testing.T) {
	columns := []string{"Date", "Amount", "Text"}
	rows := [][]string{{"2030-01-01", "1 200,5", "b"}, {"2031-01-01", "9", "a"}, {"", "", ""}}
	got := filterRows(rows, columns, []filterCondition{{"Date", ">=", "2030-01-01"}, {"Amount", ">=", "10"}}, -1)
	if len(got) != 1 || got[0][2] != "b" {
		t.Fatalf("filtered rows = %#v", got)
	}
	if got := filterRows(rows, columns, []filterCondition{{"Text", "=", ""}}, -1); len(got) != len(rows) {
		t.Fatalf("incomplete condition restricted rows: %#v", got)
	}
	if !matchesCondition(rows[1], columns, filterCondition{"Text", "<=", "a"}) {
		t.Fatal("text ordering must work")
	}
}

func TestTypedComparisonParsesMoneyAndCalendarDate(t *testing.T) {
	if got := compareValues("1 500,00 ₽", "1000", numberColumn); got <= 0 {
		t.Fatalf("currency comparison = %d", got)
	}
	if got := compareValues("2026-01-02 01:00:00", "2026-01-02", dateColumn); got != 0 {
		t.Fatalf("datetime calendar comparison = %d", got)
	}
	columns := []string{"Дата и время", "Номинал"}
	rows := [][]string{{"2026-01-02 01:00:00", "1 500,00 ₽"}, {"2026-01-03 01:00:00", "500 ₽"}}
	got := filterRows(rows, columns, []filterCondition{{"Дата и время", "=", "2026-01-02"}, {"Номинал", ">=", "1000"}}, -1)
	if len(got) != 1 {
		t.Fatalf("typed filter = %#v", got)
	}
}

func TestDynamicFilterValuesExcludeOwnCondition(t *testing.T) {
	columns := []string{"A", "B"}
	rows := [][]string{{"x", "one"}, {"x", "two"}, {"y", "two"}}
	conditions := []filterCondition{{"A", "=", "x"}, {"B", "=", "one"}}
	values := conditionValues(filterRows(rows, columns, conditions, 0), columns, conditions[0])
	if len(values) != 2 || values[1] != "x" {
		t.Fatalf("own cascade values = %#v", values)
	}
}

func TestQualValuesAndUnknownAreFilterable(t *testing.T) {
	columns := []string{"Квал"}
	rows := [][]string{{"true"}, {"false"}, {"?"}}
	if got := filterRows(rows, columns, []filterCondition{{"Квал", "=", "true"}}, -1); len(got) != 1 || got[0][0] != "true" {
		t.Fatalf("true filter = %#v", got)
	}
	if got := filterRows(rows, columns, []filterCondition{{"Квал", "!=", "?"}}, -1); len(got) != 2 {
		t.Fatalf("unknown filter = %#v", got)
	}
}
