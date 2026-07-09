package money

import (
	"encoding/json"
	"testing"
)

func TestQuotationString(t *testing.T) {
	cases := []struct {
		units int64
		nano  int32
		want  string
	}{
		{123, 0, "123"},
		{123, 456789000, "123.456789"},
		{0, 500000000, "0.5"},
		{-1, -250000000, "-1.25"},
		{1000000, 10, "1000000.00000001"},
	}
	for _, c := range cases {
		got := Quotation{Units: c.units, Nano: c.nano}.String()
		if got != c.want {
			t.Errorf("Quotation{%d,%d}.String() = %q, want %q", c.units, c.nano, got, c.want)
		}
	}
}

func TestQuotationFloat(t *testing.T) {
	q := Quotation{Units: 100, Nano: 250000000}
	if got := q.Float(); got != 100.25 {
		t.Errorf("Float() = %v, want 100.25", got)
	}
}

func TestMoneyUnmarshalUnitsAsString(t *testing.T) {
	// T-Invest serialises int64 units as a JSON string.
	var m Money
	if err := json.Unmarshal([]byte(`{"currency":"rub","units":"1500","nano":250000000}`), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Units != 1500 || m.Nano != 250000000 || m.Currency != "rub" {
		t.Fatalf("got %+v", m)
	}
	if m.String() != "1500.25" {
		t.Errorf("String() = %q, want 1500.25", m.String())
	}
}

func TestQuotationAddExact(t *testing.T) {
	// 1000 + 500.25 + 0.97 + 3 must be exact, no float drift.
	sum := Quotation{Units: 1000}.
		Add(Quotation{Units: 500, Nano: 250000000}).
		Add(Quotation{Units: 0, Nano: 970000000}).
		Add(Quotation{Units: 3})
	if got := sum.String(); got != "1504.22" {
		t.Errorf("Add chain = %q, want 1504.22", got)
	}
}

func TestFromFloatRoundTrip(t *testing.T) {
	q := FromFloat(42.75)
	if q.Units != 42 || q.Nano != 750000000 {
		t.Fatalf("FromFloat(42.75) = %+v", q)
	}
}
