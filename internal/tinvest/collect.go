package tinvest

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dmitry/tinvest-snapshot/internal/bondyield"
	"github.com/dmitry/tinvest-snapshot/internal/model"
	"github.com/dmitry/tinvest-snapshot/internal/money"
)

// Collect builds a full portfolio snapshot. targetCurrency ("" to disable)
// adds converted totals. Per-instrument enrichment failures degrade to NA
// and never abort the run; only account/portfolio failures are fatal.
// ProgressFunc is called with the current account index and total count during collection.
type ProgressFunc func(current, total int)

func (c *Client) Collect(ctx context.Context, mode, targetCurrency string, now time.Time, onProgress ProgressFunc) (*model.Snapshot, error) {
	accounts, err := c.Accounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("получение списка счетов: %w", err)
	}

	snap := &model.Snapshot{
		GeneratedAt:    now.UTC().Format(time.RFC3339),
		Mode:           mode,
		TargetCurrency: targetCurrency,
	}

	accounts = activeInvestmentAccounts(accounts)
	collected := make([]*model.Account, len(accounts))
	errs := make(chan error, len(accounts))
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for i, account := range accounts {
		wg.Add(1)
		go func(i int, account apiAccount) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			acc, err := c.collectAccount(ctx, account, now)
			if err != nil {
				errs <- err
				return
			}
			collected[i] = acc
			if onProgress != nil {
				onProgress(i+1, len(accounts))
			}
		}(i, account)
	}
	wg.Wait()
	close(errs)
	if err := <-errs; err != nil {
		return nil, err
	}

	grand := map[string]float64{}
	for _, acc := range collected {
		grand[acc.Total.Currency] += floatOf(acc.Total.Amount)
		snap.Accounts = append(snap.Accounts, *acc)
	}

	for cur, amt := range grand {
		snap.GrandTotals = append(snap.GrandTotals, model.Total{Currency: cur, Amount: money.FromFloat(amt).String()})
	}

	if targetCurrency != "" {
		c.applyConversion(ctx, snap, targetCurrency)
	}
	return snap, nil
}

func activeInvestmentAccounts(accounts []apiAccount) []apiAccount {
	active := make([]apiAccount, 0, len(accounts))
	for _, account := range accounts {
		if account.Status == "ACCOUNT_STATUS_OPEN" && (account.Type == "ACCOUNT_TYPE_TINKOFF" || account.Type == "ACCOUNT_TYPE_TINKOFF_IIS") {
			active = append(active, account)
		}
	}
	return active
}

// CollectOperations fetches operations across all accounts within the period.
// globalFrom, when non-nil, is the single start bound for every account; when
// nil each account starts from its own opening date. The returned period holds
// the effective bounds (earliest start actually used, and to). Instrument
// enrichment failures degrade to empty fields and never abort the run; only
// account listing or operations retrieval failures are fatal.
func (c *Client) CollectOperations(ctx context.Context, globalFrom *time.Time, to time.Time, onProgress ProgressFunc) ([]model.Operation, model.OperationsPeriod, error) {
	accounts, err := c.Accounts(ctx)
	if err != nil {
		return nil, model.OperationsPeriod{}, fmt.Errorf("получение списка счетов: %w", err)
	}

	ops := []model.Operation{}
	instrCache := map[string]*instrumentShort{}
	effectiveFrom := to
	for i, a := range accounts {
		if onProgress != nil {
			onProgress(i+1, len(accounts))
		}
		from := to
		if globalFrom != nil {
			from = *globalFrom
		} else {
			from = accountStart(a, to)
		}
		if from.Before(effectiveFrom) {
			effectiveFrom = from
		}
		items, err := c.Operations(ctx, a.ID, from, to)
		if err != nil {
			return nil, model.OperationsPeriod{}, fmt.Errorf("операции счёта %s: %w", a.ID, err)
		}
		for _, it := range items {
			ops = append(ops, c.buildOperation(ctx, a, it, instrCache))
		}
	}

	period := model.OperationsPeriod{From: rfc3339(effectiveFrom), To: rfc3339(to)}
	return ops, period, nil
}

// accountStart returns the account opening date, or a 30-year floor when the
// API does not report one (so nothing is silently dropped).
func accountStart(a apiAccount, to time.Time) time.Time {
	if t, err := time.Parse(time.RFC3339, a.OpenedDate); err == nil {
		return t
	}
	return to.AddDate(-30, 0, 0)
}

