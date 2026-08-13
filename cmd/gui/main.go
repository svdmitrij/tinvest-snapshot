//go:build !ci

package main

import (
	"context"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	desktopdriver "fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/dmitry/tinvest-snapshot/internal/catalog"
	"github.com/dmitry/tinvest-snapshot/internal/config"
	"github.com/dmitry/tinvest-snapshot/internal/display"
	"github.com/dmitry/tinvest-snapshot/internal/model"
	"github.com/dmitry/tinvest-snapshot/internal/period"
	"github.com/dmitry/tinvest-snapshot/internal/report"
	"github.com/dmitry/tinvest-snapshot/internal/tinvest"
	"github.com/xuri/excelize/v2"
)

//go:embed i18n/*.json config.example.json
var translations embed.FS

//go:embed config.example.json
var embeddedConfigBytes []byte

//go:embed icon.png
var iconData []byte

func appIcon() fyne.Resource {
	return fyne.NewStaticResource("icon", iconData)
}

type desktop struct {
	mu                                                             sync.RWMutex
	window                                                         fyne.Window
	configPath, cachePath, portfolioCachePath, operationsCachePath string
	cfg                                                            *config.Config
	snapshot                                                       *model.Snapshot
	scache                                                         *catalog.SegmentedCache
	text                                                           map[string]string
	portfolio, operations, instruments                             *grid
	from, to                                                       *widget.DateEntry
	status                                                         *widget.Label
	portfolioVisited, operationsVisited, instrumentsVisited        bool
	loadInstruments                                                func(bool)
	resetInstrumentFilters                                         func()
	instrumentFilterSelects                                        []*widget.Select
	refreshMu                                                      sync.Mutex
	refreshing                                                     map[string]string
}

const refreshTimeout = 30 * time.Second

// instrumentRefreshTimeout returns the user-configured bound for the full
// network instrument load (FR51/FR52); other network paths keep refreshTimeout.
func (d *desktop) instrumentRefreshTimeout() time.Duration {
	return time.Duration(d.cfg.InstrumentLoadTimeoutSeconds) * time.Second
}

func (d *desktop) portfolioRefreshTimeout() time.Duration {
	return time.Duration(d.cfg.PortfolioLoadTimeoutSeconds) * time.Second
}

func (d *desktop) dividendLoadTimeout() time.Duration {
	return time.Duration(d.cfg.DividendLoadTimeoutSeconds) * time.Second
}

type grid struct {
	mu                      sync.RWMutex
	window                  fyne.Window
	columns                 []string
	all, visible, dataView  [][]string
	table                   *widget.Table
	header, root            *fyne.Container
	search                  *widget.Entry
	findNextButton          *tooltipButton
	searchBlock, groupBlock fyne.CanvasObject
	filters                 []*dynamicFilterRow
	filterBox               *fyne.Container
	filterPanel             *fyne.Container
	filterRowHeight         float32
	matchIndex              int
	navigating              bool
	group                   *widget.Select
	sortColumn              int
	desc                    bool
	collapsed               map[string]bool
	onRow                   func([]string)
	tr                      func(string) string
	fontScale               int // percent (60–200), default 100
}

type dynamicFilterRow struct {
	column    *widget.Select
	operation *widget.Select
	value     *widget.SelectEntry
	boolean   *widget.Select
	valueBox  *fyne.Container
	valueSet  bool
}

// resolveConfigPath finds or creates the config file. The resolution order is:
// 1. --config flag (explicit path)
// 2. ./config.json (backward compatibility)
// 3. <UserConfigDir>/tinvest-snapshot/config.json (OS config directory)
// 4. Auto-create from embedded template in <UserConfigDir>/tinvest-snapshot/
// Returns the path and whether it was newly auto-created.
func resolveConfigPath() (string, *config.Config, error) {
	explicitPath := flag.String("config", "", "path to configuration")
	flag.Parse()

	// 1. Explicit --config flag (backward compatible)
	if *explicitPath != "" {
		cfg, err := config.Load(*explicitPath)
		if err != nil {
			return "", nil, err
		}
		return *explicitPath, cfg, nil
	}

	// 2. ./config.json (backward compatibility)
	if _, err := os.Stat("config.json"); err == nil {
		cfg, err := config.Load("config.json")
		if err != nil {
			return "", nil, err
		}
		return "config.json", cfg, nil
	}

	// 3. <UserConfigDir>/tinvest-snapshot/config.json
	userCfgDir, err := os.UserConfigDir()
	if err == nil {
		osCfgPath := filepath.Join(userCfgDir, "tinvest-snapshot", "config.json")
		if _, err := os.Stat(osCfgPath); err == nil {
			cfg, err := config.Load(osCfgPath)
			if err != nil {
				return "", nil, err
			}
			return osCfgPath, cfg, nil
		}
	}

	// 4. Auto-create from embedded template in <UserConfigDir>/tinvest-snapshot/
	if userCfgDir == "" {
		userCfgDir, _ = os.UserConfigDir()
	}
	if userCfgDir == "" {
		return "", nil, fmt.Errorf("не удалось определить каталог конфигурации ОС и config.json не найден")
	}
	osCfgDir := filepath.Join(userCfgDir, "tinvest-snapshot")
	osCfgPath := filepath.Join(osCfgDir, "config.json")
	if err := os.MkdirAll(osCfgDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("не удалось создать каталог конфигурации %s: %w", osCfgDir, err)
	}
	if err := os.WriteFile(osCfgPath, embeddedConfigBytes, 0o644); err != nil {
		return "", nil, fmt.Errorf("не удалось записать конфиг %s: %w", osCfgPath, err)
	}
	cfg, err := config.Load(osCfgPath)
	if err != nil {
		return "", nil, err
	}
	return osCfgPath, cfg, nil
}

