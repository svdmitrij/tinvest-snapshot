package tinvest

import (
	"strings"
	"testing"
	"time"
)

func TestRedemptionScheduleSplitsAmortizationAndOffers(t *testing.T) {
	events := []bondEvent{
		{EventType: "EVENT_TYPE_CPN", PayDate: "2026-01-15T00:00:00Z"},
		{EventType: "EVENT_TYPE_MTY", PayDate: "2026-12-22T00:00:00Z"},
		{EventType: "EVENT_TYPE_MTY", PayDate: "2026-06-25T00:00:00Z"},
		{EventType: "EVENT_TYPE_CALL", PayDate: "2027-01-23T00:00:00Z"},
	}
	amort, offers := redemptionSchedule(events)
	if strings.Join(amort, ",") != "2026-06-25,2026-12-22" {
		t.Errorf("amortization = %v, want both MTY dates sorted", amort)
	}
	if strings.Join(offers, ",") != "2027-01-23" {
		t.Errorf("offers = %v, want the CALL date", offers)
	}
}

func TestEffectiveMaturityUsesNearestFutureOffer(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	got := effectiveMaturity("2030-12-13", []string{"2026-01-01", "2027-03-10", "2026-09-11"}, now)
	if got != "2026-09-11" {
		t.Fatalf("effective maturity = %q, want nearest future offer", got)
	}
	got = effectiveMaturity("2030-12-13", []string{"2026-01-01"}, now)
	if got != "2030-12-13" {
		t.Fatalf("effective maturity without future offers = %q", got)
	}
}

// A plain bond repays its nominal once: that single MTY event is the maturity,
// not an amortization schedule.
func TestRedemptionScheduleIgnoresSingleRedemption(t *testing.T) {
	amort, offers := redemptionSchedule([]bondEvent{
		{EventType: "EVENT_TYPE_MTY", PayDate: "2027-04-11T00:00:00Z"},
		{EventType: "EVENT_TYPE_CPN", PayDate: "2026-04-11T00:00:00Z"},
	})
	if len(amort) != 0 {
		t.Errorf("amortization = %v, want none for a single redemption", amort)
	}
	if len(offers) != 0 {
		t.Errorf("offers = %v, want none", offers)
	}
}

func TestOperationTypeNameNeverUsesInstrumentName(t *testing.T) {
	got := operationTypeName(operationItem{
		Type:        "OPERATION_TYPE_COUPON",
		Name:        "СЕЛЛ-Сервис БО-01",
		Description: "Выплата купонов СЕЛЛ-Сервис БО-01",
	})
	if got != "Выплата купона" {
		t.Errorf("operationTypeName = %q, want the mapped operation label", got)
	}
}

func TestOperationTypeNameFallsBackToDescription(t *testing.T) {
	got := operationTypeName(operationItem{
		Type:        "OPERATION_TYPE_SOMETHING_NEW",
		Name:        "Инструмент",
		Description: "Неизвестная операция",
	})
	if got != "Неизвестная операция" {
		t.Errorf("operationTypeName = %q, want the API description", got)
	}
}