func (c *Client) buildOperation(ctx context.Context, a apiAccount, it operationItem, cache map[string]*instrumentShort) model.Operation {
	op := model.Operation{
		ID:              it.ID,
		AccountID:       a.ID,
		AccountName:     a.Name,
		DateTime:        it.Date,
		Type:            operationTypeName(it),
		InstrumentType:  it.InstrumentType,
		Quantity:        it.Quantity,
		PaymentAmount:   it.Payment.String(),
		PaymentCurrency: it.Payment.Currency,
		State:           operationStateName(it.State),
	}
	if it.InstrumentUID != "" {
		instr, ok := cache[it.InstrumentUID]
		if !ok {
			if got, err := c.InstrumentByUIDCached(ctx, it.InstrumentUID); err == nil {
				instr = got
			} else {
				c.log("Не удалось получить справочные данные по инструменту %s: %v", it.InstrumentUID, err)
			}
			cache[it.InstrumentUID] = instr
		}
		if instr != nil {
			op.Ticker, op.ISIN, op.Name = instr.Ticker, instr.ISIN, instr.Name
		}
	}
	// When InstrumentByUID fails or InstrumentUID is absent, the API operation
	// item's own Name field holds the instrument name (per T-Invest API docs).
	// FIGI is a different identifier, not a ticker — it must not be substituted
	// into the ticker column; users looking up a ticker would be misled.
	if op.Name == "" && it.Name != "" {
		op.Name = it.Name
	}
	return op
}

// operationTypeName renders the operation type. The API's "name" field holds
// the *instrument* name, so it must not be used here; we map the type enum and
// fall back to the API description, then to the raw enum.
func operationTypeName(it operationItem) string {
	if label, ok := operationTypeLabels[it.Type]; ok {
		return label
	}
	if it.Description != "" {
		return it.Description
	}
	return it.Type
}

var operationTypeLabels = map[string]string{
	"OPERATION_TYPE_BUY":                     "Покупка ЦБ",
	"OPERATION_TYPE_BUY_CARD":                "Покупка ЦБ с карты",
	"OPERATION_TYPE_SELL":                    "Продажа ЦБ",
	"OPERATION_TYPE_INPUT":                   "Пополнение",
	"OPERATION_TYPE_OUTPUT":                  "Вывод средств",
	"OPERATION_TYPE_COUPON":                  "Выплата купона",
	"OPERATION_TYPE_DIVIDEND":                "Выплата дивидендов",
	"OPERATION_TYPE_DIVIDEND_TAX":            "Налог на дивиденды",
	"OPERATION_TYPE_TAX":                     "Налог",
	"OPERATION_TYPE_TAX_COUPON":              "Налог на купон",
	"OPERATION_TYPE_TAX_CORRECTION":          "Корректировка налога",
	"OPERATION_TYPE_BROKER_FEE":              "Комиссия брокера",
	"OPERATION_TYPE_SERVICE_FEE":             "Комиссия за обслуживание",
	"OPERATION_TYPE_MARGIN_FEE":              "Комиссия за маржинальную торговлю",
	"OPERATION_TYPE_SUCCESS_FEE":             "Комиссия за успех",
	"OPERATION_TYPE_BOND_REPAYMENT":          "Погашение облигации",
	"OPERATION_TYPE_BOND_REPAYMENT_FULL":     "Полное погашение облигации",
	"OPERATION_TYPE_BOND_TAX":                "Налог по облигации",
	"OPERATION_TYPE_ACCRUING_VARMARGIN":      "Начисление вариационной маржи",
	"OPERATION_TYPE_WRITING_OFF_VARMARGIN":   "Списание вариационной маржи",
	"OPERATION_TYPE_INPUT_SECURITIES":        "Зачисление ценных бумаг",
	"OPERATION_TYPE_OUTPUT_SECURITIES":       "Списание ценных бумаг",
	"OPERATION_TYPE_OVERNIGHT":               "Овернайт",
	"OPERATION_TYPE_DELIVERY_BUY":            "Покупка по поставке",
	"OPERATION_TYPE_DELIVERY_SELL":           "Продажа по поставке",
	"OPERATION_TYPE_TRACK_MFEE":              "Комиссия за управление",
	"OPERATION_TYPE_TRACK_PFEE":              "Комиссия за результат",
	"OPERATION_TYPE_CASH_FEE":                "Комиссия за вывод",
	"OPERATION_TYPE_OUT_FEE":                 "Комиссия за перевод",
	"OPERATION_TYPE_OUT_STAMP_DUTY":          "Гербовый сбор",
	"OPERATION_TYPE_TAX_REPO":                "Налог по РЕПО",
	"OPERATION_TYPE_TAX_PROGRESSIVE":         "Налог по прогрессивной ставке",
	"OPERATION_TYPE_DIVIDEND_TRANSFER":       "Перевод дивидендов",
	"OPERATION_TYPE_TAX_CORRECTION_COUPON":   "Корректировка налога по купону",
	"OPERATION_TYPE_BENEFIT_TAX":             "Налог с материальной выгоды",
	"OPERATION_TYPE_FEE_RETURN":              "Возврат комиссии",
	"OPERATION_TYPE_ASSET_RETURN":            "Возврат активов",
	"OPERATION_TYPE_ADVICE_FEE":              "Комиссия за консультацию",
	"OPERATION_TYPE_TRANS_IIS_BS":            "Перевод ценных бумаг с ИИС",
	"OPERATION_TYPE_TRANS_BS_BS":             "Перевод ценных бумаг между счетами",
	"OPERATION_TYPE_OUT_MULTI":               "Вывод по нескольким инструментам",
	"OPERATION_TYPE_INP_MULTI":               "Зачисление по нескольким инструментам",
	"OPERATION_TYPE_OVER_PLACEMENT":          "Размещение овернайт",
	"OPERATION_TYPE_OVER_COM":                "Комиссия за овернайт",
	"OPERATION_TYPE_OVER_INCOME":             "Доход по овернайт",
	"OPERATION_TYPE_OPTION_EXPIRATION":       "Экспирация опциона",
	"OPERATION_TYPE_FUTURE_EXPIRATION":       "Экспирация фьючерса",
	"OPERATION_TYPE_ACCRUING_VARMARGIN_HOLD": "Удержание вариационной маржи",
}