func main() {
	configPath, cfg, err := resolveConfigPath()
	if err != nil {
		// Show a GUI error dialog even when config resolution fails
		// (critical for Windows: double-click launch has no console).
		a := app.NewWithID("ru.dmitry.tinvest-snapshot")
		w := a.NewWindow("T-Invest")
		w.SetIcon(appIcon())
		msg := err.Error()
		// Keep stderr for console launches, add GUI dialog for desktop.
		fmt.Fprintln(os.Stderr, msg)
		w.SetContent(widget.NewLabel(""))
		w.SetOnClosed(func() { os.Exit(2) })
		w.Show()
		showStartupError(w, msg, func() { os.Exit(2) })
		a.Run()
		os.Exit(2)
	}
	cacheRoot, _ := os.UserCacheDir()
	a := app.NewWithID("ru.dmitry.tinvest-snapshot")
	a.Settings().SetTheme(calmTheme{theme.DefaultTheme()})
	w := a.NewWindow("T-Invest")
	w.SetIcon(appIcon())
	cacheDir := filepath.Join(cacheRoot, "tinvest-snapshot")
	d := &desktop{window: w, configPath: configPath, cachePath: filepath.Join(cacheDir, "catalog.json"), portfolioCachePath: filepath.Join(cacheDir, "portfolio.json"), operationsCachePath: filepath.Join(cacheDir, "operations.json"), cfg: cfg}
	d.scache, _ = catalog.LoadSegmented(d.cachePath)
	d.loadText()
	d.build()
	// Clamp the desired 1024×680 to the physical display dimensions read
	// from /sys/class/drm on Linux.  1024×680 fits on any screen from
	// 1366×768 up and is the safe fallback for headless servers, Windows,
	// and non-Linux platforms.  The scroll container makes every field
	// reachable at any window size.
	w.Resize(display.Clamp(fyne.NewSize(1024, 680)))
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

// dateSize widens the date pickers: at their natural width the chosen date was
// clipped and unreadable.
var dateSize = fyne.NewSize(210, 38)

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
// fontScale is a multiplier (e.g. 1.5 for 150%).
func headerWidth(caption string, fontScale float32) float32 {
	const perRune, padding, narrowest, widest = 9, 46, 140, 340
	width := float32(utf8.RuneCountInString(caption)*perRune+padding) * fontScale
	return min(max(width, narrowest*fontScale), widest*fontScale)
}

// baseCurrencies seed the currency pickers before the catalog is loaded, so a
// conversion target can be chosen on first run.
var baseCurrencies = []string{"rub", "usd", "eur", "cny", "hkd", "chf", "gbp", "jpy", "try", "kzt", "byn", "amd", "aed"}

// currencyOptions lists the currency codes offered by the pickers: the ones the
// catalog actually contains, plus the seeds and the configured value so a code
// saved earlier never disappears from the list.
func currencyOptions(c *catalog.SegmentedCache, extra string) []string {
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
		for _, i := range c.AllInstruments() {
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
	d.portfolio = newGrid(d.window, d.tr, d, d.cfg.FontScalePortfolio)
	d.operations = newGrid(d.window, d.tr, d, d.cfg.FontScaleOperations)
	d.instruments = newGrid(d.window, d.tr, d, d.cfg.FontScaleInstruments)
	d.from = widget.NewDateEntry()
	d.to = widget.NewDateEntry()
	d.from.SetPlaceHolder(d.tr("from"))
	d.to.SetPlaceHolder(d.tr("to"))
	d.status = widget.NewLabel("")

	portfolioBar := d.tableBar(d.portfolio, &d.cfg.FontScalePortfolio, d.refreshPortfolio, func() { d.exportAll() }, d.portfolio.reset)
	operationsBar := d.operationsBar()
	// Left-click on a position/operation row opens the instrument card with a
	// hyperlink to the T-Invest website.
	d.portfolio.onRow = func(row []string) { d.showRowCard(row, portfolioFields) }
	d.operations.onRow = func(row []string) { d.showRowCard(row, operationFields) }
	searchTab := d.instrumentTab()
	tabs := container.NewAppTabs(container.NewTabItem(d.tr("portfolio"), container.NewBorder(portfolioBar, nil, nil, nil, d.portfolio.root)), container.NewTabItem(d.tr("operations"), container.NewBorder(operationsBar, nil, nil, nil, d.operations.root)), container.NewTabItem(d.tr("instruments"), searchTab), container.NewTabItem(d.tr("settings"), d.settingsTab()))
	tabs.OnSelected = func(item *container.TabItem) {
		switch item.Text {
		case d.tr("portfolio"):
			d.showPortfolio()
		case d.tr("operations"):
			d.showOperations()
		case d.tr("instruments"):
			d.showInstruments()
		}
	}
	// Wrap the whole tab area in a scrollable container so the user can reach
	// every field even when the window is smaller than the content.
	d.window.SetContent(container.NewBorder(nil, d.status, nil, nil, container.NewScroll(tabs)))
	if d.snapshot != nil {
		pc, pr := portfolioRows(d.snapshot, d.tr)
		oc, or := operationRows(d.snapshot, d.tr, *d.cfg.TimezoneOffset)
		d.portfolio.set(pc, pr)
		d.operations.set(oc, or)
	}
	// The initially selected tab follows the same lazy-loading rule as every
	// later selection. No background refresh may fetch data from another tab.
	d.showPortfolio()
}

// applyFontScale updates column widths and refreshes the table for a new font scale.
func (d *desktop) applyFontScale(g *grid, scale float32) {
	g.mu.RLock()
	cols := g.columns
	g.mu.RUnlock()
	for i, c := range cols {
		g.table.SetColumnWidth(i, headerWidth(c, scale))
	}
	g.table.Refresh()
}

// makeScaleBar builds A− / A+ / scale-label controls for one grid.
func (d *desktop) makeScaleBar(g *grid, scalePtr *int) *fyne.Container {
	label := widget.NewLabel(fmt.Sprintf("%d%%", *scalePtr))
	minus := widget.NewButton("A\u2212", func() {
		if *scalePtr > 60 {
			*scalePtr -= 10
			g.fontScale = *scalePtr
			d.applyFontScale(g, float32(*scalePtr)/100.0)
			label.SetText(fmt.Sprintf("%d%%", *scalePtr))
			_ = d.cfg.Save(d.configPath)
		}
	})
	plus := widget.NewButton("A+", func() {
		if *scalePtr < 200 {
			*scalePtr += 10
			g.fontScale = *scalePtr
			d.applyFontScale(g, float32(*scalePtr)/100.0)
			label.SetText(fmt.Sprintf("%d%%", *scalePtr))
			_ = d.cfg.Save(d.configPath)
		}
	})
	return container.NewHBox(minus, plus, label)
}

// tableBar keeps shared table controls in the same adaptive order on every
// data tab. Prefix controls are used only for the operations period fields.
func (d *desktop) tableBar(g *grid, scalePtr *int, refresh, export, reset func(), prefix ...fyne.CanvasObject) *fyne.Container {
	objects := append([]fyne.CanvasObject{}, prefix...)
	objects = append(objects,
		button(d.tr("refresh"), widget.MediumImportance, refresh),
		button(d.tr("export"), widget.MediumImportance, export),
		d.makeScaleBar(g, scalePtr),
		g.searchBlock,
		g.groupBlock,
		button(d.tr("reset_all"), widget.MediumImportance, reset))
	return container.New(&flowLayout{widthFn: func() float32 {
		if d.window == nil || d.window.Canvas() == nil {
			return 0
		}
		return d.window.Canvas().Size().Width
	}}, objects...)
}

func (d *desktop) resetOperationsView() {
	d.operations.reset()
	d.from.SetDate(nil)
	d.to.SetDate(nil)
}

func (d *desktop) resetInstrumentsView() {
	d.instruments.reset()
	if d.resetInstrumentFilters != nil {
		d.resetInstrumentFilters()
	}
}

func (d *desktop) operationsBar() *fyne.Container {
	return d.tableBar(d.operations, &d.cfg.FontScaleOperations, d.refreshOperations, func() { d.operations.exportView(d.cfg.ReportsDir) }, d.resetOperationsView,
		container.NewGridWrap(dateSize, d.from), container.NewGridWrap(dateSize, d.to))
}

const (
	searchSampleRU = "абвгдеёжзийклмнопрст"
	searchSampleEN = "abcdefghijklmnopqrst"
)

// searchFieldWidth reserves enough room for either required 20-character
// sample at the current theme's normal text scale, plus input padding.
func searchFieldWidth() float32 {
	textSize := theme.TextSize()
	textWidth := max(
		fyne.MeasureText(searchSampleRU, textSize, fyne.TextStyle{}).Width,
		fyne.MeasureText(searchSampleEN, textSize, fyne.TextStyle{}).Width)
	return textWidth + 2*theme.Size(theme.SizeNameInnerPadding)
}

// tooltipButton fills the tooltip gap in Fyne 2.6's Button widget while
// keeping the standard button renderer and compact icon-only dimensions.
type tooltipButton struct {
	widget.Button
	tooltip string
	popup   *widget.PopUp
}

func newTooltipButton(icon fyne.Resource, tooltip string, tapped func()) *tooltipButton {
	b := &tooltipButton{tooltip: tooltip}
	b.Icon = icon
	b.OnTapped = tapped
	b.ExtendBaseWidget(b)
	return b
}

func (b *tooltipButton) MouseIn(e *desktopdriver.MouseEvent) {
	b.Button.MouseIn(e)
	canvas := fyne.CurrentApp().Driver().CanvasForObject(b)
	if canvas == nil || b.tooltip == "" {
		return
	}
	b.popup = widget.NewPopUp(container.NewPadded(widget.NewLabel(b.tooltip)), canvas)
	b.popup.ShowAtRelativePosition(fyne.NewPos(0, b.Size().Height), b)
}

func (b *tooltipButton) MouseOut() {
	b.Button.MouseOut()
	if b.popup != nil {
		b.popup.Hide()
		b.popup = nil
	}
}

// calmTheme keeps Fyne's light base but replaces the loud default accent with a
// muted green, so buttons, selection and the zebra rows read as one quiet
// palette. The light variant is pinned: the zebra colours are light by design.
type calmTheme struct{ fyne.Theme }

func (t calmTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNamePrimary:
		return color.NRGBA{R: 0x4C, G: 0x7A, B: 0x5E, A: 0xFF}
	case theme.ColorNameHover:
		return color.NRGBA{R: 0xDA, G: 0xE8, B: 0xDD, A: 0xFF}
	}
	return t.Theme.Color(name, theme.VariantLight)
}

// button gives every action the same muted styling; importance marks the
// primary action of a bar rather than adding another colour.
func button(label string, importance widget.Importance, tapped func()) *widget.Button {
	b := widget.NewButton(label, tapped)
	b.Importance = importance
	return b
}

// Zebra striping requested at acceptance: even rows light green, odd rows white.
var (
	zebraEven = color.NRGBA{R: 0xE8, G: 0xF5, B: 0xE9, A: 0xFF}
	zebraOdd  = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
)

// headerCell is a clickable column header whose text size scales with the grid.
type headerCell struct {
	widget.BaseWidget
	text  *canvas.Text
	onTap func()
	scale float32
}

func newHeaderCell(scale float32) *headerCell {
	h := &headerCell{scale: scale}
	h.text = canvas.NewText("", theme.ForegroundColor())
	h.text.TextSize = theme.TextSize() * scale
	h.text.TextStyle = fyne.TextStyle{Bold: true}
	h.ExtendBaseWidget(h)
	return h
}

func (h *headerCell) setText(s string) {
	h.text.Text = s
	h.text.TextSize = theme.TextSize() * h.scale
	h.text.Refresh()
}

func (h *headerCell) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(h.text)
}

func (h *headerCell) Tapped(*fyne.PointEvent) {
	if h.onTap != nil {
		h.onTap()
	}
}

// tableCell draws one table cell: a striped background plus the value, and
// offers the value for copying through a right-click menu.
// Left-click explicitly calls onTap to trigger the row card popup — this
// bypasses Fyne's event-bubbling chain (Table.Tapped → Select → OnSelected)
// which has proven unreliable across different display drivers.
type tableCell struct {
	widget.BaseWidget
	background *canvas.Rectangle
	text       *canvas.Text
	value      string
	key        string // localized column header (e.g. "Тикер", "Ticker")
	tr         func(string) string
	onTap      func()
}

func dynamicRowHeight() float32 {
	return widget.NewButton("", nil).MinSize().Height
}

func newTableCell(tr func(string) string) *tableCell {
	c := &tableCell{tr: tr}
	c.background = canvas.NewRectangle(zebraOdd)
	c.text = canvas.NewText("", theme.ForegroundColor())
	c.text.TextSize = theme.TextSize()
	c.ExtendBaseWidget(c)
	return c
}

