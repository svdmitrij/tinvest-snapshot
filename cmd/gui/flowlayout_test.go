//go:build !ci

package main

import (
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	desktopdriver "fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/dmitry/tinvest-snapshot/internal/config"
)

func buttonLabels(row *fyne.Container) []string {
	labels := []string{}
	for _, object := range row.Objects {
		if button, ok := object.(*widget.Button); ok {
			labels = append(labels, button.Text)
		}
	}
	return labels
}

func testDesktop() *desktop {
	d := &desktop{cfg: &config.Config{Language: "ru"}}
	d.loadText()
	return d
}

func labelCount(o fyne.CanvasObject) int {
	switch v := o.(type) {
	case *widget.Label:
		return 1
	case *fyne.Container:
		n := 0
		for _, child := range v.Objects {
			n += labelCount(child)
		}
		return n
	}
	return 0
}

func assertSearchControl(t *testing.T, g *grid, sample, tooltip string) {
	t.Helper()
	block, ok := g.searchBlock.(*fyne.Container)
	if !ok || len(block.Objects) != 2 {
		t.Fatalf("search block = %T with %d objects, want field and icon button", g.searchBlock, len(block.Objects))
	}
	field, ok := block.Objects[0].(*fyne.Container)
	if !ok {
		t.Fatalf("search field wrapper = %T, want fixed-width container", block.Objects[0])
	}
	fixed, ok := field.Layout.(*fixedWidthLayout)
	if !ok || fixed.width < searchFieldWidth() {
		t.Fatalf("search field width = %#v, want at least %v", field.Layout, searchFieldWidth())
	}
	if g.findNextButton.Text != "" || g.findNextButton.Icon == nil || g.findNextButton.Icon.Name() != theme.SearchIcon().Name() {
		t.Fatalf("find-next button = text %q, icon %v, want icon-only search button", g.findNextButton.Text, g.findNextButton.Icon)
	}
	if size := g.findNextButton.MinSize(); size.Width != size.Height {
		t.Fatalf("find-next button min size = %v, want standard square icon button", size)
	}
	if g.findNextButton.tooltip != tooltip {
		t.Fatalf("find-next tooltip = %q, want %q", g.findNextButton.tooltip, tooltip)
	}
	g.search.SetText(sample)
	if g.search.Text != sample {
		t.Fatalf("search text = %q, want all 20 characters", g.search.Text)
	}
}

func TestTooltipButtonShowsLocalizedDescription(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	w := test.NewWindow(nil)
	defer w.Close()
	b := newTooltipButton(theme.SearchIcon(), "Find next", func() {})
	w.SetContent(b)
	b.MouseIn(&desktopdriver.MouseEvent{})
	if b.popup == nil || !b.popup.Visible() {
		t.Fatal("tooltip must be visible while the pointer is over the icon button")
	}
	b.MouseOut()
	if b.popup != nil {
		t.Fatal("tooltip must close when the pointer leaves the icon button")
	}
}

func snapshot(t *testing.T, w fyne.Window, name string) {
	t.Helper()
	dir := os.Getenv("FILTER_PANEL_SNAPSHOT_DIR")
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, w.Canvas().Capture()); err != nil {
		t.Fatalf("snapshot encode: %v", err)
	}
}

func TestFlowLayoutWrapsOnlyWhenRowIsFull(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	pad := theme.Padding()
	blockWidth, blockHeight := float32(200), float32(30)
	blocks := make([]fyne.CanvasObject, 3)
	for i := range blocks {
		r := canvas.NewRectangle(color.Opaque)
		r.SetMinSize(fyne.NewSize(blockWidth, blockHeight))
		blocks[i] = r
	}
	width := 2*blockWidth + pad + 10 // room for exactly two blocks per line
	f := &flowLayout{widthFn: func() float32 { return width + flowWidthAllowance }}

	f.Layout(blocks, fyne.NewSize(width, 100))
	if y0, y1 := blocks[0].Position().Y, blocks[1].Position().Y; y1 != y0 {
		t.Fatalf("second block wrapped although the line had room: y=%v", y1)
	}
	if blocks[2].Position().Y <= blocks[0].Position().Y {
		t.Fatal("third block must move to the next line when the width is exhausted")
	}
	if x := blocks[2].Position().X; x != 0 {
		t.Fatalf("wrapped block must start at the left edge, got x=%v", x)
	}

	if got, want := f.MinSize(blocks).Height, 2*blockHeight+pad; got != want {
		t.Fatalf("min height = %v, want %v (two lines)", got, want)
	}
	wide := &flowLayout{widthFn: func() float32 { return 3*blockWidth + 2*pad + 10 + flowWidthAllowance }}
	if got := wide.MinSize(blocks).Height; got != blockHeight {
		t.Fatalf("single line min height = %v, want %v", got, blockHeight)
	}
}

