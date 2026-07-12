package main

import (
	"context"
	"embed"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/dmitry/tinvest-snapshot/internal/catalog"
	"github.com/dmitry/tinvest-snapshot/internal/config"
	"github.com/dmitry/tinvest-snapshot/internal/model"
	"github.com/dmitry/tinvest-snapshot/internal/period"
	"github.com/dmitry/tinvest-snapshot/internal/report"
	"github.com/dmitry/tinvest-snapshot/internal/tinvest"
	"github.com/xuri/excelize/v2"
)

//go:embed i18n/*.json
var translations embed.FS

type desktop struct {
	mu                                 sync.RWMutex
	window                             fyne.Window
	configPath, cachePath              string
	cfg                                *config.Config
	snapshot                           *model.Snapshot
	cache                              *catalog.Cache
	text                               map[string]string
	portfolio, operations, instruments *grid
	from, to                           *widget.Entry
	status                             *widget.Label
}

type grid struct {
	mu           sync.RWMutex
	window       fyne.Window
	columns      []string
	all, visible [][]string
	table        *widget.Table
	header, root *fyne.Container
	search       *widget.Entry
	group        *widget.Select
	sortColumn   int
	desc         bool
	collapsed    map[string]bool
	onRow        func([]string)
}

func main() {
	configPath := flag.String("config", "config.json", "path to configuration")
	flag.Parse()
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	cacheRoot, _ := os.UserCacheDir()
	a := app.NewWithID("ru.dmitry.tinvest-snapshot")
	w := a.NewWindow("T-Invest")
	d := &desktop{window: w, configPath: *configPath, cachePath: filepath.Join(cacheRoot, "tinvest-snapshot", "catalog.json"), cfg: cfg}
	d.cache, _ = catalog.Load(d.cachePath)
	d.loadText()
	d.build()
	w.Resize(fyne.NewSize(1280, 800))
	w.ShowAndRun()
}

func (d *desktop) loadText() {
	d.text = map[string]string{}
	b, e := translations.ReadFile("i18n/" + d.cfg.Language + ".json")
	if e != nil {
		b, _ = translations.ReadFile("i18n/ru.json")
	}
	_ = json.Unmarshal(b, &d.text)
}
func (d *desktop) tr(k string) string {
	if s := d.text[k]; s != "" {
		return s
	}
	return k
}

func (d *desktop) build() {
	d.portfolio = newGrid(d.window)
	d.operations = newGrid(d.window)
	d.instruments = newGrid(d.window)
	d.from = widget.NewEntry()
	d.from.SetPlaceHolder("YYYY-MM-DD")
	d.to = widget.NewEntry()
	d.to.SetPlaceHolder("YYYY-MM-DD")
	d.status = widget.NewLabel("")
	refresh := widget.NewButton(d.tr("refresh"), func() { d.refreshPortfolio() })
	exportAll := widget.NewButton("JSON / CSV / XLSX", func() { d.exportAll() })
	portfolioBar := container.NewHBox(widget.NewLabel(d.tr("from")), d.from, widget.NewLabel(d.tr("to")), d.to, refresh, exportAll, widget.NewButton(d.tr("export"), func() { d.portfolio.exportView(d.cfg.ReportsDir) }))
	operationsBar := container.NewHBox(widget.NewLabel(d.tr("from")), d.from, widget.NewLabel(d.tr("to")), d.to, refresh, widget.NewButton(d.tr("export"), func() { d.operations.exportView(d.cfg.ReportsDir) }))
	searchTab := d.instrumentTab()
	tabs := container.NewAppTabs(container.NewTabItem(d.tr("portfolio"), container.NewBorder(portfolioBar, nil, nil, nil, d.portfolio.root)), container.NewTabItem(d.tr("operations"), container.NewBorder(operationsBar, nil, nil, nil, d.operations.root)), container.NewTabItem(d.tr("instruments"), searchTab), container.NewTabItem(d.tr("settings"), d.settingsTab()))
	d.window.SetContent(container.NewBorder(nil, d.status, nil, nil, tabs))
	if d.cfg.AutoRefreshMinutes > 0 {
		go func(period time.Duration) {
			ticker := time.NewTicker(period)
			defer ticker.Stop()
			for range ticker.C {
				d.refreshPortfolio()
			}
		}(time.Duration(d.cfg.AutoRefreshMinutes) * time.Minute)
	}
}