func (c *tableCell) CreateRenderer() fyne.WidgetRenderer {
	minHeight := canvas.NewRectangle(color.Transparent)
	minHeight.SetMinSize(fyne.NewSize(1, dynamicRowHeight()))
	return widget.NewSimpleRenderer(container.NewStack(c.background, c.text, minHeight))
}

func (c *tableCell) set(value string, key string, even bool, fontScale float32) {
	c.value = value
	c.key = key
	c.text.Text = value
	c.text.TextSize = theme.TextSize() * fontScale
	c.text.Refresh()
	fill := zebraOdd
	if even {
		fill = zebraEven
	}
	if c.background.FillColor != fill {
		c.background.FillColor = fill
		c.background.Refresh()
	}
}

// Tapped triggers the row-level card popup directly, without relying on
// Fyne's event-bubbling chain to reach Table.Tapped → OnSelected.
func (c *tableCell) Tapped(e *fyne.PointEvent) {
	if c.onTap != nil {
		c.onTap()
	}
}

// TappedSecondary provides a right-click context menu to copy the cell value.
func (c *tableCell) TappedSecondary(e *fyne.PointEvent) {
	if c.value == "" {
		return
	}
	copyItem := fyne.NewMenuItem(c.tr("copy_cell"), func() {
		fyne.CurrentApp().Clipboard().SetContent(c.value)
	})
	canvas := fyne.CurrentApp().Driver().CanvasForObject(c)
	if canvas == nil {
		return
	}
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", copyItem), canvas, e.AbsolutePosition)
}

func newGrid(w fyne.Window, tr func(string) string, d *desktop, fontScale int) *grid {
	g := &grid{window: w, sortColumn: -1, collapsed: map[string]bool{}, tr: tr, fontScale: fontScale}
	g.filterRowHeight = dynamicRowHeight()
	g.search = widget.NewEntry()
	g.search.SetPlaceHolder(tr("search"))
	g.group = widget.NewSelect([]string{}, func(string) { g.apply() })
	g.group.PlaceHolder = tr("group_label")
	g.filterBox = container.New(&flowLayout{widthFn: func() float32 {
		if g.window == nil || g.window.Canvas() == nil {
			return 0
		}
		return g.window.Canvas().Size().Width
	}})
	filterBorder := canvas.NewRectangle(color.Transparent)
	filterBorder.StrokeColor = theme.ForegroundColor()
	filterBorder.StrokeWidth = 2
	g.filterPanel = container.NewStack(filterBorder, container.NewPadded(g.filterBox))
	g.addFilterRow()
	g.table = widget.NewTable(func() (int, int) { g.mu.RLock(); defer g.mu.RUnlock(); return len(g.visible), len(g.columns) }, func() fyne.CanvasObject {
		return newTableCell(tr)
	}, func(id widget.TableCellID, o fyne.CanvasObject) {
		g.mu.RLock()
		defer g.mu.RUnlock()
		value, key := "", ""
		if id.Row < len(g.visible) && id.Col < len(g.visible[id.Row]) {
			value = g.visible[id.Row][id.Col]
		}
		if id.Col >= 0 && id.Col < len(g.columns) {
			key = g.columns[id.Col]
		}
		cell := o.(*tableCell)
		cell.set(value, key, id.Row%2 == 0, float32(g.fontScale)/100.0)
		// Wire the cell tap: for group headers toggle collapse, for data
		// rows trigger the card popup directly — both bypass Fyne's
		// unreliable event-bubbling chain.
		if id.Row < len(g.visible) {
			row := g.visible[id.Row]
			first := ""
			if len(row) > 0 {
				first = row[0]
			}
			if strings.HasPrefix(first, "▾ ") || strings.HasPrefix(first, "▸ ") {
				key := strings.TrimPrefix(strings.TrimPrefix(first, "▾ "), "▸ ")
				cell.onTap = func() {
					g.collapsed[key] = !g.collapsed[key]
					g.apply()
				}
			} else if g.onRow != nil {
				cell.onTap = func() { g.onRow(row) }
			}
		}
	})
	g.table.ShowHeaderRow = true
	g.table.CreateHeader = func() fyne.CanvasObject {
		return newHeaderCell(float32(g.fontScale) / 100.0)
	}
	g.table.UpdateHeader = func(id widget.TableCellID, o fyne.CanvasObject) {
		if id.Row != -1 || id.Col < 0 || id.Col >= len(g.columns) {
			return
		}
		col := id.Col
		h := o.(*headerCell)
		h.scale = float32(g.fontScale) / 100.0
		name := g.columns[col]
		if g.sortColumn == col {
			if g.desc {
				name += " ▼"
			} else {
				name += " ▲"
			}
		}
		h.setText(name)
		h.onTap = func() {
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
	g.findNextButton = newTooltipButton(theme.SearchIcon(), tr("find_next"), func() { g.findNext() })
	g.findNextButton.Importance = widget.MediumImportance
	g.searchBlock = container.NewHBox(fixedWidth(searchFieldWidth(), g.search), g.findNextButton)
	g.groupBlock = g.group
	g.root = container.NewBorder(g.filterPanel, nil, nil, nil, g.table)
	return g
}
func (g *grid) set(columns []string, rows [][]string) {
	g.mu.Lock()
	g.columns = columns
	g.all = rows
	g.mu.Unlock()
	g.group.Options = append([]string{""}, columns...)
	g.updateDynamicFilters()
	scale := float32(g.fontScale) / 100.0
	for i, c := range columns {
		g.table.SetColumnWidth(i, headerWidth(c, scale))
	}
	g.apply()
}
func (g *grid) addFilterRow() {
	g.filters = append(g.filters, g.newFilterRow())
	g.renderFilterRows()
}

func (g *grid) newFilterRow() *dynamicFilterRow {
	r := &dynamicFilterRow{column: widget.NewSelect([]string{}, nil), operation: widget.NewSelect([]string{}, nil), value: widget.NewSelectEntry([]string{}), boolean: widget.NewSelect([]string{"true", "false", "?"}, nil), valueBox: container.NewMax()}
	r.column.PlaceHolder = g.tr("filter_column")
	r.operation.PlaceHolder = " "
	r.value.SetPlaceHolder(g.tr("value"))
	r.boolean.PlaceHolder = g.tr("value")
	r.valueBox.Add(r.value)
	r.column.OnChanged = func(string) { g.updateDynamicFilters(); g.apply() }
	r.operation.OnChanged = func(string) { g.updateDynamicFilters(); g.apply() }
	r.value.OnChanged = func(string) { r.valueSet = true; g.updateDynamicFilters(); g.apply() }
	r.boolean.OnChanged = func(string) { g.apply() }
	return r
}

func (g *grid) renderFilterRows() {
	g.filterBox.RemoveAll()
	for i, row := range g.filters {
		g.filterBox.Add(g.filterRowUI(row, i == len(g.filters)-1))
	}
}

// filterRowUI renders one condition as a single compact line: column,
// operation, value, add and remove — without captions above the widgets.
func (g *grid) filterRowUI(r *dynamicFilterRow, last bool) fyne.CanvasObject {
	remove := widget.NewButton("x", func() { g.removeFilterRow(r) })
	objects := []fyne.CanvasObject{fixedWidth(filterColumnWidth, r.column), fixedWidth(filterOperationWidth, r.operation), fixedWidth(filterValueWidth, r.valueBox)}
	if last {
		objects = append(objects, widget.NewButton("+", func() { g.addFilterRow(); g.updateDynamicFilters(); g.apply() }))
	}
	return container.NewHBox(append(objects, remove)...)
}
func (g *grid) removeFilterRow(target *dynamicFilterRow) {
	for i, row := range g.filters {
		if row != target {
			continue
		}
		g.filters = append(g.filters[:i], g.filters[i+1:]...)
		break
	}
	if len(g.filters) == 0 {
		g.filters = append(g.filters, g.newFilterRow())
	}
	g.renderFilterRows()
	g.updateDynamicFilters()
	g.apply()
}

// reset restores the neutral view state without changing sorted columns,
// scale, loaded data, or settings.
func (g *grid) reset() {
	g.search.SetText("")
	g.group.ClearSelected()
	g.mu.Lock()
	g.matchIndex = -1
	g.navigating = false
	g.collapsed = map[string]bool{}
	g.filters = []*dynamicFilterRow{g.newFilterRow()}
	g.mu.Unlock()
	g.table.UnselectAll()
	g.renderFilterRows()
	g.updateDynamicFilters()
	g.apply()
}
func (g *grid) conditions() []filterCondition {
	out := make([]filterCondition, len(g.filters))
	for i, r := range g.filters {
		value := r.value.Text
		if value == "" && r.valueSet {
			value = emptyFilterValue
		}
		if columnType(g.columns, r.column.Selected) == boolColumn {
			value = r.boolean.Selected
		}
		out[i] = filterCondition{r.column.Selected, r.operation.Selected, value}
	}
	return out
}
func (g *grid) updateDynamicFilters() {
	conditions := g.conditions()
	g.mu.RLock()
	columns, rows := append([]string(nil), g.columns...), append([][]string(nil), g.all...)
	g.mu.RUnlock()
	for i, r := range g.filters {
		r.column.Options = append([]string{""}, columns...)
		r.column.Refresh()
		values := conditionValues(filterRows(rows, columns, conditions, i), columns, conditions[i])
		sort.Strings(values[1:])
		if r.value.Text != "" && !slices.Contains(values, r.value.Text) {
			values = append(values, r.value.Text)
		}
		if columnType(columns, r.column.Selected) == boolColumn {
			r.operation.Options = []string{"=", "!="}
			r.valueBox.RemoveAll()
			r.valueBox.Add(r.boolean)
		} else {
			r.operation.Options = []string{">=", "<=", "=", "!="}
			r.value.SetOptions(values)
			r.valueBox.RemoveAll()
			r.valueBox.Add(r.value)
		}
		r.operation.Refresh()
	}
}
func (g *grid) apply() {
	g.mu.Lock()
	rows := make([][]string, 0, len(g.all))
	for _, r := range filterRows(g.all, g.columns, g.conditions(), -1) {
		rows = append(rows, append([]string(nil), r...))
	}

	groupName := g.group.Selected
	groupCol := -1
	if groupName != "" {
		for i, c := range g.columns {
			if c == groupName {
				groupCol = i
				break
			}
		}
	}

	// rowLess returns true if row a should come before row b when sorted by col.
	rowLess := func(a, b []string, col int, desc bool) bool {
		va, vb := a[col], b[col]
		fa, ea := strconv.ParseFloat(strings.ReplaceAll(strings.ReplaceAll(va, ",", "."), " ", ""), 64)
		fb, eb := strconv.ParseFloat(strings.ReplaceAll(strings.ReplaceAll(vb, ",", "."), " ", ""), 64)
		if ea == nil && eb == nil {
			if desc {
				return fa > fb
			}
			return fa < fb
		}
		if desc {
			return va > vb
		}
		return va < vb
	}

	if groupCol >= 0 {
		// Phase 1: global sort by group column for stable group order.
		sort.SliceStable(rows, func(i, j int) bool {
			return rowLess(rows[i], rows[j], groupCol, false)
		})
		// Phase 2: split into groups and sort each group by user's sortColumn.
		if g.sortColumn >= 0 && g.sortColumn != groupCol {
			start := 0
			for i := 1; i <= len(rows); i++ {
				if i == len(rows) || rows[i][groupCol] != rows[start][groupCol] {
					chunk := rows[start:i]
					sort.SliceStable(chunk, func(a, b int) bool {
						return rowLess(chunk[a], chunk[b], g.sortColumn, g.desc)
					})
					start = i
				}
			}
		} else if g.sortColumn == groupCol {
			// When sorting by the group column itself, sort globally (asc/desc).
			sort.SliceStable(rows, func(i, j int) bool {
				return rowLess(rows[i], rows[j], groupCol, g.desc)
			})
		}
	} else if g.sortColumn >= 0 {
		// No grouping: sort globally by user's column.
		sort.SliceStable(rows, func(i, j int) bool {
			return rowLess(rows[i], rows[j], g.sortColumn, g.desc)
		})
	}

	g.dataView = append([][]string(nil), rows...)
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
	g.visible = rows
	g.mu.Unlock()
	g.table.Refresh()
}

func (g *grid) findNext() {
	g.mu.Lock()
	q := strings.ToLower(strings.TrimSpace(g.search.Text))
	if q == "" {
		g.matchIndex = -1
		g.navigating = false
		g.mu.Unlock()
		g.table.UnselectAll()
		return
	}
	if len(g.visible) == 0 {
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

// defaultViewName keeps the previous naming rule as the pre-filled suggestion
// in the save dialog.
func defaultViewName(now time.Time) string {
	return "table_" + now.Format("20060102_150405") + ".csv"
}

// exportView asks where to save, then writes the current view next to the
// chosen name as .csv and .xlsx.
func (g *grid) exportView(dir string) {
	save := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil {
			showError(err, g.window)
			return
		}
		if w == nil {
			return // cancelled
		}
		path := w.URI().Path()
		_ = w.Close()
		base := strings.TrimSuffix(path, filepath.Ext(path))
		if e := g.writeView(base); e != nil {
			showError(e, g.window)
			return
		}
		dialog.ShowInformation(g.tr("export_title"), base+".csv\n"+base+".xlsx", g.window)
	}, g.window)
	save.SetFileName(defaultViewName(time.Now()))
	if lister, e := listerFor(dir); e == nil {
		save.SetLocation(lister)
	}
	save.Show()
}

// listerFor resolves a directory path into a dialog start location.
func listerFor(dir string) (fyne.ListableURI, error) {
	if dir == "" {
		return nil, fmt.Errorf("no directory")
	}
	if e := os.MkdirAll(dir, 0755); e != nil {
		return nil, e
	}
	return storage.ListerForURI(storage.NewFileURI(dir))
}

func (g *grid) writeView(base string) error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	csvPath, xlsxPath := base+".csv", base+".xlsx"
	f, e := os.Create(csvPath)
	if e != nil {
		return e
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
		return e
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
	return x.SaveAs(xlsxPath)
}

func (d *desktop) client() (*tinvest.Client, error) {
	token, e := d.cfg.ResolveToken()
	if e != nil {
		return nil, e
	}
	c, err := tinvest.New(d.cfg.BaseURL(), token, d.cfg.AppName, d.cfg.Retries, time.Duration(d.cfg.RetryDelayMs)*time.Millisecond, nil, d.cfg.TLS_CA_File, d.cfg.TLS_Insecure_Skip_Verify)
	if err != nil {
		return nil, err
	}
	c.Sandbox = d.cfg.Sandbox()
	return c, nil
}

// busy coalesces repeated refresh clicks for one tab. Network work never runs
// on the UI goroutine, and a completed attempt always releases its key.
func (d *desktop) busy(key, label string, fn func() error) {
	d.refreshMu.Lock()
	if d.refreshing == nil {
		d.refreshing = make(map[string]string)
	}
	if _, running := d.refreshing[key]; running {
		d.refreshMu.Unlock()
		fyne.Do(func() { d.refreshStatus() })
		return
	}
	d.refreshing[key] = label
	d.refreshMu.Unlock()
	fyne.Do(func() { d.refreshStatus() })
	go func() {
		e := fn()
		d.refreshMu.Lock()
		delete(d.refreshing, key)
		d.refreshMu.Unlock()
		fyne.Do(func() {
			d.refreshStatus()
			if e != nil {
				showError(e, d.window)
			}
		})
	}()
}

func (d *desktop) refreshStatus() {
	d.refreshMu.Lock()
	defer d.refreshMu.Unlock()
	for _, label := range d.refreshing {
		d.status.SetText(label)
		return
	}
	d.status.SetText("")
}

func (d *desktop) setBusyStatus(key, label string) {
	d.refreshMu.Lock()
	if _, running := d.refreshing[key]; running {
		d.refreshing[key] = label
	}
	d.refreshMu.Unlock()
	d.refreshStatus()
}

func (d *desktop) loadTimeoutError(scope, setting string, timeout time.Duration) error {
	return fmt.Errorf(d.tr(scope), int(timeout.Seconds()), setting)
}

func (d *desktop) catalogError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return d.loadTimeoutError("instrument_load_timeout_error", "instrument_load_timeout_seconds", d.instrumentRefreshTimeout())
	}
	var enrichment *tinvest.EnrichmentError
	if errors.As(err, &enrichment) {
		return fmt.Errorf(d.tr("instrument_enrichment_failed"), enrichment.Failed)
	}
	return err
}