func TestInstrumentToolbarKeepsBlocksOrderedAndAdaptive(t *testing.T) {
	for _, language := range []string{"ru", "en"} {
		t.Run(language, func(t *testing.T) {
			a := test.NewApp()
			defer a.Quit()
			w := test.NewWindow(nil)
			defer w.Close()
			d := &desktop{cfg: &config.Config{Language: language, FontScaleInstruments: 100}, window: w}
			d.loadText()
			d.instruments = newGrid(w, d.tr, d, 100)
			if language == "ru" {
				assertSearchControl(t, d.instruments, searchSampleRU, "Найти далее")
			} else {
				assertSearchControl(t, d.instruments, searchSampleEN, "Find next")
			}
			tab := d.instrumentTab()
			w.SetContent(tab)
			w.Resize(fyne.NewSize(1600, 900))

			top, ok := tab.(*fyne.Container).Objects[1].(*fyne.Container)
			if !ok {
				t.Fatalf("toolbar parent type = %T, want container", tab.(*fyne.Container).Objects[1])
			}
			toolbar, ok := top.Objects[0].(*fyne.Container)
			if !ok {
				t.Fatalf("toolbar type = %T, want container", top.Objects[0])
			}
			if _, ok := toolbar.Layout.(*flowLayout); !ok {
				t.Fatalf("toolbar layout = %T, want flowLayout", toolbar.Layout)
			}
			if len(toolbar.Objects) != 6 {
				t.Fatalf("toolbar blocks = %d, want refresh, export, scale, search, group, reset", len(toolbar.Objects))
			}
			if got := toolbar.Objects[0].(*widget.Button).Text; got != d.tr("refresh") {
				t.Fatalf("first toolbar block = %q, want %q", got, d.tr("refresh"))
			}
			if got := toolbar.Objects[1].(*widget.Button).Text; got != d.tr("export") {
				t.Fatalf("second toolbar block = %q, want %q", got, d.tr("export"))
			}
			if got := toolbar.Objects[5].(*widget.Button).Text; got != d.tr("reset_all") {
				t.Fatalf("last toolbar block = %q, want %q", got, d.tr("reset_all"))
			}
			for i, block := range toolbar.Objects[1:] {
				if block.Position().Y != toolbar.Objects[0].Position().Y {
					t.Fatalf("wide toolbar block %d wrapped unexpectedly", i+1)
				}
			}

			w.Resize(fyne.NewSize(640, 480))
			lastY := toolbar.Objects[0].Position().Y
			for i, block := range toolbar.Objects {
				if block.Position().Y < lastY {
					t.Fatalf("toolbar block %d moved before its predecessor", i)
				}
				if edge := block.Position().X + block.Size().Width; edge > toolbar.Size().Width+0.5 {
					t.Fatalf("toolbar block %d overflows: right edge %v > width %v", i, edge, toolbar.Size().Width)
				}
				lastY = block.Position().Y
			}

			w.Resize(fyne.NewSize(300, 480))
			wrapped := false
			for _, block := range toolbar.Objects {
				if block.Position().Y > toolbar.Objects[0].Position().Y {
					wrapped = true
				}
			}
			if !wrapped {
				t.Fatal("toolbar must wrap when the available width is exhausted")
			}
		})
	}
}

