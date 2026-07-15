//go:build !linux

package display

func size() (width, height float32) { return 0, 0 }