func newGrid(w fyne.Window) *grid {
	g := &grid{window: w, sortColumn: -1, collapsed: map[string]bool{}}
	g.search = widget.NewEntry()
	g.search.SetPlaceHolder("Поиск")
	g.group = widget.NewSelect([]string{}, func(string) { g.apply() })
	g.table = widget.NewTable(func() (int, int) { g.mu.RLock(); defer g.mu.RUnlock(); return len(g.visible), len(g.columns) }, func() fyne.CanvasObject {
		label := widget.NewLabel("")
		label.Truncation = fyne.TextTruncateEllipsis
		return label
	}, func(id widget.TableCellID, o fyne.CanvasObject) {
		g.mu.RLock()
		defer g.mu.RUnlock()
		if id.Row < len(g.visible) && id.Col < len(g.visible[id.Row]) {
			o.(*widget.Label).SetText(g.visible[id.Row][id.Col])
		}
	})
	g.table.ShowHeaderRow = true
	g.table.CreateHeader = func() fyne.CanvasObject { return widget.NewButton("", nil) }
	g.table.UpdateHeader = func(id widget.TableCellID, o fyne.CanvasObject) {
		if id.Row != -1 || id.Col < 0 || id.Col >= len(g.columns) {
			return
		}
		col := id.Col
		b := o.(*widget.Button)
		name := g.columns[col]
		if g.sortColumn == col {
			if g.desc {
				name += " ▼"
			} else {
				name += " ▲"
			}
		}
		b.SetText(name)
		b.OnTapped = func() {
			if g.sortColumn == col {
				g.desc = !g.desc
			} else {
				g.sortColumn, g.desc = col, false
			}
			g.apply()
		}
	}
	g.table.OnSelected = func(id widget.TableCellID) {
		g.mu.Lock()
		var selected []string
		if id.Row >= 0 && id.Row < len(g.visible) && len(g.visible[id.Row]) > 0 {
			value := g.visible[id.Row][0]
			if strings.HasPrefix(value, "▾ ") || strings.HasPrefix(value, "▸ ") {
				key := strings.TrimPrefix(strings.TrimPrefix(value, "▾ "), "▸ ")
				g.collapsed[key] = !g.collapsed[key]
			} else {
				selected = append([]string(nil), g.visible[id.Row]...)
			}
		}
		callback := g.onRow
		g.mu.Unlock()
		g.table.Unselect(id)
		if callback != nil && selected != nil {
			callback(selected)
		}
		g.apply()
	}
	g.header = container.NewHBox()
	g.search.OnChanged = func(string) { g.apply() }
	g.root = container.NewBorder(container.NewHBox(widget.NewLabel("Поиск:"), g.search, widget.NewLabel("Группа:"), g.group), nil, nil, nil, g.table)
	return g
}
func (g *grid) set(columns []string, rows [][]string) {
	g.mu.Lock()
	g.columns = columns
	g.all = rows
	g.mu.Unlock()
	g.group.Options = append([]string{""}, columns...)
	for i, c := range columns {
		width := float32(140)
		if len(c) > 16 {
			width = 190
		}
		g.table.SetColumnWidth(i, width)
	}
	g.apply()
}
func (g *grid) apply() {
	g.mu.Lock()
	q := strings.ToLower(strings.TrimSpace(g.search.Text))
	rows := make([][]string, 0, len(g.all))
	for _, r := range g.all {
		if q == "" || strings.Contains(strings.ToLower(strings.Join(r, "\x00")), q) {
			rows = append(rows, append([]string(nil), r...))
		}
	}
	col := g.sortColumn
	if name := g.group.Selected; name != "" {
		for i, c := range g.columns {
			if c == name {
				col = i
				break
			}
		}
	}
	if col >= 0 {
		sort.SliceStable(rows, func(i, j int) bool {
			less := rows[i][col] < rows[j][col]
			if g.desc {
				return !less
			}
			return less
		})
	}
	if groupName := g.group.Selected; groupName != "" {
		groupCol := -1
		for i, c := range g.columns {
			if c == groupName {
				groupCol = i
				break
			}
		}
		if groupCol >= 0 {
			grouped := make([][]string, 0, len(rows))
			last := "\x00"
			for _, r := range rows {
				key := groupName + ": " + r[groupCol]
				if key != last {
					last = key
					head := make([]string, len(g.columns))
					if g.collapsed[key] {
						head[0] = "▸ " + key
					} else {
						head[0] = "▾ " + key
					}
					grouped = append(grouped, head)
				}
				if !g.collapsed[key] {
					grouped = append(grouped, r)
				}
			}
			rows = grouped
		}
	}
	g.visible = rows
	g.mu.Unlock()
	g.table.Refresh()
}
func (g *grid) exportView(dir string) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if e := os.MkdirAll(dir, 0755); e != nil {
		dialog.ShowError(e, g.window)
		return
	}
	base := filepath.Join(dir, "table_"+time.Now().Format("20060102_150405"))
	csvPath, xlsxPath := base+".csv", base+".xlsx"
	f, e := os.Create(csvPath)
	if e != nil {
		dialog.ShowError(e, g.window)
		return
	}
	w := csv.NewWriter(f)
	_ = w.Write(g.columns)
	_ = w.WriteAll(g.visible)
	w.Flush()
	e = w.Error()
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		dialog.ShowError(e, g.window)
		return
	}
	x := excelize.NewFile()
	defer x.Close()
	sheet := "Таблица"
	_ = x.SetSheetName("Sheet1", sheet)
	rows := append([][]string{g.columns}, g.visible...)
	for ri, row := range rows {
		for ci, value := range row {
			cell, _ := excelize.CoordinatesToCellName(ci+1, ri+1)
			_ = x.SetCellStr(sheet, cell, value)
		}
	}
	if e = x.SaveAs(xlsxPath); e != nil {
		dialog.ShowError(e, g.window)
		return
	}
	dialog.ShowInformation("Экспорт", csvPath+"\n"+xlsxPath, g.window)
}