func (d *desktop) failedInstrumentTypesError(types []string, cause error, enrichmentFailures int) error {
	names := make([]string, 0, len(types))
	seen := make(map[string]bool)
	for _, typ := range types {
		if !seen[typ] {
			names = append(names, d.tr("type_"+typ))
			seen[typ] = true
		}
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return fmt.Errorf(d.tr("instrument_types_timeout_error"), strings.Join(names, ", "), int(d.instrumentRefreshTimeout().Seconds()), "instrument_load_timeout_seconds")
	}
	if enrichmentFailures > 0 {
		return fmt.Errorf(d.tr("instrument_types_enrichment_failed"), strings.Join(names, ", "), enrichmentFailures)
	}
	return fmt.Errorf(d.tr("instrument_types_failed_error"), strings.Join(names, ", "))
}
func (d *desktop) refreshPortfolio() {
	d.busy("portfolio", d.tr("loading"), func() error {
		now := time.Now()
		c, e := d.client()
		if e != nil {
			return e
		}
		timeout := d.portfolioRefreshTimeout()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		snap, e := c.Collect(ctx, d.cfg.Mode, d.cfg.TargetCurrency, now, func(current, total int) {
			fyne.Do(func() {
				d.setBusyStatus("portfolio", fmt.Sprintf("%s (%d/%d)", d.tr("loading"), current, total))
			})
		})
		if e != nil {
			if errors.Is(e, context.DeadlineExceeded) {
				return d.loadTimeoutError("portfolio_load_timeout_error", "portfolio_load_timeout_seconds", timeout)
			}
			return e
		}
		d.mu.Lock()
		if d.snapshot != nil {
			snap.Operations, snap.OperationsPeriod = d.snapshot.Operations, d.snapshot.OperationsPeriod
		}
		d.snapshot = snap
		d.mu.Unlock()
		if err := saveSnapshotCache(d.portfolioCachePath, snapshotCache{UpdatedAt: now, Mode: d.cfg.Mode, Snapshot: snap}); err != nil {
			return err
		}
		pc, pr := portfolioRows(snap, d.tr)
		fyne.Do(func() { d.portfolio.set(pc, pr); d.portfolio.group.SetSelected(d.tr("col_account")) })
		return nil
	})
}

