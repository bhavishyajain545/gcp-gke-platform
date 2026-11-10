package main

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	checkLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "url_check_latency_seconds",
		Help:    "Latency of the probe against each registered URL.",
		Buckets: prometheus.DefBuckets,
	}, []string{"url"})
	checkUp = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "url_check_up",
		Help: "1 when the last probe of the URL returned 2xx/3xx, else 0.",
	}, []string{"url"})
	checksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "url_checks_total",
		Help: "Number of probes performed, by result.",
	}, []string{"result"})
	checkRuns = promauto.NewCounter(prometheus.CounterOpts{
		Name: "url_check_runs_total",
		Help: "Number of checker sweeps over all registered URLs.",
	})
)

// Checker probes every registered URL on a fixed interval in the background.
type Checker struct {
	store    Store
	interval time.Duration
	client   *http.Client
	mu       sync.Mutex
	lastRun  time.Time
}

func NewChecker(store Store, interval, timeout time.Duration) *Checker {
	return &Checker{
		store:    store,
		interval: interval,
		client: &http.Client{
			Timeout:       timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Run blocks until ctx is cancelled. The first sweep happens immediately.
func (c *Checker) Run(ctx context.Context) {
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		c.Sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Sweep probes all checks once, concurrently, and persists the results.
func (c *Checker) Sweep(ctx context.Context) {
	checks, err := c.store.List(ctx)
	if err != nil {
		slog.Error("list checks failed", "err", err)
		return
	}
	var wg sync.WaitGroup
	for _, chk := range checks {
		wg.Add(1)
		go func(chk Check) {
			defer wg.Done()
			res := c.Probe(ctx, chk)
			if err := c.store.Update(ctx, res); err != nil {
				slog.Error("update check failed", "id", chk.ID, "err", err)
			}
		}(chk)
	}
	wg.Wait()
	checkRuns.Inc()
	c.mu.Lock()
	c.lastRun = time.Now()
	c.mu.Unlock()
}

// Probe performs one HTTP GET and returns the updated check (not persisted).
func (c *Checker) Probe(ctx context.Context, chk Check) Check {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, chk.URL, nil)
	if err != nil {
		return c.record(chk, 0, time.Since(start), err)
	}
	req.Header.Set("User-Agent", "url-health-api/"+Version)
	resp, err := c.client.Do(req)
	if err != nil {
		return c.record(chk, 0, time.Since(start), err)
	}
	defer resp.Body.Close()
	return c.record(chk, resp.StatusCode, time.Since(start), nil)
}

func (c *Checker) record(chk Check, status int, latency time.Duration, err error) Check {
	chk.LastChecked = time.Now().UTC()
	chk.LatencyMs = latency.Milliseconds()
	chk.HTTPStatus = status
	chk.Error = ""
	up := err == nil && status >= 200 && status < 400
	if up {
		chk.Status = "up"
		checkUp.WithLabelValues(chk.URL).Set(1)
		checksTotal.WithLabelValues("up").Inc()
	} else {
		chk.Status = "down"
		if err != nil {
			chk.Error = err.Error()
		}
		checkUp.WithLabelValues(chk.URL).Set(0)
		checksTotal.WithLabelValues("down").Inc()
	}
	checkLatency.WithLabelValues(chk.URL).Observe(latency.Seconds())
	return chk
}

// LastRun reports when the most recent sweep finished (zero before the first).
func (c *Checker) LastRun() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastRun
}