func operationStateName(state string) string {
	switch state {
	case "OPERATION_STATE_EXECUTED":
		return "исполнена"
	case "OPERATION_STATE_CANCELED":
		return "отменена"
	case "OPERATION_STATE_PROGRESS":
		return "в процессе"
	case "", "OPERATION_STATE_UNSPECIFIED":
		return model.NA
	default:
		return state
	}
}

func (c *Client) collectAccount(ctx context.Context, a apiAccount, now time.Time) (*model.Account, error) {
	pf, err := c.Portfolio(ctx, a.ID)
	if err != nil {
		return nil, fmt.Errorf("портфель счёта %s: %w", a.ID, err)
	}
	acc := &model.Account{
		ID:    a.ID,
		Name:  a.Name,
		Type:  a.Type,
		Total: model.Total{Currency: pf.TotalAmountPortfolio.Currency, Amount: pf.TotalAmountPortfolio.String()},
	}

	cash := map[string]money.Quotation{}
	var cashOrder []string
	// Collect non-currency positions for parallel enrichment.
	type posIdx struct {
		p   portfolioPosition
		idx int
	}
	var toEnrich []posIdx
	for _, p := range pf.Positions {
		if p.InstrumentType == "currency" {
			code := currencyCode(p)
			if _, seen := cash[code]; !seen {
				cashOrder = append(cashOrder, code)
			}
			cash[code] = cash[code].Add(p.Quantity)
			continue
		}
		toEnrich = append(toEnrich, posIdx{p: p, idx: len(toEnrich)})
	}

	// Enrich positions in parallel with a semaphore (max 6 concurrent).
	if len(toEnrich) > 0 {
		positions := make([]model.Position, len(toEnrich))
		sem := make(chan struct{}, 6)
		errs := make(chan error, len(toEnrich))
		var wg sync.WaitGroup
		for _, pi := range toEnrich {
			wg.Add(1)
			go func(pi posIdx) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				positions[pi.idx] = c.buildPosition(ctx, pi.p, now)
				if ctx.Err() != nil {
					select {
					case errs <- ctx.Err():
					default:
					}
				}
			}(pi)
		}
		wg.Wait()
		close(errs)
		if err := <-errs; err != nil {
			return nil, err
		}
		acc.Positions = positions
	}

	for _, code := range cashOrder {
		acc.Cash = append(acc.Cash, model.CashBalance{Currency: code, Amount: cash[code].String()})
	}
	return acc, nil
}

