package display

import (
	"os"
	"strconv"
	"strings"
)

// size reads the smallest current mode from all connected+enabled DRM
// outputs.  Returns (0,0) when no display is detected.
func size() (width, height float32) {
	entries, err := os.ReadDir("/sys/class/drm")
	if err != nil {
		return 0, 0
	}
	var minW, minH int
	for _, e := range entries {
		name := e.Name()
		if !strings.Contains(name, "-") {
			continue // skip card0, renderD128, version
		}
		base := "/sys/class/drm/" + name
		if !connected(base) || !enabled(base) {
			continue
		}
		w, h := currentMode(base)
		if w <= 0 || h <= 0 {
			continue
		}
		if minW == 0 || w < minW {
			minW = w
		}
		if minH == 0 || h < minH {
			minH = h
		}
	}
	return float32(minW), float32(minH)
}

func connected(base string) bool {
	b, err := os.ReadFile(base + "/status")
	return err == nil && strings.TrimSpace(string(b)) == "connected"
}

func enabled(base string) bool {
	b, err := os.ReadFile(base + "/enabled")
	return err == nil && strings.TrimSpace(string(b)) == "enabled"
}

func currentMode(base string) (w, h int) {
	b, err := os.ReadFile(base + "/modes")
	if err != nil {
		return 0, 0
	}
	// First line of the modes file is the current mode, e.g. "1920x1080\n".
	firstLine, _, _ := strings.Cut(string(b), "\n")
	parts := strings.SplitN(firstLine, "x", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	w, _ = strconv.Atoi(parts[0])
	h, _ = strconv.Atoi(parts[1])
	return w, h
}