func (d *desktop) refreshOperations() {
	d.busy("operations", d.tr("loading_ops"), func() error {
		now := time.Now()
		fromText, toText := dateText(d.from), dateText(d.to)
		win, err := period.Resolve(fromText, toText, d.cfg.ReportsDir, now)
		if err != nil {
			return err
		}
		if fromText == "" && toText == "" {
			start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			end := start.AddDate(0, 0, 1).Add(-time.Second)
			win.GlobalFrom, win.To = &start, end
		}
		client, err := d.client()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
		defer cancel()
		operations, operationPeriod, err := client.CollectOperations(ctx, win.GlobalFrom, win.To, func(current, total int) {
			fyne.Do(func() { d.setBusyStatus("operations", fmt.Sprintf("%s (%d/%d)", d.tr("loading_ops"), current, total)) })
		})
		if err != nil {
			return err
		}
		d.mu.Lock()
		if d.snapshot == nil {
			d.snapshot = &model.Snapshot{}
		}
		d.snapshot.Operations, d.snapshot.OperationsPeriod = operations, &operationPeriod
		snapshot := d.snapshot
		d.mu.Unlock()
		fromKey, toKey := operationCacheRange(fromText, toText, now)
		if err := saveSnapshotCache(d.operationsCachePath, snapshotCache{UpdatedAt: now, Mode: d.cfg.Mode, From: fromKey, To: toKey, Snapshot: &model.Snapshot{Operations: operations, OperationsPeriod: &operationPeriod}}); err != nil {
			return err
		}
		cols, rows := operationRows(snapshot, d.tr, *d.cfg.TimezoneOffset)
		fyne.Do(func() { d.operations.set(cols, rows) })
		return nil
	})
}

func (d *desktop) showPortfolio() {
	d.portfolio.group.SetSelected(d.tr("col_account"))
	if !d.portfolioVisited {
		d.portfolioVisited = true
		if cached, err := loadSnapshotCache(d.portfolioCachePath); err == nil && cached.Mode == d.cfg.Mode && cached.Snapshot != nil {
			d.mu.Lock()
			d.snapshot = cached.Snapshot
			d.mu.Unlock()
			cols, rows := portfolioRows(cached.Snapshot, d.tr)
			d.portfolio.set(cols, rows)
			if cacheFresh(cached.UpdatedAt, d.cfg.CatalogTTLHours, time.Now()) {
				return
			}
		}
		d.refreshPortfolio()
	}
}

func (d *desktop) showOperations() {
	if !d.operationsVisited {
		d.operationsVisited = true
		now := time.Now()
		from, to := operationCacheRange(dateText(d.from), dateText(d.to), now)
		if cached, err := loadSnapshotCache(d.operationsCachePath); err == nil && cached.Mode == d.cfg.Mode && cached.From == from && cached.To == to && cached.Snapshot != nil {
			d.mu.Lock()
			if d.snapshot == nil {
				d.snapshot = &model.Snapshot{}
			}
			d.snapshot.Operations, d.snapshot.OperationsPeriod = cached.Snapshot.Operations, cached.Snapshot.OperationsPeriod
			snapshot := d.snapshot
			d.mu.Unlock()
			cols, rows := operationRows(snapshot, d.tr, *d.cfg.TimezoneOffset)
			d.operations.set(cols, rows)
			if cacheFresh(cached.UpdatedAt, d.cfg.CatalogTTLHours, now) {
				return
			}
		}
		d.refreshOperations()
	}
}

func (d *desktop) showInstruments() {
	if !d.instrumentsVisited && d.loadInstruments != nil {
		d.instrumentsVisited = true
		d.mu.RLock()
		fresh := d.scache != nil && d.scache.Mode == d.cfg.Mode && d.scache.Fresh("bond", time.Duration(d.cfg.CatalogTTLHours)*time.Hour, time.Now())
		d.mu.RUnlock()
		d.loadInstruments(!fresh)
	}
}
func (d *desktop) exportAll() {
	d.mu.RLock()
	snap := d.snapshot
	d.mu.RUnlock()
	if snap == nil {
		showError(fmt.Errorf("%s", d.tr("refresh_first")), d.window)
		return
	}
	// The report set keeps its own file naming, so only the directory is asked.
	pick := dialog.NewFolderOpen(func(dir fyne.ListableURI, err error) {
		if err != nil {
			showError(err, d.window)
			return
		}
		if dir == nil {
			return // cancelled
		}
		paths, e := report.Write(dir.Path(), time.Now(), snap)
		if e != nil {
			showError(e, d.window)
			return
		}
		dialog.ShowInformation(d.tr("export_title"), strings.Join(paths.All(), "\n"), d.window)
	}, d.window)
	if lister, e := listerFor(d.cfg.ReportsDir); e == nil {
		pick.SetLocation(lister)
	}
	pick.Show()
}

var portfolioFields = []string{"row_kind", "account", "account_id", "type", "ticker", "isin", "name", "currency", "quantity", "avg_price", "current_price", "current_value", "pnl_abs", "pnl_pct", "coupon_rate_pct", "current_yield", "yield_to_maturity", "coupon_frequency", "next_coupon_date", "next_coupon_amount", "issuer_rating", "last_dividend_amount", "dividend_frequency", "next_payment_date", "next_payment_amount", "total_amount", "converted_amount", "converted_currency", "conversion_rate"}

var operationFields = []string{"id", "account_id", "account_name", "datetime", "operation_type", "instrument_type", "ticker", "isin", "name", "quantity", "payment_amount", "payment_currency", "state"}

var instrumentFields = []string{"type", "ticker", "name", "isin", "currency", "exchange", "sector", "risk_level", "coupon_frequency", "coupon_type", "coupon_rate_pct", "next_coupon_date", "dividends", "qual", "nominal", "maturity_date", "last_price", "amortization", "amortization_dates", "offer_dates", "uid", "figi"}

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
			row[26], row[27], row[28] = a.TotalConverted.Amount, a.TotalConverted.Currency, naOr(a.TotalConverted.Rate)
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
		// The rate is absent when the grand total mixed several currencies.
		row[26], row[27], row[28] = s.GrandConverted.Amount, s.GrandConverted.Currency, naOr(s.GrandConverted.Rate)
		rows = append(rows, row)
	}
	return cols, rows
}

// naOr keeps empty optional values readable in the table.
func naOr(value string) string {
	if strings.TrimSpace(value) == "" {
		return model.NA
	}
	return value
}
func operationRows(s *model.Snapshot, tr func(string) string, offset int) ([]string, [][]string) {
	cols := trCols(tr, operationFields...)
	rows := make([][]string, 0, len(s.Operations))
	for _, o := range s.Operations {
		rows = append(rows, []string{o.ID, o.AccountID, o.AccountName, formatTimeZone(o.DateTime, offset), o.Type, o.InstrumentType, o.Ticker, o.ISIN, o.Name, o.Quantity, o.PaymentAmount, o.PaymentCurrency, o.State})
	}
	return cols, rows
}

// formatTimeZone parses an RFC3339 UTC timestamp and formats it in the given
// timezone offset (hours from UTC).  Returns the original string on error.
func formatTimeZone(utc string, offset int) string {
	t, err := time.Parse(time.RFC3339, utc)
	if err != nil {
		return utc
	}
	return t.In(time.FixedZone("", offset*3600)).Format("2006-01-02 15:04:05")
}

// showInstrumentCard opens the details popup. A bond whose events were never
// fetched is enriched on the spot, so the card shows its real amortization and
// call schedule instead of н/д.
func (d *desktop) showInstrumentCard(row []string) {
	uid := fieldOf(row, "uid")
	if uid != "" && fieldOf(row, "type") == "bond" {
		d.busy("instrument-card", d.tr("loading"), func() error {
			if e := d.enrichOne(uid); e != nil {
				return e
			}
			d.mu.RLock()
			item, ok := d.instrumentByUID(uid)
			d.mu.RUnlock()
			if !ok {
				return nil
			}
			_, rows := instrumentRows([]catalog.Instrument{item}, d)
			fyne.Do(func() { d.showCardDialog(rows[0]) })
			return nil
		})
		return
	}
	d.showCardDialog(row)
}

// instrumentURL builds a link to the instrument page on the T-Bank website.
// Accepts ticker and figi as separate parameters since callers use different
// column layouts.
func instrumentURLFrom(ticker, figi string) string {
	if strings.TrimSpace(ticker) != "" && ticker != model.NA {
		return "https://www.tbank.ru/invest/search/?query=" + ticker
	}
	if strings.TrimSpace(figi) != "" && figi != model.NA {
		return "https://www.tbank.ru/invest/catalog/" + figi + "/"
	}
	return ""
}

func instrumentURL(row []string) string {
	return instrumentURLFrom(fieldOf(row, "ticker"), fieldOf(row, "figi"))
}

// openBrowser launches the default browser for the given URL.
func openBrowser(url string) {
	go func() {
		var cmd string
		var args []string
		switch runtime.GOOS {
		case "linux":
			cmd = "xdg-open"
			args = []string{url}
		case "windows":
			cmd = "rundll32"
			args = []string{"url.dll,FileProtocolHandler", url}
		default:
			return
		}
		_ = exec.Command(cmd, args...).Start()
	}()
}