func TestTablePanelsUseUnifiedControlsAndLocalizedPlaceholders(t *testing.T) {
	for _, language := range []string{"ru", "en"} {
		t.Run(language, func(t *testing.T) {
			groupPlaceholder, fromPlaceholder, toPlaceholder := "Группировать по", "Период с", "Период по"
			if language == "en" {
				groupPlaceholder, fromPlaceholder, toPlaceholder = "Group by", "Period from", "Period to"
			}
			a := test.NewApp()
			defer a.Quit()
			w := test.NewWindow(nil)
			defer w.Close()
			d := &desktop{cfg: &config.Config{Language: language, FontScalePortfolio: 100, FontScaleOperations: 100}, window: w}
			d.loadText()
			portfolio := newGrid(w, d.tr, d, 100)
			operations := newGrid(w, d.tr, d, 100)
			d.from, d.to = widget.NewDateEntry(), widget.NewDateEntry()
			d.from.SetPlaceHolder(d.tr("from"))
			d.to.SetPlaceHolder(d.tr("to"))

			portfolioBar := d.tableBar(portfolio, &d.cfg.FontScalePortfolio, func() {}, func() {}, portfolio.reset)
			operationsBar := d.tableBar(operations, &d.cfg.FontScaleOperations, func() {}, func() {}, operations.reset,
				container.NewGridWrap(dateSize, d.from), container.NewGridWrap(dateSize, d.to))
			for name, bar := range map[string]*fyne.Container{"portfolio": portfolioBar, "operations": operationsBar} {
				if _, ok := bar.Layout.(*flowLayout); !ok {
					t.Fatalf("%s bar layout = %T, want flowLayout", name, bar.Layout)
				}
				if got := bar.Objects[len(bar.Objects)-1].(*widget.Button).Text; got != d.tr("reset_all") {
					t.Fatalf("%s reset button = %q, want %q", name, got, d.tr("reset_all"))
				}
				bar.Resize(fyne.NewSize(1600, 200))
				firstY := bar.Objects[0].Position().Y
				for i, block := range bar.Objects {
					if block.Position().Y != firstY {
						t.Fatalf("%s block %d wrapped on a wide panel", name, i)
					}
				}
				bar.Resize(fyne.NewSize(640, 480))
				for i, block := range bar.Objects {
					if edge := block.Position().X + block.Size().Width; edge > bar.Size().Width+0.5 {
						t.Fatalf("%s block %d overflows at 640px", name, i)
					}
				}
			}

			for name, g := range map[string]*grid{"portfolio": portfolio, "operations": operations} {
				sample := searchSampleRU
				tooltip := "Найти далее"
				if language == "en" {
					sample = searchSampleEN
					tooltip = "Find next"
				}
				assertSearchControl(t, g, sample, tooltip)
				if g.search.PlaceHolder != d.tr("search") {
					t.Fatalf("%s search placeholder = %q, want %q", name, g.search.PlaceHolder, d.tr("search"))
				}
				if g.group.PlaceHolder != groupPlaceholder {
					t.Fatalf("%s group placeholder = %q, want %q", name, g.group.PlaceHolder, groupPlaceholder)
				}
				if labelCount(g.searchBlock) != 0 || labelCount(g.groupBlock) != 0 {
					t.Fatalf("%s toolbar contains an external search or group label", name)
				}
				g.group.Options = []string{"", "Name"}
				g.group.SetSelected("Name")
				if g.group.Selected != "Name" {
					t.Fatalf("%s group selection was not retained", name)
				}
				g.group.ClearSelected()
				if g.group.Selected != "" {
					t.Fatalf("%s group selection was not cleared", name)
				}
			}

			if d.from.PlaceHolder != fromPlaceholder || d.to.PlaceHolder != toPlaceholder {
				t.Fatalf("date placeholders = %q, %q, want %q, %q", d.from.PlaceHolder, d.to.PlaceHolder, fromPlaceholder, toPlaceholder)
			}
			selected := time.Date(2026, time.July, 24, 0, 0, 0, 0, time.UTC)
			d.from.SetDate(&selected)
			d.to.SetDate(&selected)
			if d.from.Text == "" || d.to.Text == "" {
				t.Fatal("selected dates must replace their placeholders")
			}
			d.from.SetDate(nil)
			d.to.SetDate(nil)
			if d.from.Text != "" || d.to.Text != "" {
				t.Fatal("cleared dates must restore their placeholders")
			}
		})
	}
}