func (d *desktop) client() (*tinvest.Client, error) {
	token, e := d.cfg.ResolveToken()
	if e != nil {
		return nil, e
	}
	c := tinvest.New(d.cfg.BaseURL(), token, d.cfg.AppName, d.cfg.Retries, time.Duration(d.cfg.RetryDelayMs)*time.Millisecond, nil)
	c.Sandbox = d.cfg.Sandbox()
	return c, nil
}
func (d *desktop) busy(label string, fn func() error) {
	fyne.Do(func() { d.status.SetText(label) })
	go func() {
		e := fn()
		fyne.Do(func() {
			d.status.SetText("")
			if e != nil {
				dialog.ShowError(e, d.window)
			}
		})
	}()
}
func (d *desktop) refreshPortfolio() {
	d.busy(d.tr("loading"), func() error {
		now := time.Now()
		win, e := period.Resolve(d.from.Text, d.to.Text, d.cfg.ReportsDir, now)
		if e != nil {
			return e
		}
		c, e := d.client()
		if e != nil {
			return e
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		snap, e := c.Collect(ctx, d.cfg.Mode, d.cfg.TargetCurrency, now)
		if e != nil {
			return e
		}
		ops, p, e := c.CollectOperations(ctx, win.GlobalFrom, win.To)
		if e != nil {
			return e
		}
		snap.Operations = ops
		snap.OperationsPeriod = &p
		d.mu.Lock()
		d.snapshot = snap
		d.mu.Unlock()
		pc, pr := portfolioRows(snap)
		oc, or := operationRows(snap)
		fyne.Do(func() { d.portfolio.set(pc, pr); d.operations.set(oc, or) })
		return nil
	})
}
func (d *desktop) exportAll() {
	d.mu.RLock()
	snap := d.snapshot
	d.mu.RUnlock()
	if snap == nil {
		dialog.ShowError(fmt.Errorf("сначала обновите данные"), d.window)
		return
	}
	paths, e := report.Write(d.cfg.ReportsDir, time.Now(), snap)
	if e != nil {
		dialog.ShowError(e, d.window)
		return
	}
	dialog.ShowInformation("Экспорт", strings.Join(paths.All(), "\n"), d.window)
}

func portfolioRows(s *model.Snapshot) ([]string, [][]string) {
	cols := []string{"account", "account_id", "type", "ticker", "isin", "name", "currency", "quantity", "avg_price", "current_price", "current_value", "pnl_abs", "pnl_pct", "coupon_rate_pct", "current_yield", "yield_to_maturity", "coupon_frequency", "next_coupon_date", "next_coupon_amount", "issuer_rating", "last_dividend_amount", "dividend_frequency", "next_payment_date", "next_payment_amount"}
	var rows [][]string
	for _, a := range s.Accounts {
		for _, p := range a.Positions {
			r := []string{a.Name, a.ID, p.InstrumentType, p.Ticker, p.ISIN, p.Name, p.Currency, p.Quantity, p.AvgPrice, p.CurrentPrice, p.CurrentValue, p.PnLAbs, p.PnLPct}
			if p.Bond != nil {
				r = append(r, p.Bond.CouponRatePct, p.Bond.CurrentYield, p.Bond.YieldToMaturity, p.Bond.CouponFrequency, p.Bond.NextCouponDate, p.Bond.NextCouponAmount, p.Bond.IssuerRating)
			} else {
				r = append(r, model.NA, model.NA, model.NA, model.NA, model.NA, model.NA, model.NA)
			}
			if p.Share != nil {
				r = append(r, p.Share.LastDividendAmount, p.Share.Frequency, p.Share.NextPaymentDate, p.Share.NextPaymentAmount)
			} else {
				r = append(r, model.NA, model.NA, model.NA, model.NA)
			}
			rows = append(rows, r)
		}
	}
	return cols, rows
}
func operationRows(s *model.Snapshot) ([]string, [][]string) {
	cols := []string{"id", "account_id", "account_name", "datetime", "type", "instrument_type", "ticker", "isin", "name", "quantity", "payment_amount", "payment_currency", "state"}
	rows := make([][]string, 0, len(s.Operations))
	for _, o := range s.Operations {
		rows = append(rows, []string{o.ID, o.AccountID, o.AccountName, o.DateTime, o.Type, o.InstrumentType, o.Ticker, o.ISIN, o.Name, o.Quantity, o.PaymentAmount, o.PaymentCurrency, o.State})
	}
	return cols, rows
}

func (d *desktop) instrumentTab() fyne.CanvasObject {
	d.instruments.onRow = func(row []string) {
		var b strings.Builder
		for i, value := range row {
			if i < len(d.instruments.columns) {
				fmt.Fprintf(&b, "%s: %s\n", d.instruments.columns[i], value)
			}
		}
		dialog.ShowInformation(d.tr("details"), b.String(), d.window)
	}
	typeSelect := widget.NewSelect([]string{"share", "bond", "etf", "currency", "future"}, nil)
	typeSelect.SetSelected("share")
	currency, exchange, sector := widget.NewEntry(), widget.NewEntry(), widget.NewEntry()
	currency.SetPlaceHolder("Валюта")
	exchange.SetPlaceHolder("Биржа")
	sector.SetPlaceHolder("Сектор")
	risk := widget.NewSelect([]string{"", "RISK_LEVEL_LOW", "RISK_LEVEL_MODERATE", "RISK_LEVEL_HIGH", "RISK_LEVEL_UNSPECIFIED"}, nil)
	frequency := widget.NewSelect([]string{"", "12", "4", "2", "1"}, nil)
	couponType := widget.NewSelect([]string{"", "fixed", "floating"}, nil)
	dividends := widget.NewSelect([]string{"", "yes", "no"}, nil)
	rateFrom, rateTo, month := widget.NewEntry(), widget.NewEntry(), widget.NewEntry()
	rateFrom.SetPlaceHolder("Ставка от")
	rateTo.SetPlaceHolder("до")
	month.SetPlaceHolder("Месяц")
	load := func(force bool) {
		d.busy(d.tr("loading"), func() error {
			d.mu.Lock()
			defer d.mu.Unlock()
			client, e := d.client()
			if e != nil {
				return e
			}
			if d.cache == nil || force || !d.cache.Fresh(time.Duration(d.cfg.CatalogTTLHours)*time.Hour, time.Now()) {
				items, e := client.Catalog(context.Background(), time.Now())
				if e != nil {
					return e
				}
				d.cache = &catalog.Cache{UpdatedAt: time.Now(), Instruments: items}
				if e = d.cache.Save(d.cachePath); e != nil {
					return e
				}
			}
			base := catalog.Filter{Type: typeSelect.Selected, Query: d.instruments.search.Text, Currency: currency.Text, Exchange: exchange.Text, Sector: sector.Text, Risk: risk.Selected, Frequency: atoi(frequency.Selected), CouponType: couponType.Selected}
			candidates := catalog.Search(d.cache.Instruments, base)
			needsDetails := rateFrom.Text != "" || rateTo.Text != "" || month.Text != "" || dividends.Selected != ""
			if needsDetails {
				enriched := client.EnrichCatalog(context.Background(), candidates, time.Now())
				byUID := make(map[string]catalog.Instrument, len(enriched))
				for _, item := range enriched {
					byUID[item.UID] = item
				}
				for i, item := range d.cache.Instruments {
					if update, ok := byUID[item.UID]; ok {
						d.cache.Instruments[i] = update
					}
				}
				if e := d.cache.Save(d.cachePath); e != nil {
					return e
				}
			}
			var dividendFilter *bool
			if dividends.Selected != "" {
				v := dividends.Selected == "yes"
				dividendFilter = &v
			}
			f := catalog.Filter{Type: typeSelect.Selected, Query: d.instruments.search.Text, Currency: currency.Text, Exchange: exchange.Text, Sector: sector.Text, Risk: risk.Selected, Frequency: atoi(frequency.Selected), CouponType: couponType.Selected, RateFrom: catalog.Float(rateFrom.Text), RateTo: catalog.Float(rateTo.Text), CouponMonth: atoi(month.Text), Dividends: dividendFilter}
			items := catalog.Search(d.cache.Instruments, f)
			cols, rows := instrumentRows(items)
			fyne.Do(func() { d.instruments.set(cols, rows) })
			return nil
		})
	}
	for _, e := range []*widget.Entry{currency, exchange, sector, rateFrom, rateTo, month} {
		e.OnSubmitted = func(string) { load(false) }
	}
	for _, s := range []*widget.Select{typeSelect, risk, frequency, couponType, dividends} {
		s.OnChanged = func(string) { load(false) }
	}
	bar := container.New(layout.NewGridWrapLayout(fyne.NewSize(145, 38)), typeSelect, currency, exchange, sector, risk, frequency, couponType, dividends, rateFrom, rateTo, month, widget.NewButton(d.tr("search"), func() { load(false) }), widget.NewButton(d.tr("refresh"), func() { load(true) }), widget.NewButton(d.tr("export"), func() { d.instruments.exportView(d.cfg.ReportsDir) }))
	return container.NewBorder(bar, nil, nil, nil, d.instruments.root)
}
func atoi(s string) int { v, _ := strconv.Atoi(s); return v }
func instrumentRows(items []catalog.Instrument) ([]string, [][]string) {
	cols := []string{"type", "ticker", "name", "isin", "currency", "exchange", "sector", "risk_level", "coupon_frequency", "coupon_type", "coupon_rate_pct", "next_coupon_date", "dividends", "nominal", "maturity_date", "uid", "figi"}
	rows := make([][]string, 0, len(items))
	for _, i := range items {
		rate := model.NA
		if i.CouponRatePct != nil {
			rate = strconv.FormatFloat(*i.CouponRatePct, 'f', 2, 64)
		}
		ct := "fixed"
		if i.FloatingCoupon {
			ct = "floating"
		}
		rows = append(rows, []string{i.Type, i.Ticker, i.Name, i.ISIN, i.Currency, i.Exchange, i.Sector, i.RiskLevel, strconv.Itoa(i.CouponFrequency), ct, rate, i.NextCouponDate, strconv.FormatBool(i.HasDividends), i.Nominal, i.MaturityDate, i.UID, i.FIGI})
	}
	return cols, rows
}

func (d *desktop) settingsTab() fyne.CanvasObject {
	mode := widget.NewSelect([]string{config.ModeProd, config.ModeSandbox}, nil)
	mode.SetSelected(d.cfg.Mode)
	tokenEnv, reports, target := widget.NewEntry(), widget.NewEntry(), widget.NewEntry()
	tokenEnv.SetText(d.cfg.TokenEnv)
	reports.SetText(d.cfg.ReportsDir)
	target.SetText(d.cfg.TargetCurrency)
	retries := widget.NewEntry()
	retries.SetText(strconv.Itoa(d.cfg.Retries))
	delay := widget.NewEntry()
	delay.SetText(strconv.Itoa(d.cfg.RetryDelayMs))
	auto := widget.NewEntry()
	auto.SetText(strconv.Itoa(d.cfg.AutoRefreshMinutes))
	ttl := widget.NewEntry()
	ttl.SetText(strconv.Itoa(d.cfg.CatalogTTLHours))
	lang := widget.NewSelect([]string{"ru", "en"}, nil)
	lang.SetSelected(d.cfg.Language)
	form := widget.NewForm(widget.NewFormItem("Mode", mode), widget.NewFormItem("Token env", tokenEnv), widget.NewFormItem("Reports", reports), widget.NewFormItem("Target currency", target), widget.NewFormItem("Retries", retries), widget.NewFormItem("Retry delay ms", delay), widget.NewFormItem("Auto refresh min", auto), widget.NewFormItem("Catalog TTL h", ttl), widget.NewFormItem("Language", lang))
	form.OnSubmit = func() {
		c := *d.cfg
		c.Mode = mode.Selected
		c.TokenEnv = tokenEnv.Text
		c.ReportsDir = reports.Text
		c.TargetCurrency = target.Text
		c.Retries = atoi(retries.Text)
		c.RetryDelayMs = atoi(delay.Text)
		c.AutoRefreshMinutes = atoi(auto.Text)
		c.CatalogTTLHours = atoi(ttl.Text)
		c.Language = lang.Selected
		if e := c.Save(d.configPath); e != nil {
			dialog.ShowError(e, d.window)
			return
		}
		d.cfg = &c
		dialog.ShowInformation(d.tr("settings"), "OK", d.window)
	}
	return container.NewVBox(form)
}
