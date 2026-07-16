//go:build !ci

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/dmitry/tinvest-snapshot/internal/model"
)

type snapshotCache struct {
	UpdatedAt time.Time       `json:"updated_at"`
	Mode      string          `json:"mode"`
	From      string          `json:"from,omitempty"`
	To        string          `json:"to,omitempty"`
	Snapshot  *model.Snapshot `json:"snapshot"`
}

func loadSnapshotCache(path string) (*snapshotCache, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cache snapshotCache
	if err = json.Unmarshal(b, &cache); err != nil {
		return nil, err
	}
	return &cache, nil
}

func saveSnapshotCache(path string, cache snapshotCache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cache-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(b); err == nil {
		err = tmp.Close()
	} else {
		_ = tmp.Close()
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func cacheFresh(updated time.Time, ttlHours int, now time.Time) bool {
	return !updated.IsZero() && now.Sub(updated) < time.Duration(ttlHours)*time.Hour
}

// operationCacheRange is a user-facing calendar key. It deliberately avoids
// RFC3339 conversion: local midnight in a positive offset is the previous UTC
// date and must still match the same selected calendar day after restart.
func operationCacheRange(from, to string, now time.Time) (string, string) {
	day := now.Format("2006-01-02")
	if from == "" && to == "" {
		return day, day
	}
	return from, to
}
