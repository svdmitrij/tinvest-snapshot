package display

import (
	"testing"

	"fyne.io/fyne/v2"
)

func TestClamp(t *testing.T) {
	// When display detection fails (size() returns 0,0), desired is returned as-is.
	sizeFn = func() (float32, float32) { return 0, 0 }
	if got := Clamp(fyne.NewSize(1280, 800)); got.Width != 1280 || got.Height != 800 {
		t.Fatalf("no display: expected 1280×800, got %.0f×%.0f", got.Width, got.Height)
	}

	// 1366×768 laptop — desired height clamped to 768−80=688.
	sizeFn = func() (float32, float32) { return 1366, 768 }
	if got := Clamp(fyne.NewSize(1280, 800)); got.Width != 1280 || got.Height != 688 {
		t.Fatalf("1366×768, desired 1280×800: expected 1280×688, got %.0f×%.0f", got.Width, got.Height)
	}

	// 1920×1080 monitor — desired fits fully.
	sizeFn = func() (float32, float32) { return 1920, 1080 }
	if got := Clamp(fyne.NewSize(1280, 800)); got.Width != 1280 || got.Height != 800 {
		t.Fatalf("1920×1080: expected 1280×800, got %.0f×%.0f", got.Width, got.Height)
	}

	// Small display 1024×768 — both dimensions clamped.
	sizeFn = func() (float32, float32) { return 1024, 768 }
	if got := Clamp(fyne.NewSize(1280, 800)); got.Width != 984 || got.Height != 688 {
		t.Fatalf("1024×768: expected 984×688, got %.0f×%.0f", got.Width, got.Height)
	}

	// 800×600 — clamped with floor still effective.
	sizeFn = func() (float32, float32) { return 800, 600 }
	if got := Clamp(fyne.NewSize(1280, 800)); got.Width != 760 || got.Height != 520 {
		t.Fatalf("800×600: expected 760×520, got %.0f×%.0f", got.Width, got.Height)
	}

	// Very tiny display — clamped to floor.
	sizeFn = func() (float32, float32) { return 400, 300 }
	if got := Clamp(fyne.NewSize(1280, 800)); got.Width != 640 || got.Height != 480 {
		t.Fatalf("400×300: expected 640×480 (floor), got %.0f×%.0f", got.Width, got.Height)
	}
}
