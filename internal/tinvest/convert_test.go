package tinvest

import (
	"math"
	"strconv"
	"testing"

	"github.com/dmitry/tinvest-snapshot/internal/model"
)

func TestConvertRateKeepsPrecision(t *testing.T) {
	cv := &converter{rubPerUnit: map[string]float64{"usd": 76.3}}
	total := model.Total{Currency: "rub", Amount: "2090032.4"}

	conv, ok := convert(cv, total, "usd")
	if !ok {
		t.Fatal("expected conversion to succeed")
	}
	if conv.Rate == "0.01" {
		t.Fatalf("rate was rounded to 2 decimals: %q", conv.Rate)
	}

	rate, err := strconv.ParseFloat(conv.Rate, 64)
	if err != nil {
		t.Fatalf("rate not parseable: %q", conv.Rate)
	}
	amount, _ := strconv.ParseFloat(conv.Amount, 64)
	// total * rate must reconstruct the converted amount within rounding.
	if math.Abs(2090032.4*rate-amount) > 0.5 {
		t.Errorf("rate %v inconsistent with amount %v", rate, amount)
	}
}

func TestGrandConvertedReportsRateForSingleCurrency(t *testing.T) {
	cv := &converter{rubPerUnit: map[string]float64{"usd": 80}}
	grand := grandConverted(cv, []model.Total{{Currency: "rub", Amount: "8000"}}, "usd")
	if grand == nil {
		t.Fatal("expected a converted grand total")
	}
	if grand.Amount != "100" {
		t.Errorf("amount = %q, want 100", grand.Amount)
	}
	rate, err := strconv.ParseFloat(grand.Rate, 64)
	if err != nil {
		t.Fatalf("rate not reported for a single-currency total: %q", grand.Rate)
	}
	if math.Abs(rate-0.0125) > 1e-9 {
		t.Errorf("rate = %v, want 1/80", rate)
	}
}

// Several source currencies have no single exchange rate, so the field is left
// out rather than filled with one of them.
func TestGrandConvertedOmitsRateForMixedCurrencies(t *testing.T) {
	cv := &converter{rubPerUnit: map[string]float64{"usd": 80, "eur": 90}}
	grand := grandConverted(cv, []model.Total{
		{Currency: "rub", Amount: "8000"},
		{Currency: "eur", Amount: "10"},
	}, "usd")
	if grand == nil {
		t.Fatal("expected a converted grand total")
	}
	if grand.Rate != "" {
		t.Errorf("rate = %q, want it omitted for a mixed-currency total", grand.Rate)
	}
	if grand.Amount != "111.25" {
		t.Errorf("amount = %q, want 8000 rub + 10 eur in usd", grand.Amount)
	}
}

func TestConverterRateViaRubPivot(t *testing.T) {
	cv := &converter{rubPerUnit: map[string]float64{"usd": 80, "eur": 90}}
	// 1 EUR should buy 90/80 = 1.125 USD.
	r, ok := cv.Rate("eur", "usd")
	if !ok || math.Abs(r-1.125) > 1e-9 {
		t.Errorf("Rate(eur,usd) = %v, ok=%v, want 1.125", r, ok)
	}
	// rub -> rub is identity.
	if r, ok := cv.Rate("rub", "rub"); !ok || r != 1 {
		t.Errorf("Rate(rub,rub) = %v, want 1", r)
	}
}