func (c *Client) buildPosition(ctx context.Context, p portfolioPosition, now time.Time) model.Position {
	pos := model.Position{
		InstrumentType: p.InstrumentType,
		Ticker:         model.NA,
		ISIN:           model.NA,
		Name:           model.NA,
		Currency:       p.CurrentPrice.Currency,
		Quantity:       p.Quantity.String(),
		AvgPrice:       p.AveragePositionPrice.String(),
		CurrentPrice:   p.CurrentPrice.String(),
	}

	qty := p.Quantity.Float()
	cur := p.CurrentPrice.Float()
	avg := p.AveragePositionPrice.Float()
	value := qty * cur
	cost := qty * avg
	pos.CurrentValue = money.FromFloat(value).String()
	pos.PnLAbs = money.FromFloat(value - cost).String()
	if cost != 0 {
		pos.PnLPct = strconv.FormatFloat(money.Round2((value-cost)/cost*100), 'f', 2, 64)
	} else {
		pos.PnLPct = model.NA
	}

	if instr, err := c.InstrumentByUIDCached(ctx, p.InstrumentUID); err == nil {
		if instr.Ticker != "" {
			pos.Ticker = instr.Ticker
		}
		if instr.ISIN != "" {
			pos.ISIN = instr.ISIN
		}
		if instr.Name != "" {
			pos.Name = instr.Name
		}
		if instr.Currency != "" {
			pos.Currency = instr.Currency
		}
	} else {
		c.log("Не удалось получить справочные данные по инструменту %s: %v", p.InstrumentUID, err)
	}

	switch p.InstrumentType {
	case "bond":
		pos.Bond = c.enrichBond(ctx, p, now)
	case "share":
		pos.Share = c.enrichShare(ctx, p, now)
	}
	return pos
}

func (c *Client) enrichBond(ctx context.Context, p portfolioPosition, now time.Time) *model.BondInfo {
	info := model.NewBondInfo()
	var nominal float64
	var perYear int32
	var maturity time.Time
	var hasMaturity, floating, perpetual, amortized bool
	if b, err := c.BondByUIDCached(ctx, p.InstrumentUID); err == nil {
		if b.CouponQuantityPerYear > 0 {
			perYear = b.CouponQuantityPerYear
			info.CouponFrequency = strconv.Itoa(int(perYear))
		}
		if b.RiskLevel != "" {
			info.IssuerRating = b.RiskLevel
		}
		nominal = b.Nominal.Float()
		floating, perpetual, amortized = b.FloatingCouponFlag, b.PerpetualFlag, b.AmortizationFlag
		if t, perr := time.Parse(time.RFC3339, b.MaturityDate); perr == nil {
			maturity, hasMaturity = t, true
		}
	} else {
		c.log("Не удалось получить данные облигации %s: %v", p.InstrumentUID, err)
	}

	// Narrow coupon window: 1 year back, 5 years forward (was 30 years).
	events, err := c.Coupons(ctx, p.InstrumentUID, now.AddDate(-1, 0, 0), now.AddDate(5, 0, 0))
	if err != nil {
		c.log("Не удалось получить купоны %s: %v", p.InstrumentUID, err)
		return info
	}
	if next, ok := nextCoupon(events, now); ok {
		info.NextCouponDate = formatDate(next.CouponDate)
		info.NextCouponAmount = next.PayOneBond.String()
		if nominal > 0 && perYear > 0 {
			rate := next.PayOneBond.Float() * float64(perYear) / nominal * 100
			info.CouponRatePct = strconv.FormatFloat(money.Round2(rate), 'f', 2, 64)
		}
	}

	cleanPrice := p.CurrentPrice.Float()

	// 6.1 Current yield = annual coupon income / current price.
	if coupon, ok := representativeCoupon(events, now); ok && perYear > 0 {
		annual := coupon.Float() * float64(perYear)
		if cy, ok := bondyield.CurrentYield(annual, cleanPrice); ok {
			info.CurrentYield = strconv.FormatFloat(money.Round2(cy), 'f', 2, 64)
		}
	}

	// 6.2 YTM: only for fixed-coupon, non-amortized, non-perpetual bonds.
	if !floating && !perpetual && !amortized && hasMaturity && nominal > 0 {
		flows := bondCashFlows(events, now, maturity, nominal)
		dirty := cleanPrice + p.CurrentNkd.Float()
		if y, ok := bondyield.YTM(dirty, flows); ok {
			info.YieldToMaturity = strconv.FormatFloat(money.Round2(y), 'f', 2, 64)
		}
	}
	return info
}

