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

// operationItem mirrors GetOperationsByCursor items. Name is the API's
// human-readable operation label; Type is the raw enum used as a fallback.
type operationItem struct {
	ID             string      `json:"id"`
	Date           string      `json:"date"`
	Type           string      `json:"type"`
	Name           string      `json:"name"`
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
