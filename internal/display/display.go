// Package display provides the physical screen dimensions by reading
// sysfs on Linux; on unsupported platforms it returns (0,0) and the
// caller uses its own safe default.
package display

import "fyne.io/fyne/v2"

// sizeFn is the platform-specific display-size probe.  Tests override it.
var sizeFn = size

// Clamp returns a size that fits within the detected display work area
// (physical screen minus estimated window-manager chrome).  When the
// display size cannot be detected, desired is returned unchanged.
func Clamp(desired fyne.Size) fyne.Size {
	w, h := sizeFn()
	if w <= 0 || h <= 0 {
		return desired
	}
	// Reserve 40×80 px for window decorations + system panel.
	availW, availH := w-40, h-80
	if availW < 640 {
		availW = 640
	}
	if availH < 480 {
		availH = 480
	}
	if desired.Width > availW {
		desired.Width = availW
	}
	if desired.Height > availH {
		desired.Height = availH
	}
	return desired
}