// bondCashFlows builds the future cash flows (per bond) for YTM: each future
// coupon at its date plus the nominal redemption at maturity.
func bondCashFlows(events []couponEvent, now, maturity time.Time, nominal float64) []bondyield.CashFlow {
	var flows []bondyield.CashFlow
	for _, e := range events {
		d, err := time.Parse(time.RFC3339, e.CouponDate)
		if err != nil || !d.After(now) {
			continue
		}
		flows = append(flows, bondyield.CashFlow{Years: yearsBetween(now, d), Amount: e.PayOneBond.Float()})
	}
	flows = append(flows, bondyield.CashFlow{Years: yearsBetween(now, maturity), Amount: nominal})
	return flows
}

func yearsBetween(from, to time.Time) float64 {
	return to.Sub(from).Hours() / 24 / 365
}

// representativeCoupon returns the next future coupon, or the most recent past
// one if none is scheduled ahead, as a stand-in for the periodic coupon amount.
func representativeCoupon(events []couponEvent, now time.Time) (money.Money, bool) {
	if next, ok := nextCoupon(events, now); ok {
		return next.PayOneBond, true
	}
	var last couponEvent
	found := false
	for _, e := range events {
		t, err := time.Parse(time.RFC3339, e.CouponDate)
		if err != nil || t.After(now) {
			continue
		}
		if !found || t.After(mustParse(last.CouponDate)) {
			last, found = e, true
		}
	}
	if !found {
		return money.Money{}, false
	}
	return last.PayOneBond, true
}

func (c *Client) enrichShare(ctx context.Context, p portfolioPosition, now time.Time) *model.ShareInfo {
	info := model.NewShareInfo()
	divs, err := c.Dividends(ctx, p.InstrumentUID, now.AddDate(-1, 0, 0), now.AddDate(1, 0, 0))
	if err != nil {
		c.log("Не удалось получить дивиденды %s: %v", p.InstrumentUID, err)
		return info
	}
	var last, next *dividend
	count12m := 0
	for i := range divs {
		d := &divs[i]
		pd, perr := time.Parse(time.RFC3339, d.PaymentDate)
		if perr != nil {
			continue
		}
		if pd.After(now) {
			if next == nil || pd.Before(mustParse(next.PaymentDate)) {
				next = d
			}
		} else {
			if last == nil || pd.After(mustParse(last.PaymentDate)) {
				last = d
			}
			if pd.After(now.AddDate(-1, 0, 0)) {
				count12m++
			}
		}
	}
	if last != nil {
		info.LastDividendAmount = last.DividendNet.String()
	}
	if count12m > 0 {
		info.Frequency = strconv.Itoa(count12m)
	}
	if next != nil {
		info.NextPaymentDate = formatDate(next.PaymentDate)
		info.NextPaymentAmount = next.DividendNet.String()
	}
	return info
}

func nextCoupon(events []couponEvent, now time.Time) (couponEvent, bool) {
	var best couponEvent
	found := false
	for _, e := range events {
		t, err := time.Parse(time.RFC3339, e.CouponDate)
		if err != nil || !t.After(now) {
			continue
		}
		if !found || t.Before(mustParse(best.CouponDate)) {
			best, found = e, true
		}
	}
	return best, found
}

func currencyCode(p portfolioPosition) string {
	if p.CurrentPrice.Currency != "" {
		return p.CurrentPrice.Currency
	}
	if p.AveragePositionPrice.Currency != "" {
		return p.AveragePositionPrice.Currency
	}
	return model.NA
}

func formatDate(rfc string) string {
	t, err := time.Parse(time.RFC3339, rfc)
	if err != nil {
		return model.NA
	}
	return t.Format("2006-01-02")
}

func mustParse(rfc string) time.Time {
	t, _ := time.Parse(time.RFC3339, rfc)
	return t
}

func floatOf(decimal string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(decimal), 64)
	if err != nil {
		return 0
	}
	return v
}