// showCardDialog displays the instrument details in a custom dialog with a
// clickable link to the T-Bank instrument page.
func (d *desktop) showCardDialog(row []string) {
	url := instrumentURL(row)
	var b strings.Builder
	for i, value := range row {
		if i < len(d.instruments.columns) {
			fmt.Fprintf(&b, "%s: %s\n", d.instruments.columns[i], value)
		}
	}
	label := widget.NewLabel(b.String())
	label.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(label)
	if url != "" {
		link := widget.NewHyperlink(d.tr("open_on_site"), nil)
		link.OnTapped = func() { openBrowser(url) }
		content.Add(link)
	}
	dialog.ShowCustom(d.tr("details"), d.tr("close"), content, d.window)
}

// rowField reads a value from a row by its raw field name, using the supplied
// field list (not instrumentFields) so it works with portfolio and operations
// rows as well.
func rowField(row []string, fields []string, name string) string {
	for i, f := range fields {
		if f == name && i < len(row) {
			return row[i]
		}
	}
	return ""
}

// showRowCard opens the instrument card for a row from the portfolio or
// operations table. It finds ticker/isin/name among the row's fields and
// shows the URL link when at least one instrument identifier is present.
func (d *desktop) showRowCard(row []string, fields []string) {
	ticker := rowField(row, fields, "ticker")
	name := rowField(row, fields, "name")
	url := instrumentURLFrom(ticker, rowField(row, fields, "figi"))
	var b strings.Builder
	for i, value := range row {
		if value == "" || value == model.NA {
			continue
		}
		label := ""
		if i < len(fields) {
			label = d.tr("col_" + fields[i])
			if label == "col_"+fields[i] {
				label = fields[i]
			}
		}
		fmt.Fprintf(&b, "%s: %s\n", label, value)
	}
	if name != "" {
		dialogTitle := d.tr("details") + ": " + name
		label := widget.NewLabel(b.String())
		label.Wrapping = fyne.TextWrapWord
		content := container.NewVBox(label)
		if url != "" {
			link := widget.NewHyperlink(d.tr("open_on_site"), nil)
			link.OnTapped = func() { openBrowser(url) }
			content.Add(link)
		}
		dialog.ShowCustom(dialogTitle, d.tr("close"), content, d.window)
	}
}

// fieldOf reads a value out of an instrument row by its raw field name.
func fieldOf(row []string, field string) string {
	for i, f := range instrumentFields {
		if f == field && i < len(row) {
			return row[i]
		}
	}
	return ""
}

func (d *desktop) instrumentByUID(uid string) (catalog.Instrument, bool) {
	if d.scache == nil {
		return catalog.Instrument{}, false
	}
	for _, i := range d.scache.AllInstruments() {
		if i.UID == uid {
			return i, true
		}
	}
	return catalog.Instrument{}, false
}

