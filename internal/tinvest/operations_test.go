package tinvest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// operationsMock serves account listing, two-page cursor operations and
// instrument enrichment so CollectOperations can be exercised offline.
func operationsMock(t *testing.T) *httptest.Server {
	t.Helper()
	// "name" carries the *instrument* name and "description" a sentence about
	// the operation, exactly as the production API returns them.
	page1 := `{"hasNext":true,"nextCursor":"c2","items":[
		{"id":"op1","date":"2026-03-15T12:00:00Z","type":"OPERATION_TYPE_BUY","name":"ОФЗ 26240",
		 "description":"Покупка 10 облигаций ОФЗ 26240",
		 "state":"OPERATION_STATE_EXECUTED","instrumentUid":"bond-uid","figi":"BBG00","instrumentType":"bond",
		 "payment":{"currency":"rub","units":"-9000","nano":0},"quantity":"10"}
	]}`
	page2 := `{"hasNext":false,"nextCursor":"","items":[
		{"id":"op2","date":"2026-03-20T09:00:00Z","type":"OPERATION_TYPE_INPUT","name":"",
		 "description":"Пополнение брокерского счёта",
		 "state":"OPERATION_STATE_EXECUTED","instrumentUid":"","payment":{"currency":"rub","units":"5000","nano":0},"quantity":"0"}
	]}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		method := parts[len(parts)-1]
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		switch method {
		case "GetSandboxAccounts":
			w.Write([]byte(`{"accounts":[{"id":"acc1","type":"ACCOUNT_TYPE_TINKOFF","name":"Брокерский","status":"ACCOUNT_STATUS_OPEN","openedDate":"2020-01-01T00:00:00Z"}]}`))
		case "GetOperationsByCursor":
			if strings.Contains(string(body), `"cursor":"c2"`) {
				w.Write([]byte(page2))
			} else {
				w.Write([]byte(page1))
			}
		case "GetInstrumentBy":
			w.Write([]byte(`{"instrument":{"ticker":"SU26240","isin":"RU000A101","name":"ОФЗ 26240","uid":"bond-uid"}}`))
		default:
			http.Error(w, "no mock for "+method, http.StatusNotFound)
		}
	}))
}

func TestCollectOperationsPaginatesAndMaps(t *testing.T) {
	srv := operationsMock(t)
	defer srv.Close()

	c := New(srv.URL, "test-token", "test", 1, time.Millisecond, nil)
	c.Sandbox = true

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	ops, period, err := c.CollectOperations(context.Background(), &from, to, nil)
	if err != nil {
		t.Fatalf("CollectOperations: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("operations = %d, want 2 (both cursor pages)", len(ops))
	}

	op1 := ops[0]
	if op1.ID != "op1" || op1.Type != "Покупка ЦБ" || op1.State != "исполнена" {
		t.Errorf("op1 mapping = %+v", op1)
	}
	// Regression: the type column must not repeat the instrument name.
	if op1.Type == op1.Name {
		t.Errorf("op1 type shows the instrument name %q instead of the operation type", op1.Type)
	}
	if op1.Ticker != "SU26240" || op1.ISIN != "RU000A101" || op1.Name != "ОФЗ 26240" {
		t.Errorf("op1 instrument enrichment = %+v", op1)
	}
	if op1.PaymentAmount != "-9000" || op1.PaymentCurrency != "rub" || op1.AccountName != "Брокерский" {
		t.Errorf("op1 payment/account = %+v", op1)
	}

	op2 := ops[1]
	if op2.ID != "op2" || op2.Type != "Пополнение" {
		t.Errorf("op2 mapping = %+v", op2)
	}
	// Cash flow is not tied to an instrument: instrument fields stay empty.
	if op2.Ticker != "" || op2.ISIN != "" || op2.Name != "" {
		t.Errorf("op2 instrument fields not empty: %+v", op2)
	}

	if period.From != "2026-01-01T00:00:00Z" || period.To != "2026-07-09T12:00:00Z" {
		t.Errorf("period = %+v", period)
	}
}

func TestCollectOperationsPerAccountOpenedDate(t *testing.T) {
	srv := operationsMock(t)
	defer srv.Close()

	c := New(srv.URL, "test-token", "test", 1, time.Millisecond, nil)
	c.Sandbox = true

	to := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	_, period, err := c.CollectOperations(context.Background(), nil, to, nil)
	if err != nil {
		t.Fatalf("CollectOperations: %v", err)
	}
	// With no global from, the effective start is the account opening date.
	if period.From != "2020-01-01T00:00:00Z" {
		t.Errorf("period.From = %q, want account opened date", period.From)
	}
}
