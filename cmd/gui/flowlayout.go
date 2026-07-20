//go:build !ci

package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
)

const (
	filterColumnWidth    = 220
	filterOperationWidth = 72
	filterValueWidth     = 220
	// flowWidthAllowance compensates the paddings between the window edge
	// and the filter panel so MinSize never assumes more width than the
	// panel actually receives.
	flowWidthAllowance = 16
)

// flowLayout arranges children left to right at their minimum size and moves
// a child to the next line only when the current line has no width left for
// it. MinSize cannot know the final container width, so the expected width
// comes from widthFn (the window canvas width for the filter panel, which
// spans the whole window); Layout wraps by the width actually given.
type flowLayout struct {
	widthFn func() float32
}

func (f *flowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	pad := theme.Padding()
	x, y, rowHeight := float32(0), float32(0), float32(0)
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		min := o.MinSize()
		if x > 0 && x+min.Width > size.Width {
			x, y = 0, y+rowHeight+pad
			rowHeight = 0
		}
		o.Resize(min)
		o.Move(fyne.NewPos(x, y))
		x += min.Width + pad
		if min.Height > rowHeight {
			rowHeight = min.Height
		}
	}
}

func (f *flowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	width := float32(0)
	if f.widthFn != nil {
		width = f.widthFn() - flowWidthAllowance
	}
	pad := theme.Padding()
	x, height, rowHeight, widest := float32(0), float32(0), float32(0), float32(0)
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		min := o.MinSize()
		if min.Width > widest {
			widest = min.Width
		}
		if width > 0 && x > 0 && x+min.Width > width {
			x, height = 0, height+rowHeight+pad
			rowHeight = 0
		}
		x += min.Width + pad
		if min.Height > rowHeight {
			rowHeight = min.Height
		}
	}
	return fyne.NewSize(widest, height+rowHeight)
}

// fixedWidthLayout pins children to a constant width so filter widgets keep
// a uniform compact footprint; height follows the child minimum so theme or
// font changes are never clipped.
type fixedWidthLayout struct {
	width float32
}

func (l *fixedWidthLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		o.Resize(fyne.NewSize(l.width, size.Height))
		o.Move(fyne.NewPos(0, 0))
	}
}

func (l *fixedWidthLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	height := float32(0)
	for _, o := range objects {
		if h := o.MinSize().Height; h > height {
			height = h
		}
	}
	return fyne.NewSize(l.width, height)
}

func fixedWidth(width float32, o fyne.CanvasObject) fyne.CanvasObject {
	return container.New(&fixedWidthLayout{width: width}, o)
}
