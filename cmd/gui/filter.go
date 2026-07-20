//go:build !ci

package main

import (
	"strconv"
	"strings"
	"time"
)

type filterCondition struct{ Column, Operation, Value string }

func filterColumnIndex(columns []string, name string) int {
	for i, column := range columns {
		if column == name {
			return i
		}
	}
	return -1
}

func isDateValue(value string) bool {
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func numericValue(value string) (float64, bool) {
	value = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), " ", ""), ",", ".")
	v, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
	return v, err == nil
}

// compareValues follows table sorting: empty data is lower than any value,
// then dates and numbers compare by their actual value, otherwise by text.
func compareValues(left, right string) int {
	if left == right {
		return 0
	}
	if left == "" {
		return -1
	}
	if right == "" {
		return 1
	}
	if isDateValue(left) && isDateValue(right) {
		if left < right {
			return -1
		}
		return 1
	}
	if l, ok := numericValue(left); ok {
		if r, ok := numericValue(right); ok {
			if l < r {
				return -1
			}
			if l > r {
				return 1
			}
			return 0
		}
	}
	if left < right {
		return -1
	}
	return 1
}

func matchesCondition(row []string, columns []string, condition filterCondition) bool {
	if condition.Column == "" || condition.Operation == "" {
		return true
	}
	i := filterColumnIndex(columns, condition.Column)
	if i < 0 || i >= len(row) {
		return true
	}
	cmp := compareValues(row[i], condition.Value)
	switch condition.Operation {
	case "=":
		return cmp == 0
	case "!=":
		return cmp != 0
	case ">=":
		return cmp >= 0
	case "<=":
		return cmp <= 0
	}
	return true
}

func filterRows(rows [][]string, columns []string, conditions []filterCondition, skip int) [][]string {
	out := make([][]string, 0, len(rows))
	for _, row := range rows {
		ok := true
		for i, condition := range conditions {
			if i != skip && !matchesCondition(row, columns, condition) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, row)
		}
	}
	return out
}

func conditionValues(rows [][]string, columns []string, condition filterCondition) []string {
	i := filterColumnIndex(columns, condition.Column)
	values := map[string]bool{}
	if i >= 0 {
		for _, row := range rows {
			if i < len(row) {
				values[row[i]] = true
			}
		}
	}
	out := []string{""}
	for value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
