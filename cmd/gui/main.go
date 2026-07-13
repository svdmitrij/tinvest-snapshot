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
	"unicode/utf8"

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
	from, to                           *widget.DateEntry
	status                             *widget.Label
	refreshStop                        chan struct{}
}

type grid struct {
	mu                     sync.RWMutex
	window                 fyne.Window
	columns                []string
	all, visible, dataView [][]string
	table                  *widget.Table
	header, root           *fyne.Container
	search                 *widget.Entry
	filterColumn           *widget.Select
	filterValue            *widget.Entry
	matchIndex             int
	navigating             bool
	group                  *widget.Select
	sortColumn             int
	desc                   bool
	collapsed              map[string]bool
	onRow                  func([]string)
	tr                     func(string) string
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

// labeled puts a caption above a control so its purpose is visible in the UI.
func labeled(caption string, w fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(widget.NewLabel(caption), w)
}

// dateText renders a picked date in the format period.Resolve expects; an
// empty string keeps the CLI autodetection behaviour.
func dateText(e *widget.DateEntry) string {
	if e == nil || e.Date == nil {
		return ""
	}
	return e.Date.Format("2006-01-02")
}

// trCols maps raw field names to localized table headers.
func trCols(tr func(string) string, keys ...string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = tr("col_" + k)
	}
	return out
}

// headerWidth sizes a column so its localized caption stays readable: measured
// in runes, not bytes, because Cyrillic headers are twice as long in bytes, and
// with room for the sort arrow appended to the active column.
func headerWidth(caption string) float32 {
	const perRune, padding, narrowest, widest = 9, 46, 140, 340
	width := float32(utf8.RuneCountInString(caption)*perRune + padding)
	return min(max(width, narrowest), widest)
}

// baseCurrencies seed the currency pickers before the catalog is loaded, so a
// conversion target can be chosen on first run.
var baseCurrencies = []string{"rub", "usd", "eur", "cny", "hkd", "chf", "gbp", "jpy", "try", "kzt", "byn", "amd", "aed"}

// currencyOptions lists the currency codes offered by the pickers: the ones the
// catalog actually contains, plus the seeds and the configured value so a code
// saved earlier never disappears from the list.
func currencyOptions(c *catalog.Cache, extra string) []string {
	seen := map[string]bool{}
	add := func(code string) {
		if code = strings.ToLower(strings.TrimSpace(code)); code != "" {
			seen[code] = true
		}
	}
	for _, code := range baseCurrencies {
		add(code)
	}
	if c != nil {
		for _, i := range c.Instruments {
			add(i.Currency)
		}
	}
	add(extra)
	out := make([]string, 0, len(seen))
	for code := range seen {
		out = append(out, strings.ToUpper(code))
	}
	sort.Strings(out)
	return out
}

// widthFraction lays its children out at a fraction of the available width,
// so form fields do not stretch to the very edge of the window.
type widthFraction struct{ frac float32 }

func (l widthFraction) MinSize(objects []fyne.CanvasObject) fyne.Size {
	size := fyne.NewSize(0, 0)
	for _, o := range objects {
		size = size.Max(o.MinSize())
	}
	return size
}

func (l widthFraction) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		o.Resize(fyne.NewSize(size.Width*l.frac, o.MinSize().Height))
		o.Move(fyne.NewPos(0, 0))
	}
}

func (d *desktop) build() {
	d.portfolio = newGrid(d.window, d.tr)
	d.operations = newGrid(d.window, d.tr)
	d.instruments = newGrid(d.window, d.tr)
	d.from = widget.NewDateEntry()
	d.to = widget.NewDateEntry()
	d.status = widget.NewLabel("")
	refresh := widget.NewButton(d.tr("refresh"), func() { d.refreshPortfolio() })
	exportAll := widget.NewButton(d.tr("export_all"), func() { d.exportAll() })
	portfolioBar := container.NewHBox(refresh, exportAll, widget.NewButton(d.tr("export"), func() { d.portfolio.exportView(d.cfg.ReportsDir) }))
	operationsBar := container.NewHBox(labeled(d.tr("from"), d.from), labeled(d.tr("to"), d.to),
		widget.NewButton(d.tr("refresh"), func() { d.refreshPortfolio() }),
		widget.NewButton(d.tr("export"), func() { d.operations.exportView(d.cfg.ReportsDir) }))
	searchTab := d.instrumentTab()
	tabs := container.NewAppTabs(container.NewTabItem(d.tr("portfolio"), container.NewBorder(portfolioBar, nil, nil, nil, d.portfolio.root)), container.NewTabItem(d.tr("operations"), container.NewBorder(operationsBar, nil, nil, nil, d.operations.root)), container.NewTabItem(d.tr("instruments"), searchTab), container.NewTabItem(d.tr("settings"), d.settingsTab()))
	d.window.SetContent(container.NewBorder(nil, d.status, nil, nil, tabs))
	if d.snapshot != nil {
		pc, pr := portfolioRows(d.snapshot, d.tr)
		oc, or := operationRows(d.snapshot, d.tr)
		d.portfolio.set(pc, pr)
		d.operations.set(oc, or)
	}
	d.startAutoRefresh()
}

