// Command gui starts the cross-platform graphical interface in a local,
// loopback-only application host and opens it in the OS browser.
package main

import (
	"context"
	"embed"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/dmitry/tinvest-snapshot/internal/catalog"
	"github.com/dmitry/tinvest-snapshot/internal/config"
	"github.com/dmitry/tinvest-snapshot/internal/model"
	"github.com/dmitry/tinvest-snapshot/internal/period"
	"github.com/dmitry/tinvest-snapshot/internal/report"
	"github.com/dmitry/tinvest-snapshot/internal/tinvest"
)

//go:embed web/* i18n/*
var assets embed.FS

type app struct {
	mu                    sync.RWMutex
	configPath, cachePath string
	cfg                   *config.Config
	snapshot              *model.Snapshot
	cache                 *catalog.Cache
}

func main() {
	cfgPath := flag.String("config", "config.json", "configuration file")
	flag.Parse()
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	data, _ := os.UserCacheDir()
	a := &app{configPath: *cfgPath, cachePath: filepath.Join(data, "tinvest-snapshot", "catalog.json"), cfg: cfg}
	a.cache, _ = catalog.Load(a.cachePath)
	mux := http.NewServeMux()
	sub, _ := fs.Sub(assets, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/state", a.state)
	mux.HandleFunc("/api/settings", a.settings)
	mux.HandleFunc("/api/refresh", a.refresh)
	mux.HandleFunc("/api/catalog", a.catalog)
	mux.HandleFunc("/api/export-view", a.exportView)
	mux.HandleFunc("/api/export-full", a.exportFull)
	mux.Handle("/i18n/", http.FileServer(http.FS(assets)))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	url := "http://" + ln.Addr().String()
	go open(url)
	fmt.Println("T-Invest GUI:", url)
	if err = http.Serve(ln, mux); err != nil {
		panic(err)
	}
}
func (a *app) client() (*tinvest.Client, error) {
	token, e := a.cfg.ResolveToken()
	if e != nil {
		return nil, e
	}
	c := tinvest.New(a.cfg.BaseURL(), token, a.cfg.AppName, a.cfg.Retries, time.Duration(a.cfg.RetryDelayMs)*time.Millisecond, nil)
	c.Sandbox = a.cfg.Sandbox()
	return c, nil
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, e error) { http.Error(w, e.Error(), http.StatusBadGateway) }
func (a *app) state(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var updated any
	if a.cache != nil {
		updated = a.cache.UpdatedAt
	}
	writeJSON(w, map[string]any{"settings": a.cfg, "snapshot": a.snapshot, "catalog_updated_at": updated})
}
func (a *app) settings(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	var c config.Config
	if e := json.NewDecoder(r.Body).Decode(&c); e != nil {
		fail(w, e)
		return
	}
	if e := c.Save(a.configPath); e != nil {
		fail(w, e)
		return
	}
	a.mu.Lock()
	a.cfg = &c
	a.mu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}
func (a *app) refresh(w http.ResponseWriter, r *http.Request) {
	var q struct{ From, To string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	win, e := period.Resolve(q.From, q.To, a.cfg.ReportsDir, time.Now())
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	c, e := a.client()
	if e != nil {
		fail(w, e)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	snap, e := c.Collect(ctx, a.cfg.Mode, a.cfg.TargetCurrency, time.Now())
	if e == nil {
		var ops []model.Operation
		var p model.OperationsPeriod
		ops, p, e = c.CollectOperations(ctx, win.GlobalFrom, win.To)
		snap.Operations = ops
		snap.OperationsPeriod = &p
	}
	if e != nil {
		fail(w, e)
		return
	}
	a.mu.Lock()
	a.snapshot = snap
	a.mu.Unlock()
	writeJSON(w, snap)
}
func (a *app) catalog(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	force := r.URL.Query().Get("force") == "1"
	if a.cache == nil || force || !a.cache.Fresh(time.Duration(a.cfg.CatalogTTLHours)*time.Hour, time.Now()) {
		c, e := a.client()
		if e != nil {
			fail(w, e)
			return
		}
		items, e := c.Catalog(r.Context(), time.Now())
		if e != nil {
			fail(w, e)
			return
		}
		a.cache = &catalog.Cache{UpdatedAt: time.Now(), Instruments: items}
		if e = a.cache.Save(a.cachePath); e != nil {
			fail(w, e)
			return
		}
	}
	q := r.URL.Query()
	f := catalog.Filter{Type: q.Get("type"), Query: q.Get("q"), Currency: q.Get("currency"), Exchange: q.Get("exchange"), Sector: q.Get("sector"), Risk: q.Get("risk"), CouponType: q.Get("coupon_type"), SortBy: q.Get("sort"), Desc: q.Get("desc") == "1", RateFrom: catalog.Float(q.Get("rate_from")), RateTo: catalog.Float(q.Get("rate_to"))}
	f.Frequency, _ = strconv.Atoi(q.Get("frequency"))
	f.CouponMonth, _ = strconv.Atoi(q.Get("month"))
	if q.Get("dividends") != "" {
		v := q.Get("dividends") == "1"
		f.Dividends = &v
	}
	writeJSON(w, map[string]any{"updated_at": a.cache.UpdatedAt, "instruments": catalog.Search(a.cache.Instruments, f)})
}
func (a *app) exportView(w http.ResponseWriter, r *http.Request) {
	var rows [][]string
	if e := json.NewDecoder(r.Body).Decode(&rows); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	if e := os.MkdirAll(a.cfg.ReportsDir, 0755); e != nil {
		fail(w, e)
		return
	}
	p := filepath.Join(a.cfg.ReportsDir, "table_"+time.Now().Format("20060102_150405")+".csv")
	f, e := os.Create(p)
	if e != nil {
		fail(w, e)
		return
	}
	cw := csv.NewWriter(f)
	e = cw.WriteAll(rows)
	cw.Flush()
	if e == nil {
		e = cw.Error()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, map[string]string{"path": p})
}

func (a *app) exportFull(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	snap := a.snapshot
	a.mu.RUnlock()
	if snap == nil {
		http.Error(w, "сначала обновите данные", http.StatusBadRequest)
		return
	}
	paths, err := report.Write(a.cfg.ReportsDir, time.Now(), snap)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, paths)
}
func open(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "linux":
		c = exec.Command("xdg-open", url)
	default:
		return
	}
	c.Stdout = nil
	c.Stderr = nil
	_ = c.Start()
}
