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
	if !matchesCondition(rows[2], columns, filterCondition{"Text", "=", ""}) {
		t.Fatal("empty must be data")
	}
	if !matchesCondition(rows[1], columns, filterCondition{"Text", "<=", "a"}) {
		t.Fatal("text ordering must work")
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