func TestGridResetRestoresCurrentTabView(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	d := testDesktop()
	w := test.NewWindow(nil)
	defer w.Close()
	g := newGrid(w, d.tr, d, 100)
	g.set([]string{"Name", "Kind"}, [][]string{{"Alpha", "A"}, {"Beta", "B"}, {"Gamma", "A"}})
	g.group.SetSelected("Kind")
	g.collapsed["Kind: A"] = true
	g.search.SetText("Alpha")
	g.matchIndex = 1
	row := g.filters[0]
	row.column.SetSelected("Kind")
	row.operation.SetSelected("=")
	row.value.SetText("A")
	g.addFilterRow()

	g.reset()
	if g.search.Text != "" || g.group.Selected != "" || g.matchIndex != -1 || len(g.collapsed) != 0 {
		t.Fatal("reset must clear search, grouping, navigation, and collapsed groups")
	}
	if len(g.filters) != 1 || g.filters[0].column.Selected != "" || g.filters[0].operation.Selected != "" || g.filters[0].value.Text != "" {
		t.Fatal("reset must leave exactly one empty filter row")
	}
	if len(g.visible) != len(g.all) {
		t.Fatalf("reset visible rows = %d, want %d", len(g.visible), len(g.all))
	}

	g.reset()
	if len(g.visible) != len(g.all) || len(g.filters) != 1 {
		t.Fatal("repeated reset must keep the neutral view unchanged")
	}
}

func TestEmptySearchButtonClearsNavigation(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	d := testDesktop()
	w := test.NewWindow(nil)
	defer w.Close()
	g := newGrid(w, d.tr, d, 100)
	g.set([]string{"Name"}, [][]string{{"Alpha"}, {"Beta"}})
	g.search.SetText("Alpha")
	g.findNext()
	g.search.SetText("")
	g.findNextButton.OnTapped()
	if g.matchIndex != -1 || g.navigating {
		t.Fatal("empty search action must clear navigation state")
	}
	if len(g.visible) != len(g.all) {
		t.Fatalf("empty search visible rows = %d, want full view %d", len(g.visible), len(g.all))
	}
}

func TestResetButtonsAreLocalAndDoNotLoadData(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	w := test.NewWindow(nil)
	defer w.Close()
	d := &desktop{cfg: &config.Config{Language: "ru", FontScalePortfolio: 100, FontScaleOperations: 100, FontScaleInstruments: 100}, window: w}
	d.loadText()
	d.portfolio = newGrid(w, d.tr, d, 100)
	d.operations = newGrid(w, d.tr, d, 100)
	d.instruments = newGrid(w, d.tr, d, 100)
	for _, g := range []*grid{d.portfolio, d.operations, d.instruments} {
		g.set([]string{"Name"}, [][]string{{"Alpha"}, {"Beta"}})
	}
	d.from, d.to = widget.NewDateEntry(), widget.NewDateEntry()
	selected := time.Date(2026, time.July, 24, 0, 0, 0, 0, time.UTC)
	d.from.SetDate(&selected)
	d.to.SetDate(&selected)

	filtersReset, loads := 0, 0
	d.resetInstrumentFilters = func() { filtersReset++ }
	d.loadInstruments = func(bool) { loads++ }
	portfolioBar := d.tableBar(d.portfolio, &d.cfg.FontScalePortfolio, func() {}, func() {}, d.portfolio.reset)
	operationsBar := d.tableBar(d.operations, &d.cfg.FontScaleOperations, func() {}, func() {}, d.resetOperationsView)
	instrumentsBar := d.tableBar(d.instruments, &d.cfg.FontScaleInstruments, func() {}, func() {}, d.resetInstrumentsView)

	d.portfolio.search.SetText("Alpha")
	d.operations.search.SetText("Beta")
	d.instruments.search.SetText("Alpha")
	portfolioBar.Objects[len(portfolioBar.Objects)-1].(*widget.Button).OnTapped()
	if d.portfolio.search.Text != "" || d.operations.search.Text != "Beta" || d.instruments.search.Text != "Alpha" {
		t.Fatal("portfolio reset must not change other tab state")
	}
	if d.from.Date == nil || d.to.Date == nil {
		t.Fatal("portfolio reset must not clear operation period")
	}

	operationsBar.Objects[len(operationsBar.Objects)-1].(*widget.Button).OnTapped()
	if d.operations.search.Text != "" || d.from.Date != nil || d.to.Date != nil {
		t.Fatal("operations reset must clear only its view and period")
	}
	if d.instruments.search.Text != "Alpha" {
		t.Fatal("operations reset must not change instrument state")
	}

	instrumentsBar.Objects[len(instrumentsBar.Objects)-1].(*widget.Button).OnTapped()
	if d.instruments.search.Text != "" || filtersReset != 1 || loads != 0 {
		t.Fatalf("instrument reset: search=%q filters=%d loads=%d, want empty/1/0", d.instruments.search.Text, filtersReset, loads)
	}
	instrumentsBar.Objects[len(instrumentsBar.Objects)-1].(*widget.Button).OnTapped()
	if filtersReset != 2 || loads != 0 {
		t.Fatalf("repeated instrument reset: filters=%d loads=%d, want 2/0", filtersReset, loads)
	}
}