func (d *desktop) startAutoRefresh() {
	if d.refreshStop != nil {
		close(d.refreshStop)
		d.refreshStop = nil
	}
	if d.cfg.AutoRefreshMinutes <= 0 {
		return
	}
	stop := make(chan struct{})
	d.refreshStop = stop
	go func(period time.Duration) {
		ticker := time.NewTicker(period)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				d.refreshPortfolio()
			case <-stop:
				return
			}
		}
	}(time.Duration(d.cfg.AutoRefreshMinutes) * time.Minute)
}

func newGrid(w fyne.Window, tr func(string) string) *grid {
	g := &grid{window: w, sortColumn: -1, collapsed: map[string]bool{}, tr: tr}
	g.search = widget.NewEntry()
	g.search.SetPlaceHolder(tr("search"))
	g.group = widget.NewSelect([]string{}, func(string) { g.apply() })
	g.filterColumn = widget.NewSelect([]string{}, func(string) { g.apply() })
	g.filterValue = widget.NewEntry()
	g.filterValue.SetPlaceHolder(tr("value"))
	g.filterValue.OnChanged = func(string) { g.apply() }
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
		if g.navigating {
			g.navigating = false
			g.mu.Unlock()
			return
		}
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
	g.search.OnChanged = func(string) { g.matchIndex = -1; g.findNext() }
	next := widget.NewButton(tr("find_next"), func() { g.findNext() })
	g.root = container.NewBorder(container.NewHBox(
		labeled(tr("search_label"), g.search), labeled(" ", next),
		labeled(tr("filter_label"), g.filterColumn), labeled(tr("value"), g.filterValue),
		labeled(tr("group_label"), g.group)), nil, nil, nil, g.table)
	return g
}
func (g *grid) set(columns []string, rows [][]string) {
	g.mu.Lock()
	g.columns = columns
	g.all = rows
	g.mu.Unlock()
	g.group.Options = append([]string{""}, columns...)
	g.filterColumn.Options = append([]string{""}, columns...)
	for i, c := range columns {
		g.table.SetColumnWidth(i, headerWidth(c))
	}
	g.apply()
}
func (g *grid) apply() {
	g.mu.Lock()
	filter := strings.ToLower(strings.TrimSpace(g.filterValue.Text))
	filterCol := -1
	for i, c := range g.columns {
		if c == g.filterColumn.Selected {
			filterCol = i
			break
		}
	}
	rows := make([][]string, 0, len(g.all))
	for _, r := range g.all {
		if filter == "" || filterCol < 0 || strings.Contains(strings.ToLower(r[filterCol]), filter) {
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
			if g.desc {
				return rows[i][col] > rows[j][col]
			}
			return rows[i][col] < rows[j][col]
		})
	}
	g.dataView = append([][]string(nil), rows...)
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

func (g *grid) findNext() {
	g.mu.Lock()
	q := strings.ToLower(strings.TrimSpace(g.search.Text))
	if q == "" || len(g.visible) == 0 {
		g.mu.Unlock()
		return
	}
	for step := 1; step <= len(g.visible); step++ {
		idx := (g.matchIndex + step) % len(g.visible)
		if strings.Contains(strings.ToLower(strings.Join(g.visible[idx], "\x00")), q) {
			g.matchIndex = idx
			g.navigating = true
			g.mu.Unlock()
			fyne.Do(func() { id := widget.TableCellID{Row: idx, Col: 0}; g.table.ScrollTo(id); g.table.Select(id) })
			return
		}
	}
	g.mu.Unlock()
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
	_ = w.WriteAll(g.dataView)
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
	sheet := g.tr("table")
	_ = x.SetSheetName("Sheet1", sheet)
	rows := append([][]string{g.columns}, g.dataView...)
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
	dialog.ShowInformation(g.tr("export_title"), csvPath+"\n"+xlsxPath, g.window)
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
		win, e := period.Resolve(dateText(d.from), dateText(d.to), d.cfg.ReportsDir, now)
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
		pc, pr := portfolioRows(snap, d.tr)
		oc, or := operationRows(snap, d.tr)
		fyne.Do(func() { d.portfolio.set(pc, pr); d.operations.set(oc, or) })
		return nil
	})
}
func (d *desktop) exportAll() {
	d.mu.RLock()
	snap := d.snapshot
	d.mu.RUnlock()
	if snap == nil {
		dialog.ShowError(fmt.Errorf("%s", d.tr("refresh_first")), d.window)
		return
	}
	paths, e := report.Write(d.cfg.ReportsDir, time.Now(), snap)
	if e != nil {
		dialog.ShowError(e, d.window)
		return
	}
	dialog.ShowInformation(d.tr("export_title"), strings.Join(paths.All(), "\n"), d.window)
}

