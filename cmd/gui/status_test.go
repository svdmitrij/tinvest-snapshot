//go:build !ci

package main

import "testing"

const testHeader = "Сейчас обновляется"

func TestBusyTextEmptyWhenNothingRunning(t *testing.T) {
	if got := busyText(testHeader, nil); got != "" {
		t.Fatalf("busyText(nil) = %q, want empty", got)
	}
	if got := busyText(testHeader, map[string]busyState{}); got != "" {
		t.Fatalf("busyText(empty) = %q, want empty", got)
	}
}

func TestBusyTextShowsPrefixAndPercentWhenTotalKnown(t *testing.T) {
	got := busyText(testHeader, map[string]busyState{
		"portfolio":       {"Портфель", 3, 5},
		"instrument-card": {"Детализация облигации", 0, 0},
	})
	want := testHeader + ": Детализация облигации; Портфель 60%"
	if got != want {
		t.Fatalf("busyText = %q, want %q", got, want)
	}
}

func TestBusyTextListsEveryActiveLoadInStableOrder(t *testing.T) {
	got := busyText(testHeader, map[string]busyState{
		"portfolio":  {"Портфель", 1, 2},
		"operations": {"Операции", 2, 8},
	})
	want := testHeader + ": Операции 25%; Портфель 50%"
	if got != want {
		t.Fatalf("busyText = %q, want %q", got, want)
	}
}

func TestBusyTextRoundPercentDown(t *testing.T) {
	got := busyText(testHeader, map[string]busyState{
		"instruments-refresh-all": {"Список инструментов", 1, 3},
	})
	want := testHeader + ": Список инструментов 33%"
	if got != want {
		t.Fatalf("busyText = %q, want %q", got, want)
	}
}