// enrichOne fetches the coupon/event details of a single instrument.
func (d *desktop) enrichOne(uid string) error {
	d.mu.RLock()
	item, ok := d.instrumentByUID(uid)
	d.mu.RUnlock()
	if !ok || item.Enriched {
		return nil
	}
	client, e := d.client()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()
	enriched, err := client.EnrichCatalog(ctx, []catalog.Instrument{item}, time.Now())
	if err != nil {
		return err
	}
	if len(enriched) == 0 {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, typ := range []string{"bond", "share", "etf", "currency", "future"} {
		seg := d.scache.Segment(typ)
		if seg == nil {
			continue
		}
		for i := range seg.Instruments {
			if seg.Instruments[i].UID == uid {
				seg.Instruments[i] = enriched[0]
				return d.scache.SaveSegmented(d.cachePath)
			}
		}
	}
	return nil
}

func (d *desktop) instrumentTab() fyne.CanvasObject {
	d.instruments.onRow = func(row []string) { d.showInstrumentCard(row) }
	typeOptions := make([]string, 0, len(instrumentTypes)+1)
	typeOptions = append(typeOptions, d.tr("all"))
	for _, t := range instrumentTypes {
		typeOptions = append(typeOptions, d.tr("type_"+t))
	}
	typeSelect := widget.NewSelect(typeOptions, nil)
	typeSelect.SetSelected(d.tr("all"))
	currency := widget.NewSelect(append([]string{d.tr("all")}, currencyOptions(d.scache, "")...), nil)
	currency.SetSelected(d.tr("all"))
	exchange, sector := widget.NewSelect([]string{}, nil), widget.NewSelect([]string{}, nil)
	risk := widget.NewSelect([]string{"", d.tr("risk_low"), d.tr("risk_moderate"), d.tr("risk_high"), d.tr("na")}, nil)
	frequency := widget.NewSelect([]string{"", d.tr("monthly"), d.tr("quarterly"), d.tr("semiannual"), d.tr("annual")}, nil)
	couponType := widget.NewSelect([]string{"", d.tr("fixed"), d.tr("floating")}, nil)
	dividends := widget.NewSelect([]string{"", d.tr("yes"), d.tr("no")}, nil)
	rateFrom, rateTo := widget.NewSelect([]string{}, nil), widget.NewSelect([]string{}, nil)
	maturityFrom, maturityTo := widget.NewSelect([]string{}, nil), widget.NewSelect([]string{}, nil)
	monthNames := append([]string{""}, localizedMonthNames(d)...)
	month := widget.NewSelect(monthNames, nil)
	updated := widget.NewLabel("")
	resettingFilters := false
	filter := func() catalog.Filter {
		var dividendFilter *bool
		if dividends.Selected != "" {
			v := dividends.Selected == d.tr("yes")
			dividendFilter = &v
		}
		return catalog.Filter{Type: typeAPI(typeSelect.Selected, d), Query: d.instruments.search.Text, Currency: currencyFilter(currency.Selected, d), Exchange: exchange.Selected, Sector: sector.Selected, Risk: riskAPI(risk.Selected, d), Frequency: frequencyAPI(frequency.Selected, d), CouponType: couponAPI(couponType.Selected, d), RateFrom: catalog.Float(rateFrom.Selected), RateTo: catalog.Float(rateTo.Selected), MaturityFrom: catalog.Date(maturityFrom.Selected), MaturityTo: catalog.Date(maturityTo.Selected), CouponMonth: monthIndex(month.Selected, d), Dividends: dividendFilter}
	}
	applyFilters := func() {
		d.mu.RLock()
		scache := d.scache
		d.mu.RUnlock()
		if scache == nil {
			return
		}
		items := catalog.Search(scache.AllInstruments(), filter())
		cols, rows := instrumentRows(items, d)
		d.instruments.set(cols, rows)
		setOptions := func(selectbox *widget.Select, values []string) {
			selected := selectbox.Selected
			selectbox.Options = append([]string{""}, values...)
			if selected != "" && !slices.Contains(selectbox.Options, selected) {
				selectbox.Options = append(selectbox.Options, selected)
			}
			selectbox.Refresh()
		}
		facet := func(ignore string) []catalog.Instrument {
			f := filter()
			switch ignore {
			case "type":
				f.Type = ""
			case "currency":
				f.Currency = ""
			case "exchange":
				f.Exchange = ""
			case "sector":
				f.Sector = ""
			case "risk":
				f.Risk = ""
			case "frequency":
				f.Frequency = 0
			case "coupon":
				f.CouponType = ""
			case "dividends":
				f.Dividends = nil
			case "from":
				f.RateFrom = nil
			case "to":
				f.RateTo = nil
			case "maturity_from":
				f.MaturityFrom = nil
			case "maturity_to":
				f.MaturityTo = nil
			case "month":
				f.CouponMonth = 0
			}
			return catalog.Search(scache.AllInstruments(), f)
		}
		values := func(items []catalog.Instrument, field func(catalog.Instrument) string) []string {
			set := map[string]bool{}
			for _, item := range items {
				if value := field(item); value != "" {
					set[value] = true
				}
			}
			out := make([]string, 0, len(set))
			for value := range set {
				out = append(out, value)
			}
			sort.Strings(out)
			return out
		}
		setOptions(typeSelect, append([]string{d.tr("all")}, values(facet("type"), func(item catalog.Instrument) string { return d.tr("type_" + item.Type) })...))
		setOptions(currency, append([]string{d.tr("all")}, values(facet("currency"), func(item catalog.Instrument) string { return strings.ToUpper(item.Currency) })...))
		setOptions(exchange, values(facet("exchange"), func(item catalog.Instrument) string { return item.Exchange }))
		setOptions(sector, values(facet("sector"), func(item catalog.Instrument) string { return item.Sector }))
		setOptions(risk, values(facet("risk"), func(item catalog.Instrument) string {
			return d.tr("risk_" + strings.ToLower(strings.TrimPrefix(item.RiskLevel, "RISK_LEVEL_")))
		}))
		setOptions(frequency, values(facet("frequency"), func(item catalog.Instrument) string {
			return d.tr(map[int]string{1: "annual", 2: "semiannual", 4: "quarterly", 12: "monthly"}[item.CouponFrequency])
		}))
		setOptions(couponType, values(facet("coupon"), func(item catalog.Instrument) string {
			if item.FloatingCoupon {
				return d.tr("floating")
			}
			return d.tr("fixed")
		}))
		setOptions(dividends, values(facet("dividends"), func(item catalog.Instrument) string {
			if item.HasDividends {
				return d.tr("yes")
			}
			return d.tr("no")
		}))
		setOptions(month, values(facet("month"), func(item catalog.Instrument) string {
			if t, e := time.Parse("2006-01-02", item.NextCouponDate[:min(10, len(item.NextCouponDate))]); e == nil {
				return d.tr(monthKeys[int(t.Month())-1])
			}
			return ""
		}))
		rates := values(facet("from"), func(item catalog.Instrument) string {
			if item.CouponRatePct == nil {
				return ""
			}
			return strconv.FormatFloat(*item.CouponRatePct, 'f', -1, 64)
		})
		sort.Slice(rates, func(i, j int) bool {
			a, _ := strconv.ParseFloat(rates[i], 64)
			b, _ := strconv.ParseFloat(rates[j], 64)
			return a < b
		})
		if rateTo.Selected != "" {
			limit, _ := strconv.ParseFloat(rateTo.Selected, 64)
			rates = slices.DeleteFunc(rates, func(v string) bool { x, _ := strconv.ParseFloat(v, 64); return x > limit })
		}
		setOptions(rateFrom, rates)
		rates = values(facet("to"), func(item catalog.Instrument) string {
			if item.CouponRatePct == nil {
				return ""
			}
			return strconv.FormatFloat(*item.CouponRatePct, 'f', -1, 64)
		})
		sort.Slice(rates, func(i, j int) bool {
			a, _ := strconv.ParseFloat(rates[i], 64)
			b, _ := strconv.ParseFloat(rates[j], 64)
			return a < b
		})
		if rateFrom.Selected != "" {
			limit, _ := strconv.ParseFloat(rateFrom.Selected, 64)
			rates = slices.DeleteFunc(rates, func(v string) bool { x, _ := strconv.ParseFloat(v, 64); return x < limit })
		}
		setOptions(rateTo, rates)
		maturities := values(facet("maturity_from"), func(item catalog.Instrument) string { return maturityDay(item.MaturityDate, d) })
		maturities = slices.DeleteFunc(maturities, func(value string) bool { return value == d.tr("na") })
		if maturityTo.Selected != "" {
			maturities = slices.DeleteFunc(maturities, func(value string) bool { return value > maturityTo.Selected })
		}
		setOptions(maturityFrom, maturities)
		maturities = values(facet("maturity_to"), func(item catalog.Instrument) string { return maturityDay(item.MaturityDate, d) })
		maturities = slices.DeleteFunc(maturities, func(value string) bool { return value == d.tr("na") })
		if maturityFrom.Selected != "" {
			maturities = slices.DeleteFunc(maturities, func(value string) bool { return value < maturityFrom.Selected })
		}
		setOptions(maturityTo, maturities)
		updated.SetText(d.tr("catalog_updated") + ": " + scache.Segment("bond").UpdatedAt.In(time.FixedZone("", *d.cfg.TimezoneOffset*3600)).Format("2006-01-02 15:04:05"))
	}
	load := func(force bool) {
		currentFilter := filter()
		if !force {
			applyFilters()
			d.mu.RLock()
			sc := d.scache
			d.mu.RUnlock()
			if sc == nil || !needsInstrumentEnrichment(currentFilter) || catalogEnriched(sc.AllInstruments()) {
				return
			}
			d.busy(instrumentEnrichmentKey(currentFilter), d.tr("loading"), func() error {
				client, err := d.client()
				if err != nil {
					return err
				}
				base := enrichmentFilter(currentFilter)
				ctx, cancel := context.WithTimeout(context.Background(), d.instrumentRefreshTimeout())
				defer cancel()
				enriched, err := client.EnrichCatalog(ctx, catalog.Search(sc.AllInstruments(), base), time.Now())
				byUID := make(map[string]catalog.Instrument, len(enriched))
				for _, item := range enriched {
					byUID[item.UID] = item
				}
				d.mu.Lock()
				for _, typ := range []string{"bond", "share", "etf", "currency", "future"} {
					seg := d.scache.Segment(typ)
					if seg == nil {
						continue
					}
					for i := range seg.Instruments {
						if item, ok := byUID[seg.Instruments[i].UID]; ok {
							seg.Instruments[i] = item
						}
					}
				}
				err = d.scache.SaveSegmented(d.cachePath)
				d.mu.Unlock()
				fyne.Do(applyFilters)
				if err != nil {
					return d.catalogError(err)
				}
				return nil
			})
			return
		}
		// Force refresh: type-specific or "Все".
		// Cached rows remain useful while a stale network refresh is running.
		fyne.Do(applyFilters)
		typ := currentFilter.Type // "" means "Все"
		if typ == "" {
			d.busy("instruments-refresh-all", d.tr("loading"), func() error {
				defer fyne.Do(applyFilters)
				client, err := d.client()
				if err != nil {
					return err
				}
				now := time.Now()
				var muErrs sync.Mutex
				var failed []string
				var enrichmentFailures int
				var saveErr error
				ctx, cancel := context.WithTimeout(context.Background(), d.instrumentRefreshTimeout())
				defer cancel()
				client.Catalog(ctx, now, func(kind string, items []catalog.Instrument, err error) {
					if err != nil {
						muErrs.Lock()
						failed = append(failed, kind)
						muErrs.Unlock()
						return
					}
					if kind == "bond" && len(items) > 0 {
						enriched, e := client.EnrichCatalog(ctx, catalog.Search(items, catalog.Filter{Type: "bond"}), now)
						byUID := make(map[string]catalog.Instrument, len(enriched))
						for _, it := range enriched {
							byUID[it.UID] = it
						}
						for i := range items {
							if it, ok := byUID[items[i].UID]; ok {
								items[i] = it
							}
						}
						if e != nil {
							muErrs.Lock()
							failed = append(failed, kind)
							var enrichmentError *tinvest.EnrichmentError
							if errors.As(e, &enrichmentError) {
								enrichmentFailures += enrichmentError.Failed
							}
							muErrs.Unlock()
						}
					}
					d.mu.Lock()
					if d.scache == nil {
						d.scache = &catalog.SegmentedCache{}
					}
					d.scache.Mode = d.cfg.Mode
					d.scache.Set(kind, items, now)
					if err := d.scache.SaveSegmented(d.cachePath); err != nil && saveErr == nil {
						saveErr = err
					}
					d.mu.Unlock()
				})
				if saveErr != nil {
					return saveErr
				}
				if len(failed) > 0 {
					return d.failedInstrumentTypesError(failed, ctx.Err(), enrichmentFailures)
				}
				return nil
			})
		} else {
			d.busy("instruments-refresh-"+typ, d.tr("loading"), func() error {
				defer fyne.Do(applyFilters)
				client, err := d.client()
				if err != nil {
					return err
				}
				now := time.Now()
				ctx, cancel := context.WithTimeout(context.Background(), d.instrumentRefreshTimeout())
				defer cancel()
				var catErr error
				var catItems []catalog.Instrument
				client.Catalog(ctx, now, func(kind string, items []catalog.Instrument, err error) {
					if kind != typ {
						return
					}
					catErr = err
					catItems = items
				})
				if catErr != nil {
					return d.catalogError(catErr)
				}
				if typ == "bond" && len(catItems) > 0 {
					enriched, e := client.EnrichCatalog(ctx, catalog.Search(catItems, catalog.Filter{Type: "bond"}), now)
					byUID := make(map[string]catalog.Instrument, len(enriched))
					for _, it := range enriched {
						byUID[it.UID] = it
					}
					for i := range catItems {
						if it, ok := byUID[catItems[i].UID]; ok {
							catItems[i] = it
						}
					}
					if e != nil {
						catErr = e
					}
				}
				d.mu.Lock()
				if d.scache == nil {
					d.scache = &catalog.SegmentedCache{}
				}
				d.scache.Mode = d.cfg.Mode
				d.scache.Set(typ, catItems, now)
				if err := d.scache.SaveSegmented(d.cachePath); err != nil {
					d.mu.Unlock()
					return err
				}
				d.mu.Unlock()
				return d.catalogError(catErr)
			})
		}
	}
	d.loadInstruments = load
	d.instrumentFilterSelects = []*widget.Select{typeSelect, currency, exchange, sector, risk, frequency, couponType, dividends, rateFrom, rateTo, maturityFrom, maturityTo, month}
	for _, s := range d.instrumentFilterSelects {
		s.OnChanged = func(string) {
			if !resettingFilters && d.loadInstruments != nil {
				d.loadInstruments(false)
			}
		}
	}
	d.resetInstrumentFilters = func() {
		resettingFilters = true
		defer func() {
			resettingFilters = false
			applyFilters()
		}()
		for index, selectbox := range d.instrumentFilterSelects {
			selected := ""
			if index < 2 {
				selected = d.tr("all")
			}
			selectbox.SetSelected(selected)
		}
	}
	bar := d.tableBar(d.instruments, &d.cfg.FontScaleInstruments, func() { load(true) }, func() { d.instruments.exportView(d.cfg.ReportsDir) }, d.resetInstrumentsView)
	if d.scache != nil {
		seg := d.scache.Segment("bond")
		if seg != nil {
			updated.SetText(d.tr("catalog_updated") + ": " + seg.UpdatedAt.In(time.FixedZone("", *d.cfg.TimezoneOffset*3600)).Format("2006-01-02 15:04:05"))
		}
	}
	// Search and grouping now belong to the instrument toolbar, so keep only
	// filters and the table in the grid body. This removes the former reserved
	// vertical rows between the tab strip and the controls.
	d.instruments.root = container.NewBorder(d.instruments.filterPanel, nil, nil, nil, d.instruments.table)
	return container.NewBorder(container.NewVBox(bar, updated), nil, nil, nil, d.instruments.root)
}

func needsInstrumentEnrichment(f catalog.Filter) bool {
	return f.Type == "bond" || f.RateFrom != nil || f.RateTo != nil || f.MaturityFrom != nil || f.MaturityTo != nil || f.CouponMonth > 0 || f.Dividends != nil
}

func catalogEnriched(items []catalog.Instrument) bool {
	for _, item := range items {
		if !item.Enriched {
			return false
		}
	}
	return true
}

// enrichmentFilter keeps only fields known before the deferred API calls.
// Coupon rates, dividends, and effective maturity depend on enrichment, so
// applying them beforehand can incorrectly exclude a matching instrument.
func enrichmentFilter(f catalog.Filter) catalog.Filter {
	f.RateFrom, f.RateTo = nil, nil
	f.MaturityFrom, f.MaturityTo = nil, nil
	f.Dividends, f.CouponMonth = nil, 0
	return f
}

// instrumentEnrichmentKey only coalesces identical enrichment requests. A
// later filter with a different candidate set must run, otherwise its rows can
// be evaluated against incomplete reference data.
func instrumentEnrichmentKey(f catalog.Filter) string {
	date := func(value *time.Time) string {
		if value == nil {
			return ""
		}
		return value.Format("2006-01-02")
	}
	dividends := ""
	if f.Dividends != nil {
		dividends = strconv.FormatBool(*f.Dividends)
	}
	return strings.Join([]string{f.Type, f.Query, f.Currency, f.Exchange, f.Sector, f.Risk, f.CouponType, strconv.Itoa(f.Frequency), strconv.Itoa(f.CouponMonth), date(f.MaturityFrom), date(f.MaturityTo), dividends}, "\x00")
}
func atoi(s string) int { v, _ := strconv.Atoi(s); return v }

var monthKeys = []string{"month_jan", "month_feb", "month_mar", "month_apr", "month_may", "month_jun", "month_jul", "month_aug", "month_sep", "month_oct", "month_nov", "month_dec"}

func localizedMonthNames(d *desktop) []string {
	out := make([]string, len(monthKeys))
	for i, k := range monthKeys {
		out[i] = d.tr(k)
	}
	return out
}

// monthIndex returns the 1-based month number from a localized month label,
// or 0 when the label is empty (no filter).
func monthIndex(label string, d *desktop) int {
	for i, k := range monthKeys {
		if label == d.tr(k) {
			return i + 1
		}
	}
	return 0
}
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
	return ""
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
		qual := "?"
		if i.ForQualInvestor != nil {
			qual = strconv.FormatBool(*i.ForQualInvestor)
		}
		rows = append(rows, []string{i.Type, i.Ticker, i.Name, i.ISIN, i.Currency, i.Exchange, i.Sector, risk, freq, ct, rate,
			naIfEmpty(i.NextCouponDate, d), div, qual, naIfEmpty(i.Nominal, d), maturityDay(i.MaturityDate, d), naIfEmpty(i.LastPrice, d),
			amortizationLabel(i, d), dateList(i.AmortizationDates, d), dateList(i.OfferDates, d), i.UID, i.FIGI})
	}
	return cols, rows
}