var portfolioFields = []string{"row_kind", "account", "account_id", "type", "ticker", "isin", "name", "currency", "quantity", "avg_price", "current_price", "current_value", "pnl_abs", "pnl_pct", "coupon_rate_pct", "current_yield", "yield_to_maturity", "coupon_frequency", "next_coupon_date", "next_coupon_amount", "issuer_rating", "last_dividend_amount", "dividend_frequency", "next_payment_date", "next_payment_amount", "total_amount", "converted_amount", "converted_currency", "conversion_rate"}

var operationFields = []string{"id", "account_id", "account_name", "datetime", "type", "instrument_type", "ticker", "isin", "name", "quantity", "payment_amount", "payment_currency", "state"}

var instrumentFields = []string{"type", "ticker", "name", "isin", "currency", "exchange", "sector", "risk_level", "coupon_frequency", "coupon_type", "coupon_rate_pct", "next_coupon_date", "dividends", "nominal", "maturity_date", "uid", "figi"}

func portfolioRows(s *model.Snapshot, tr func(string) string) ([]string, [][]string) {
	cols := trCols(tr, portfolioFields...)
	var rows [][]string
	pad := func(values ...string) []string {
		row := make([]string, len(cols))
		for i := range row {
			row[i] = model.NA
		}
		copy(row, values)
		return row
	}
	for _, a := range s.Accounts {
		for _, p := range a.Positions {
			r := []string{"position", a.Name, a.ID, p.InstrumentType, p.Ticker, p.ISIN, p.Name, p.Currency, p.Quantity, p.AvgPrice, p.CurrentPrice, p.CurrentValue, p.PnLAbs, p.PnLPct}
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
			for len(r) < len(cols) {
				r = append(r, model.NA)
			}
			rows = append(rows, r)
		}
		for _, cash := range a.Cash {
			rows = append(rows, pad("cash", a.Name, a.ID, "cash", model.NA, model.NA, tr("free_cash"), cash.Currency, cash.Amount))
		}
		row := pad("account_total", a.Name, a.ID, "total", model.NA, model.NA, tr("account_total"), a.Total.Currency)
		row[25] = a.Total.Amount
		if a.TotalConverted != nil {
			row[26], row[27], row[28] = a.TotalConverted.Amount, a.TotalConverted.Currency, a.TotalConverted.Rate
		}
		rows = append(rows, row)
	}
	for _, total := range s.GrandTotals {
		row := pad("grand_total", tr("all_accounts"), model.NA, "total", model.NA, model.NA, tr("grand_total"), total.Currency)
		row[25] = total.Amount
		rows = append(rows, row)
	}
	if s.GrandConverted != nil {
		row := pad("grand_converted", tr("all_accounts"), model.NA, "total", model.NA, model.NA, tr("grand_converted"), s.GrandConverted.Currency)
		row[26], row[27], row[28] = s.GrandConverted.Amount, s.GrandConverted.Currency, s.GrandConverted.Rate
		rows = append(rows, row)
	}
	return cols, rows
}
func operationRows(s *model.Snapshot, tr func(string) string) ([]string, [][]string) {
	cols := trCols(tr, operationFields...)
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
	typeOptions := make([]string, len(instrumentTypes))
	for i, t := range instrumentTypes {
		typeOptions[i] = d.tr("type_" + t)
	}
	typeSelect := widget.NewSelect(typeOptions, nil)
	typeSelect.SetSelected(d.tr("type_share"))
	currency := widget.NewSelect(append([]string{d.tr("all")}, currencyOptions(d.cache, "")...), nil)
	currency.SetSelected(d.tr("all"))
	exchange, sector := widget.NewEntry(), widget.NewEntry()
	exchange.SetPlaceHolder(d.tr("exchange"))
	sector.SetPlaceHolder(d.tr("sector"))
	risk := widget.NewSelect([]string{"", d.tr("risk_low"), d.tr("risk_moderate"), d.tr("risk_high"), d.tr("na")}, nil)
	frequency := widget.NewSelect([]string{"", d.tr("monthly"), d.tr("quarterly"), d.tr("semiannual"), d.tr("annual")}, nil)
	couponType := widget.NewSelect([]string{"", d.tr("fixed"), d.tr("floating")}, nil)
	dividends := widget.NewSelect([]string{"", d.tr("yes"), d.tr("no")}, nil)
	rateFrom, rateTo, month := widget.NewEntry(), widget.NewEntry(), widget.NewEntry()
	rateFrom.SetPlaceHolder(d.tr("rate_from"))
	rateTo.SetPlaceHolder(d.tr("rate_to"))
	month.SetPlaceHolder(d.tr("coupon_month"))
	updated := widget.NewLabel("")
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
			base := catalog.Filter{Type: typeAPI(typeSelect.Selected, d), Query: d.instruments.search.Text, Currency: currencyFilter(currency.Selected, d), Exchange: exchange.Text, Sector: sector.Text, Risk: riskAPI(risk.Selected, d), Frequency: frequencyAPI(frequency.Selected, d), CouponType: couponAPI(couponType.Selected, d)}
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
				v := dividends.Selected == d.tr("yes")
				dividendFilter = &v
			}
			f := catalog.Filter{Type: typeAPI(typeSelect.Selected, d), Query: d.instruments.search.Text, Currency: currencyFilter(currency.Selected, d), Exchange: exchange.Text, Sector: sector.Text, Risk: riskAPI(risk.Selected, d), Frequency: frequencyAPI(frequency.Selected, d), CouponType: couponAPI(couponType.Selected, d), RateFrom: catalog.Float(rateFrom.Text), RateTo: catalog.Float(rateTo.Text), CouponMonth: atoi(month.Text), Dividends: dividendFilter}
			items := catalog.Search(d.cache.Instruments, f)
			cols, rows := instrumentRows(items, d)
			options := append([]string{d.tr("all")}, currencyOptions(d.cache, "")...)
			fyne.Do(func() {
				d.instruments.set(cols, rows)
				currency.Options = options
				currency.Refresh()
				updated.SetText(d.tr("catalog_updated") + ": " + d.cache.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
			})
			return nil
		})
	}
	for _, e := range []*widget.Entry{exchange, sector, rateFrom, rateTo, month} {
		e.OnSubmitted = func(string) { load(false) }
	}
	for _, s := range []*widget.Select{typeSelect, currency, risk, frequency, couponType, dividends} {
		s.OnChanged = func(string) { load(false) }
	}
	bar := container.New(layout.NewGridWrapLayout(fyne.NewSize(190, 74)),
		labeled(d.tr("cap_type"), typeSelect),
		labeled(d.tr("currency"), currency),
		labeled(d.tr("exchange"), exchange),
		labeled(d.tr("sector"), sector),
		labeled(d.tr("cap_risk"), risk),
		labeled(d.tr("cap_frequency"), frequency),
		labeled(d.tr("cap_coupon_type"), couponType),
		labeled(d.tr("cap_dividends"), dividends),
		labeled(d.tr("rate_from"), rateFrom),
		labeled(d.tr("rate_to"), rateTo),
		labeled(d.tr("coupon_month"), month),
		labeled(" ", widget.NewButton(d.tr("search"), func() { load(false) })),
		labeled(" ", widget.NewButton(d.tr("refresh"), func() { load(true) })),
		labeled(" ", widget.NewButton(d.tr("export"), func() { d.instruments.exportView(d.cfg.ReportsDir) })))
	if d.cache != nil {
		updated.SetText(d.tr("catalog_updated") + ": " + d.cache.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
	}
	return container.NewBorder(container.NewVBox(bar, updated), nil, nil, nil, d.instruments.root)
}
func atoi(s string) int { v, _ := strconv.Atoi(s); return v }
func riskAPI(s string, d *desktop) string {
	switch s {
	case d.tr("risk_low"):
		return "RISK_LEVEL_LOW"
	case d.tr("risk_moderate"):
		return "RISK_LEVEL_MODERATE"
	case d.tr("risk_high"):
		return "RISK_LEVEL_HIGH"
	case d.tr("na"):
		return "RISK_LEVEL_UNSPECIFIED"
	}
	return ""
}
func frequencyAPI(s string, d *desktop) int {
	switch s {
	case d.tr("monthly"):
		return 12
	case d.tr("quarterly"):
		return 4
	case d.tr("semiannual"):
		return 2
	case d.tr("annual"):
		return 1
	}
	return 0
}