func TestFilterRowIsCompactSingleLine(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	d := testDesktop()
	w := test.NewWindow(nil)
	defer w.Close()
	g := newGrid(w, d.tr, d, 100)

	if _, ok := g.filterBox.Layout.(*flowLayout); !ok {
		t.Fatalf("filter panel layout = %T, want flow layout", g.filterBox.Layout)
	}
	if n := labelCount(g.filterBox); n != 0 {
		t.Fatalf("filter rows render %d captions, want none", n)
	}
	r := g.filters[0]
	if r.column.PlaceHolder != d.tr("filter_column") {
		t.Fatalf("column placeholder = %q, want %q", r.column.PlaceHolder, d.tr("filter_column"))
	}
	if r.value.PlaceHolder != d.tr("value") {
		t.Fatalf("value placeholder = %q, want %q", r.value.PlaceHolder, d.tr("value"))
	}
	unit, ok := g.filterBox.Objects[0].(*fyne.Container)
	if !ok {
		t.Fatalf("condition row type = %T, want container", g.filterBox.Objects[0])
	}
	if len(unit.Objects) != 5 {
		t.Fatalf("condition row has %d children, want 5 (column, operation, value, +, x)", len(unit.Objects))
	}
	op, ok := unit.Objects[1].(*fyne.Container)
	if !ok {
		t.Fatalf("operation wrapper type = %T, want container", unit.Objects[1])
	}
	fixed, ok := op.Layout.(*fixedWidthLayout)
	if !ok || fixed.width != filterOperationWidth {
		t.Fatalf("operation layout = %#v, want fixed width %v", op.Layout, filterOperationWidth)
	}
}

func TestFilterRowsAreRenderedOnceAndOnlyLastHasAddButton(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	d := testDesktop()
	w := test.NewWindow(nil)
	defer w.Close()
	g := newGrid(w, d.tr, d, 100)

	for cycle := 0; cycle < 3; cycle++ {
		g.removeFilterRow(g.filters[0])
		if len(g.filters) != 1 || len(g.filterBox.Objects) != 1 {
			t.Fatalf("cycle %d: filters=%d rendered=%d, want one", cycle, len(g.filters), len(g.filterBox.Objects))
		}
		g.addFilterRow()
		g.addFilterRow()
		if len(g.filterBox.Objects) != 3 {
			t.Fatalf("cycle %d: rendered rows=%d, want 3", cycle, len(g.filterBox.Objects))
		}
		for i, object := range g.filterBox.Objects {
			row := object.(*fyne.Container)
			buttons := buttonLabels(row)
			if i == len(g.filterBox.Objects)-1 {
				if len(buttons) != 2 || buttons[0] != "+" || buttons[1] != "x" {
					t.Fatalf("last row buttons = %#v, want [+ x]", buttons)
				}
			} else if len(buttons) != 1 || buttons[0] != "x" {
				t.Fatalf("row %d buttons = %#v, want [x]", i, buttons)
			}
		}
		for len(g.filters) > 1 {
			g.removeFilterRow(g.filters[len(g.filters)-1])
		}
	}
}

