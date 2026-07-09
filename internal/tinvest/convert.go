package tinvest

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/dmitry/tinvest-snapshot/internal/model"
	"github.com/dmitry/tinvest-snapshot/internal/money"
)

// converter holds the RUB value of one unit of each currency at snapshot time.
type converter struct {
	rubPerUnit map[string]float64 // lowercase currency code -> RUB per 1 unit
}

// Rate returns how many units of "to" one unit of "from" buys, via RUB pivot.
func (cv *converter) Rate(from, to string) (float64, bool) {
	from, to = strings.ToLower(from), strings.ToLower(to)
	rf, ok1 := cv.rubValue(from)
	rt, ok2 := cv.rubValue(to)
	if !ok1 || !ok2 || rt == 0 {
		return 0, false
	}
	return rf / rt, true
}

func (cv *converter) rubValue(cur string) (float64, bool) {
	if cur == "rub" {
		return 1, true
	}
	v, ok := cv.rubPerUnit[cur]
	return v, ok
}

func (c *Client) buildConverter(ctx context.Context) (*converter, error) {
	currencies, err := c.Currencies(ctx)
	if err != nil {
		return nil, fmt.Errorf("список валют: %w", err)
	}
	uids := make([]string, 0, len(currencies))
	nominal := map[string]float64{} // uid -> nominal units
	iso := map[string]string{}      // uid -> iso code
	for _, cur := range currencies {
		if cur.UID == "" {
			continue
		}
		uids = append(uids, cur.UID)
		n := cur.Nominal.Float()
		if n == 0 {
			n = 1
		}
		nominal[cur.UID] = n
		iso[cur.UID] = strings.ToLower(cur.ISOCode)
	}
	prices, err := c.LastPrices(ctx, uids)
	if err != nil {
		return nil, fmt.Errorf("курсы валют: %w", err)
	}
	cv := &converter{rubPerUnit: map[string]float64{}}
	for _, p := range prices {
		code := iso[p.InstrumentUID]
		if code == "" {
			continue
		}
		n := nominal[p.InstrumentUID]
		cv.rubPerUnit[code] = p.Price.Float() / n
	}
	return cv, nil
}

// applyConversion fills the *Converted fields. It degrades gracefully: a
// missing rate yields no converted value rather than aborting the snapshot.
func (c *Client) applyConversion(ctx context.Context, snap *model.Snapshot, target string) {
	cv, err := c.buildConverter(ctx)
	if err != nil {
		c.log("Пересчёт в %s недоступен: %v", strings.ToUpper(target), err)
		return
	}
	for i := range snap.Accounts {
		acc := &snap.Accounts[i]
		if conv, ok := convert(cv, acc.Total, target); ok {
			acc.TotalConverted = conv
		}
	}
	var sum float64
	all := true
	for _, t := range snap.GrandTotals {
		rate, ok := cv.Rate(t.Currency, target)
		if !ok {
			all = false
			continue
		}
		sum += floatOf(t.Amount) * rate
	}
	if all || sum != 0 {
		snap.GrandConverted = &model.Converted{
			Currency: strings.ToLower(target),
			Amount:   money.FromFloat(money.Round2(sum)).String(),
		}
	}
}

func convert(cv *converter, t model.Total, target string) (*model.Converted, bool) {
	rate, ok := cv.Rate(t.Currency, target)
	if !ok {
		return nil, false
	}
	return &model.Converted{
		Currency: strings.ToLower(target),
		Amount:   money.FromFloat(money.Round2(floatOf(t.Amount) * rate)).String(),
		Rate:     strconv.FormatFloat(money.Round2(rate), 'f', -1, 64),
	}, true
}