// currencyFilter maps the picked currency label to a catalog filter value; the
// "all" option clears the filter.
func currencyFilter(label string, d *desktop) string {
	if label == "" || label == d.tr("all") {
		return ""
	}
	return strings.ToLower(label)
}
func couponAPI(s string, d *desktop) string {
	if s == d.tr("fixed") {
		return "fixed"
	}
	if s == d.tr("floating") {
		return "floating"
	}
	return ""
}

var instrumentTypes = []string{"share", "bond", "etf", "currency", "future"}

func typeAPI(label string, d *desktop) string {
	for _, t := range instrumentTypes {
		if label == d.tr("type_"+t) {
			return t
		}
	}
	return "share"
}
func instrumentRows(items []catalog.Instrument, d *desktop) ([]string, [][]string) {
	cols := trCols(d.tr, instrumentFields...)
	rows := make([][]string, 0, len(items))
	for _, i := range items {
		rate := model.NA
		if i.CouponRatePct != nil {
			rate = strconv.FormatFloat(*i.CouponRatePct, 'f', 2, 64)
		}
		ct := d.tr("fixed")
		if i.FloatingCoupon {
			ct = d.tr("floating")
		}
		risk := map[string]string{"RISK_LEVEL_LOW": d.tr("risk_low"), "RISK_LEVEL_MODERATE": d.tr("risk_moderate"), "RISK_LEVEL_HIGH": d.tr("risk_high"), "RISK_LEVEL_UNSPECIFIED": d.tr("na")}[i.RiskLevel]
		if risk == "" {
			risk = d.tr("na")
		}
		freq := map[int]string{12: d.tr("monthly"), 4: d.tr("quarterly"), 2: d.tr("semiannual"), 1: d.tr("annual")}[i.CouponFrequency]
		if freq == "" {
			freq = d.tr("na")
		}
		div := d.tr("no")
		if i.HasDividends {
			div = d.tr("yes")
		}
		rows = append(rows, []string{i.Type, i.Ticker, i.Name, i.ISIN, i.Currency, i.Exchange, i.Sector, risk, freq, ct, rate, i.NextCouponDate, div, i.Nominal, i.MaturityDate, i.UID, i.FIGI})
	}
	return cols, rows
}