func TestFilterPanelHasBorderAndMatchesTableRowHeight(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	d := testDesktop()
	w := test.NewWindow(nil)
	defer w.Close()
	g := newGrid(w, d.tr, d, 100)
	w.SetContent(g.root)
	w.Resize(fyne.NewSize(1200, 800))

	if len(g.filterPanel.Objects) != 2 {
		t.Fatalf("filter panel objects = %d, want border and content", len(g.filterPanel.Objects))
	}
	border, ok := g.filterPanel.Objects[0].(*canvas.Rectangle)
	if !ok || border.StrokeWidth < 2 || border.StrokeColor == nil {
		t.Fatalf("filter border = %#v, want visible rectangle", g.filterPanel.Objects[0])
	}
	filterHeight := g.filterBox.Objects[0].Size().Height
	tableHeight := newTableCell(d.tr).MinSize().Height
	if delta := filterHeight - tableHeight; delta < -2 || delta > 2 {
		t.Fatalf("filter row height = %v, table row height = %v", filterHeight, tableHeight)
	}
}

func TestFilterPanelPacksConditionsAndAdaptsToResize(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	d := testDesktop()
	w := test.NewWindow(nil)
	defer w.Close()
	g := newGrid(w, d.tr, d, 100)
	w.SetContent(g.root)

	columns := []string{"Тикер", "Название", "Валюта", "Ставка, %", "Дата погашения", "Квал"}
	rows := [][]string{
		{"SU26240", "ОФЗ 26240", "RUB", "7,10", "2036-07-30", "false"},
		{"RU000A106T36", "Газпром капитал БО-002P", "RUB", "8,90", "2028-09-15", "false"},
		{"RU000A105A95", "ВЭБ.РФ ПБО-002Р-36", "RUB", "9,20", "2027-05-21", "true"},
	}
	g.set(columns, rows)
	for len(g.filters) < 8 {
		g.addFilterRow()
	}
	g.updateDynamicFilters()
	g.filters[0].column.SetSelected("Валюта")
	g.filters[0].operation.SetSelected("=")
	w.Resize(fyne.NewSize(1400, 900))

	units := g.filterBox.Objects
	if len(units) != 8 {
		t.Fatalf("condition count = %d, want 8", len(units))
	}
	firstY := units[0].Position().Y
	if units[1].Position().Y != firstY {
		t.Fatal("at 1400px at least two conditions must share the first line")
	}
	if units[len(units)-1].Position().Y == firstY {
		t.Fatal("eight conditions cannot fit a single line at 1400px, wrapping expected")
	}
	unitHeight := units[0].Size().Height
	if h := g.filterBox.Size().Height; h >= float32(len(units))*unitHeight {
		t.Fatalf("panel height %v is not compact, a vertical stack would take %v", h, float32(len(units))*unitHeight)
	}
	panelWidth := g.filterBox.Size().Width
	for i, u := range units {
		if edge := u.Position().X + u.Size().Width; edge > panelWidth+0.5 {
			t.Fatalf("condition %d overflows the panel: right edge %v > width %v", i, edge, panelWidth)
		}
	}
	snapshot(t, w, "filter-panel-1400x900.png")

	w.Resize(fyne.NewSize(760, 600))
	if units[1].Position().Y == units[0].Position().Y {
		t.Fatal("at 760px conditions must wrap onto separate lines")
	}
	snapshot(t, w, "filter-panel-760x600.png")

	w.Resize(fyne.NewSize(1400, 900))
	if units[1].Position().Y != units[0].Position().Y {
		t.Fatal("packing must be restored after growing the window back")
	}
}
