//go:build !ci

package main

import (
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
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
	rowHeight := g.filterBox.Objects[0].Size().Height
	if delta := rowHeight - g.filterRowHeight; delta < -2 || delta > 2 {
		t.Fatalf("filter row height = %v, table row height = %v", rowHeight, g.filterRowHeight)
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