func (d *desktop) settingsTab() fyne.CanvasObject {
	mode := widget.NewSelect([]string{config.ModeProd, config.ModeSandbox}, nil)
	mode.SetSelected(d.cfg.Mode)
	tokenEnv, reports := widget.NewEntry(), widget.NewEntry()
	token := widget.NewPasswordEntry()
	token.SetText(d.cfg.Token)
	tokenEnv.SetText(d.cfg.TokenEnv)
	reports.SetText(d.cfg.ReportsDir)
	target := widget.NewSelect(d.targetCurrencyOptions(), nil)
	target.SetSelected(d.targetCurrencyLabel(d.cfg.TargetCurrency))
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
	form := widget.NewForm(widget.NewFormItem(d.tr("mode"), mode), widget.NewFormItem(d.tr("token_env"), tokenEnv), widget.NewFormItem(d.tr("token_value"), token), widget.NewFormItem(d.tr("reports"), reports), widget.NewFormItem(d.tr("target_currency"), target), widget.NewFormItem(d.tr("retries"), retries), widget.NewFormItem(d.tr("retry_delay"), delay), widget.NewFormItem(d.tr("auto_refresh"), auto), widget.NewFormItem(d.tr("catalog_ttl"), ttl), widget.NewFormItem(d.tr("language"), lang))
	form.OnSubmit = func() {
		c := *d.cfg
		c.Mode = mode.Selected
		c.TokenEnv = tokenEnv.Text
		c.Token = token.Text
		c.ReportsDir = reports.Text
		c.TargetCurrency = targetCurrencyValue(target.Selected, d)
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
		d.loadText()
		d.build()
		dialog.ShowInformation(d.tr("settings"), d.tr("saved"), d.window)
	}
	return container.NewVBox(container.New(widthFraction{frac: 0.98}, form))
}

func (d *desktop) targetCurrencyOptions() []string {
	return append([]string{d.tr("no_conversion")}, currencyOptions(d.cache, d.cfg.TargetCurrency)...)
}

func (d *desktop) targetCurrencyLabel(code string) string {
	if strings.TrimSpace(code) == "" {
		return d.tr("no_conversion")
	}
	return strings.ToUpper(code)
}

func targetCurrencyValue(label string, d *desktop) string {
	if label == "" || label == d.tr("no_conversion") {
		return ""
	}
	return strings.ToLower(label)
}
