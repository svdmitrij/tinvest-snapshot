//go:build !ci

package main

import "testing"

func TestBusyTextEmptyWhenNothingRunning(t *testing.T) {
	if got := busyText(nil); got != "" {
		t.Fatalf("busyText(nil) = %q, want empty", got)
	}
	if got := busyText(map[string]busyState{}); got != "" {
		t.Fatalf("busyText(empty) = %q, want empty", got)
	}
}

func TestBusyTextShowsPercentWhenTotalKnown(t *testing.T) {
	got := busyText(map[string]busyState{
		"portfolio":       {label: "Портфель", current: 3, total: 5},
		"instrument-card": {label: "Детализация облигации"},
	})
	want := "Детализация облигации; Портфель 60%"
	if got != want {
		t.Fatalf("busyText = %q, want %q", got, want)
	}
}

func TestBusyTextListsEveryActiveLoadInStableOrder(t *testing.T) {
	got := busyText(map[string]busyState{
		"portfolio":  {label: "Портфель", current: 1, total: 2},
		"operations": {label: "Операции", current: 2, total: 8},
	})
	want := "Операции 25%; Портфель 50%"
	if got != want {
		t.Fatalf("busyText = %q, want %q", got, want)
	}
}

func TestBusyTextRoundPercentDown(t *testing.T) {
	got := busyText(map[string]busyState{
		"instruments-refresh-all": {label: "Список инструментов", current: 1, total: 3},
	})
	if want := "Список инструментов 33%"; got != want {
		t.Fatalf("busyText = %q, want %q", got, want)
	}
}
