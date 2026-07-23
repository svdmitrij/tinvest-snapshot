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

func TestTypedFiltersTreatUnparseableValuesAsMissing(t *testing.T) {
	for _, missing := range []string{"н/д", "n/a", "not a date"} {
		t.Run(missing, func(t *testing.T) {
			dateColumns := []string{"Дата купона"}
			dateRows := [][]string{{"2026-01-01"}, {missing}, {"2026-12-31"}}
			if got := filterRows(dateRows, dateColumns, []filterCondition{{"Дата купона", ">=", "2026-06-01"}}, -1); len(got) != 1 || got[0][0] != "2026-12-31" {
				t.Fatalf("date >= result = %#v", got)
			}
			if got := filterRows(dateRows, dateColumns, []filterCondition{{"Дата купона", "<=", "2026-06-01"}}, -1); len(got) != 2 || got[0][0] != "2026-01-01" || got[1][0] != missing {
				t.Fatalf("date <= result = %#v", got)
			}

			numberColumns := []string{"Стоимость"}
			numberRows := [][]string{{"10"}, {missing}, {"100"}}
			if got := filterRows(numberRows, numberColumns, []filterCondition{{"Стоимость", ">=", "50"}}, -1); len(got) != 1 || got[0][0] != "100" {
				t.Fatalf("number >= result = %#v", got)
			}
			if got := filterRows(numberRows, numberColumns, []filterCondition{{"Стоимость", "<=", "50"}}, -1); len(got) != 2 || got[0][0] != "10" || got[1][0] != missing {
				t.Fatalf("number <= result = %#v", got)
			}
		})
	}
}

func TestExplicitEmptyValueFiltersRows(t *testing.T) {
	columns := []string{"Text"}
	rows := [][]string{{""}, {"value"}}
	if got := filterRows(rows, columns, []filterCondition{{"Text", "=", emptyFilterValue}}, -1); len(got) != 1 || got[0][0] != "" {
		t.Fatalf("empty equality = %#v", got)
	}
	if got := filterRows(rows, columns, []filterCondition{{"Text", "!=", emptyFilterValue}}, -1); len(got) != 1 || got[0][0] != "value" {
		t.Fatalf("empty inequality = %#v", got)
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
