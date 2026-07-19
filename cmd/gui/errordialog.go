//go:build !ci

package main

import (
	"math"
	"unicode"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// The visible error area is capped so a long report scrolls instead of
// growing the dialog beyond the window.
const (
	errorViewWidth     float32 = 480
	errorViewMaxHeight float32 = 240
)

// copyableErrorMessage is the single shared representation of an error text
// in the GUI: a selectable read-only label. Fyne handles mouse selection,
// the platform copy shortcut and a copy-only context menu; a label has no
// editing paths at all. The scroll wrapper keeps the full text reachable
// for long and multi-line messages.
func copyableErrorMessage(msg string) fyne.CanvasObject {
	l := widget.NewLabel(msg)
	l.Selectable = true
	l.Wrapping = fyne.TextWrapWord

	natural := widget.NewLabel(msg).MinSize() // unwrapped extent of the text
	h := natural.Height
	if natural.Width > errorViewWidth {
		// The widest line will wrap into several rows; reserve room for them.
		rows := float32(math.Ceil(float64(natural.Width) / float64(errorViewWidth-20)))
		h = natural.Height * rows
	}
	if h > errorViewMaxHeight {
		h = errorViewMaxHeight
	}
	scroll := container.NewScroll(l)
	scroll.SetMinSize(fyne.NewSize(errorViewWidth, h))
	return scroll
}

// showError replaces dialog.ShowError for every error the GUI reports after
// the main window is up. The displayed text, the localised title and the OK
// button mirror dialog.ShowError exactly (including the capitalised first
// rune); only the message becomes selectable and copyable. Closing the
// dialog returns to the application as before.
func showError(err error, win fyne.Window) {
	d := dialog.NewCustom(lang.L("Error"), lang.L("OK"), copyableErrorMessage(errorDialogText(err)), win)
	d.SetIcon(theme.ErrorIcon())
	d.Show()
}

func errorDialogText(err error) string {
	msg := err.Error()
	if r, size := utf8.DecodeRuneInString(msg); r != utf8.RuneError {
		msg = string(unicode.ToUpper(r)) + msg[size:]
	}
	return msg
}

// showStartupError keeps the pre-window fatal dialog contract — the
// "T-Invest" title, a single OK button and the exit callback — while
// presenting the message in the shared copyable view. The text stays
// verbatim: the previous startup dialog did not capitalise it.
func showStartupError(win fyne.Window, msg string, onDismiss func()) {
	dialog.ShowCustomConfirm("T-Invest", "OK", "", copyableErrorMessage(msg), func(bool) { onDismiss() }, win)
}
