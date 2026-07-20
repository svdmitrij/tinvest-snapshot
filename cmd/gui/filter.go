//go:build !ci

package main

import (
	"strconv"
	"strings"
	"time"
)

type filterCondition struct{ Column, Operation, Value string }

type columnKind int

const (
	textColumn columnKind = iota
	numberColumn
	dateColumn
	boolColumn
)

func filterColumnIndex(columns []string, name string) int {
	for i, column := range columns {
		if column == name {
			return i
		}
	}
	return -1
}

func columnType(columns []string, name string) columnKind {
	// Table captions are localized, so known semantic columns are matched by
	// their translations at the grid boundary rather than guessed from values.
	lower := strings.ToLower(name)
	if strings.Contains(lower, "квал") || strings.Contains(lower, "qual") || strings.Contains(lower, "дивиденд") || strings.Contains(lower, "dividend") {
		return boolColumn
	}
	if strings.Contains(lower, "дата") || strings.Contains(lower, "date") || strings.Contains(lower, "время") || strings.Contains(lower, "time") || strings.Contains(lower, "погаш") || strings.Contains(lower, "maturity") {
		return dateColumn
	}
	for _, hint := range []string{"amount", "price", "value", "quantity", "rate", "yield", "p/l", "sum", "amount", "количество", "цена", "стоимость", "сумма", "ставка", "доходность", "номинал", "прибыль", "итого"} {
		if strings.Contains(lower, hint) {
			return numberColumn
		}
	}
	return textColumn
}

func numericValue(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	var b strings.Builder
	started := false
	for _, r := range value {
		if (r >= '0' && r <= '9') || r == '-' || r == '+' || r == ',' || r == '.' || r == ' ' || r == '\u00a0' {
			b.WriteRune(r)
			started = true
			continue
		}
		if started {
			break
		}
	}
	value = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(b.String(), " ", ""), "\u00a0", ""), ",", ".")
	if strings.Count(value, ".") > 1 {
		last := strings.LastIndex(value, ".")
		value = strings.ReplaceAll(value[:last], ".", "") + value[last:]
	}
	v, err := strconv.ParseFloat(value, 64)
	return v, err == nil
}

// compareValues follows table sorting: empty data is lower than any value,
// then dates and numbers compare by their actual value, otherwise by text.
func compareValues(left, right string, kind columnKind) int {
	if left == right {
		return 0
	}
	if left == "" {
		return -1
	}
	if right == "" {
		return 1
	}
	if kind == dateColumn {
		left, right = left[:min(10, len(left))], right[:min(10, len(right))]
		if l, e := time.Parse("2006-01-02", left); e == nil {
			if r, e := time.Parse("2006-01-02", right); e == nil {
				if l.Before(r) {
					return -1
				}
				if l.After(r) {
					return 1
				}
				return 0
			}
		}
	}
	if kind == numberColumn {
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
	}
	if left < right {
		return -1
	}
	return 1
}

func matchesCondition(row []string, columns []string, condition filterCondition) bool {
	if condition.Column == "" || condition.Operation == "" || condition.Value == "" {
		return true
	}
	i := filterColumnIndex(columns, condition.Column)
	if i < 0 || i >= len(row) {
		return true
	}
	cmp := compareValues(row[i], condition.Value, columnType(columns, condition.Column))
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

func legacyCompareValues(left, right string) int {
	if left < right {
		return -1
	}
	return 1
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