func naIfEmpty(value string, d *desktop) string {
	if strings.TrimSpace(value) == "" {
		return d.tr("na")
	}
	return value
}

func maturityDay(raw string, d *desktop) string {
	if t, e := time.Parse(time.RFC3339, raw); e == nil {
		return t.Format("2006-01-02")
	}
	return naIfEmpty(raw, d)
}

// amortizationLabel answers "does this bond amortize?" from the directory flag;
// the schedule itself only arrives with enrichment.
func amortizationLabel(i catalog.Instrument, d *desktop) string {
	if i.Type != "bond" {
		return d.tr("na")
	}
	if i.Amortized {
		return d.tr("yes")
	}
	return d.tr("no")
}

func dateList(dates []string, d *desktop) string {
	if len(dates) == 0 {
		return d.tr("na")
	}
	return strings.Join(dates, ", ")
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
	ttl := widget.NewEntry()
	ttl.SetText(strconv.Itoa(d.cfg.CatalogTTLHours))
	loadTimeout := widget.NewEntry()
	loadTimeout.SetText(strconv.Itoa(d.cfg.InstrumentLoadTimeoutSeconds))
	portfolioTimeout := widget.NewEntry()
	portfolioTimeout.SetText(strconv.Itoa(d.cfg.PortfolioLoadTimeoutSeconds))
	lang := widget.NewSelect([]string{"ru", "en"}, nil)
	lang.SetSelected(d.cfg.Language)
	tzOptions := make([]string, 0, 47)
	tzLabels := make(map[string]int, 47)
	for off := -23; off <= 23; off++ {
		label := "UTC"
		if off >= 0 {
			label += "+" + strconv.Itoa(off)
		} else {
			label += strconv.Itoa(off)
		}
		tzOptions = append(tzOptions, label)
		tzLabels[label] = off
	}
	tz := widget.NewSelect(tzOptions, nil)
	tz.SetSelected("UTC+" + strconv.Itoa(*d.cfg.TimezoneOffset))
	if *d.cfg.TimezoneOffset < 0 {
		tz.SetSelected("UTC" + strconv.Itoa(*d.cfg.TimezoneOffset))
	}
	// TLS settings
	tlsCAFile := widget.NewEntry()
	tlsCAFile.SetText(d.cfg.TLS_CA_File)
	tlsCAFile.SetPlaceHolder("/usr/local/share/ca-certificates/russian-trusted/russian_trusted_sub_ca_pem.crt")
	tlsCAHint := widget.NewLabelWithStyle("ℹ️ " + d.tr("tls_ca_hint"), fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	tlsCAFileBox := container.NewBorder(nil, tlsCAHint, nil, nil, tlsCAFile)
	tlsInsecure := widget.NewCheck(d.tr("tls_insecure_hint"), nil)
	tlsInsecure.SetChecked(d.cfg.TLS_Insecure_Skip_Verify)
	tlsForm := widget.NewForm(
		widget.NewFormItem(d.tr("tls_ca_file"), tlsCAFileBox),
		widget.NewFormItem(d.tr("tls_insecure_skip_verify"), tlsInsecure),
	)
	form := widget.NewForm(widget.NewFormItem(d.tr("mode"), mode), widget.NewFormItem(d.tr("token_env"), tokenEnv), widget.NewFormItem(d.tr("token_value"), token), widget.NewFormItem(d.tr("reports"), reports), widget.NewFormItem(d.tr("target_currency"), target), widget.NewFormItem(d.tr("retries"), retries), widget.NewFormItem(d.tr("retry_delay"), delay), widget.NewFormItem(d.tr("catalog_ttl"), ttl), widget.NewFormItem(d.tr("instrument_load_timeout"), loadTimeout), widget.NewFormItem(d.tr("portfolio_load_timeout"), portfolioTimeout), widget.NewFormItem(d.tr("language"), lang), widget.NewFormItem(d.tr("timezone"), tz))
	form.OnSubmit = func() {
		c := *d.cfg
		c.Mode = mode.Selected
		c.TokenEnv = tokenEnv.Text
		c.Token = token.Text
		c.ReportsDir = reports.Text
		c.TargetCurrency = targetCurrencyValue(target.Selected, d)
		c.Retries = atoi(retries.Text)
		c.RetryDelayMs = atoi(delay.Text)
		c.CatalogTTLHours = atoi(ttl.Text)
		c.InstrumentLoadTimeoutSeconds = atoi(loadTimeout.Text)
		c.PortfolioLoadTimeoutSeconds = atoi(portfolioTimeout.Text)
		c.Language = lang.Selected
		if off, ok := tzLabels[tz.Selected]; ok {
			c.TimezoneOffset = &off
		}
		if e := c.Save(d.configPath); e != nil {
			showError(e, d.window)
			return
		}
		d.cfg = &c
		d.loadText()
		d.build()
		dialog.ShowInformation(d.tr("settings"), d.tr("saved"), d.window)
	}
	tlsForm.OnSubmit = func() {
		c := *d.cfg
		c.TLS_CA_File = tlsCAFile.Text
		c.TLS_Insecure_Skip_Verify = tlsInsecure.Checked
		if e := c.Save(d.configPath); e != nil {
			showError(e, d.window)
			return
		}
		d.cfg = &c
		d.loadText()
		d.build()
		dialog.ShowInformation(d.tr("settings"), d.tr("saved"), d.window)
	}
	tlsApplyBtn := widget.NewButton(d.tr("apply"), func() {
		tlsForm.OnSubmit()
	})
	return container.NewVBox(
		container.New(widthFraction{frac: 0.98}, form),
		widget.NewSeparator(),
		container.New(widthFraction{frac: 0.98}, tlsForm),
		container.NewBorder(nil, nil, nil, nil, tlsApplyBtn),
	)
}

func (d *desktop) targetCurrencyOptions() []string {
	return append([]string{d.tr("no_conversion")}, currencyOptions(d.scache, d.cfg.TargetCurrency)...)
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
