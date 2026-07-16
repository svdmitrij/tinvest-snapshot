package tinvest

import "github.com/dmitry/tinvest-snapshot/internal/money"

// Wire types mirror the JSON of the T-Invest REST gateway (proto3-JSON,
// lowerCamelCase field names; int64 fields are encoded as strings).

type apiAccount struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	OpenedDate string `json:"openedDate"`
}

type getAccountsResponse struct {
	Accounts []apiAccount `json:"accounts"`
}

type portfolioRequest struct {
	AccountID string `json:"accountId"`
	Currency  string `json:"currency,omitempty"`
}

type portfolioPosition struct {
	Figi                 string          `json:"figi"`
	InstrumentType       string          `json:"instrumentType"`
	Quantity             money.Quotation `json:"quantity"`
	AveragePositionPrice money.Money     `json:"averagePositionPrice"`
	ExpectedYield        money.Quotation `json:"expectedYield"`
	CurrentPrice         money.Money     `json:"currentPrice"`
	CurrentNkd           money.Money     `json:"currentNkd"`
	InstrumentUID        string          `json:"instrumentUid"`
}

type portfolioResponse struct {
	TotalAmountPortfolio money.Money         `json:"totalAmountPortfolio"`
	Positions            []portfolioPosition `json:"positions"`
}

type instrumentShort struct {
	Figi           string `json:"figi"`
	Ticker         string `json:"ticker"`
	ISIN           string `json:"isin"`
	Name           string `json:"name"`
	Currency       string `json:"currency"`
	InstrumentType string `json:"instrumentType"`
	UID            string `json:"uid"`
}

type getInstrumentResponse struct {
	Instrument instrumentShort `json:"instrument"`
}

type instrumentRequest struct {
	IDType string `json:"idType"`
	ID     string `json:"id"`
}

type bond struct {
	Figi                  string      `json:"figi"`
	Name                  string      `json:"name"`
	CouponQuantityPerYear int32       `json:"couponQuantityPerYear"`
	Nominal               money.Money `json:"nominal"`
	MaturityDate          string      `json:"maturityDate"`
	RiskLevel             string      `json:"riskLevel"`
	Currency              string      `json:"currency"`
	FloatingCouponFlag    bool        `json:"floatingCouponFlag"`
	PerpetualFlag         bool        `json:"perpetualFlag"`
	AmortizationFlag      bool        `json:"amortizationFlag"`
}

type bondResponse struct {
	Instrument bond `json:"instrument"`
}

type couponsRequest struct {
	InstrumentID string `json:"instrumentId"`
	From         string `json:"from"`
	To           string `json:"to"`
}

type couponEvent struct {
	CouponDate string      `json:"couponDate"`
	PayOneBond money.Money `json:"payOneBond"`
	CouponType string      `json:"couponType"`
}

type couponsResponse struct {
	Events []couponEvent `json:"events"`
}

type dividendsRequest struct {
	InstrumentID string `json:"instrumentId"`
	From         string `json:"from"`
	To           string `json:"to"`
}

type dividend struct {
	DividendNet  money.Money `json:"dividendNet"`
	PaymentDate  string      `json:"paymentDate"`
	DeclaredDate string      `json:"declaredDate"`
	Regularity   string      `json:"regularity"`
}

type dividendsResponse struct {
	Dividends []dividend `json:"dividends"`
}

type currency struct {
	Figi    string      `json:"figi"`
	Ticker  string      `json:"ticker"`
	ISOCode string      `json:"isoCurrencyName"`
	Nominal money.Money `json:"nominal"`
	UID     string      `json:"uid"`
}

type currenciesResponse struct {
	Instruments []currency `json:"instruments"`
}

type apiInstrument struct {
	UID                   string      `json:"uid"`
	Figi                  string      `json:"figi"`
	Ticker                string      `json:"ticker"`
	Name                  string      `json:"name"`
	ISIN                  string      `json:"isin"`
	Currency              string      `json:"currency"`
	Exchange              string      `json:"exchange"`
	Sector                string      `json:"sector"`
	RiskLevel             string      `json:"riskLevel"`
	CouponQuantityPerYear int         `json:"couponQuantityPerYear"`
	FloatingCouponFlag    bool        `json:"floatingCouponFlag"`
	AmortizationFlag      bool        `json:"amortizationFlag"`
	Nominal               money.Money `json:"nominal"`
	MaturityDate          string      `json:"maturityDate"`
	NextCouponDate        string      `json:"nextCouponDate"`
}
type instrumentsResponse struct {
	Instruments []apiInstrument `json:"instruments"`
}

// bondEventsRequest must carry an explicit window: without from/to the API
// returns only the events of the nearest period, so a long amortization
// schedule comes back empty.
type bondEventsRequest struct {
	InstrumentID string `json:"instrumentId"`
	From         string `json:"from"`
	To           string `json:"to"`
}

// bondEvent mirrors GetBondEvents items. The API exposes no amortization event
// type: partial redemptions appear as several EVENT_TYPE_MTY events, each
// paying back a part of the nominal, while a plain bond has a single one.
// Calls (оферты) come as EVENT_TYPE_CALL.
type bondEvent struct {
	EventType  string      `json:"eventType"`
	EventDate  string      `json:"eventDate"`
	PayDate    string      `json:"payDate"`
	PayOneBond money.Money `json:"payOneBond"`
}

type bondEventsResponse struct {
	Events []bondEvent `json:"events"`
}

type lastPricesRequest struct {
	InstrumentID []string `json:"instrumentId"`
}

type lastPrice struct {
	Figi          string          `json:"figi"`
	Price         money.Quotation `json:"price"`
	InstrumentUID string          `json:"instrumentUid"`
}

type lastPricesResponse struct {
	LastPrices []lastPrice `json:"lastPrices"`
}

type operationsByCursorRequest struct {
	AccountID string `json:"accountId"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
	Limit     int32  `json:"limit,omitempty"`
}

// operationItem mirrors GetOperationsByCursor items. Type is the operation
// enum; Description is the API's human-readable sentence about the operation.
// Name is the *instrument* name, not an operation label.
type operationItem struct {
	ID             string      `json:"id"`
	Date           string      `json:"date"`
	Type           string      `json:"type"`
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	State          string      `json:"state"`
	InstrumentUID  string      `json:"instrumentUid"`
	Figi           string      `json:"figi"`
	InstrumentType string      `json:"instrumentType"`
	Payment        money.Money `json:"payment"`
	Quantity       string      `json:"quantity"`
}

type operationsByCursorResponse struct {
	HasNext    bool            `json:"hasNext"`
	NextCursor string          `json:"nextCursor"`
	Items      []operationItem `json:"items"`
}
