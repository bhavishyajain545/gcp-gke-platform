package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// httpDuration feeds the latency SLO (99% of /checks under 300 ms);
// httpRequests feeds the availability SLO (non-5xx / total).
var (
	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency by route and status code.",
		Buckets: []float64{.005, .01, .025, .05, .1, .2, .3, .5, 1, 2.5},
	}, []string{"route", "method", "code"})
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "HTTP requests by route, method and status code.",
	}, []string{"route", "method", "code"})
)

type api struct {
	store   Store
	checker *Checker
}

// NewRouter wires routes with the Go 1.22+ method-aware ServeMux.
func NewRouter(store Store, checker *Checker) http.Handler {
	a := &api{store: store, checker: checker}
	mux := http.NewServeMux()
	mux.Handle("POST /checks", instrument("/checks", http.HandlerFunc(a.createCheck)))
	mux.Handle("GET /checks", instrument("/checks", http.HandlerFunc(a.listChecks)))
	mux.Handle("GET /healthz", http.HandlerFunc(a.healthz))
	mux.Handle("GET /readyz", http.HandlerFunc(a.readyz))
	mux.Handle("GET /version", http.HandlerFunc(a.version))
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}

type createRequest struct {
	URL string `json:"url"`
}

func (a *api) createCheck(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON like {\"url\": \"https://example.com\"}")
		return
	}
	u, err := url.ParseRequestURI(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeError(w, http.StatusBadRequest, "url must be an absolute http(s) URL")
		return
	}
	c, err := a.store.Create(r.Context(), u.String())
	if err != nil {
		slog.Error("create failed", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save check")
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (a *api) listChecks(w http.ResponseWriter, r *http.Request) {
	checks, err := a.store.List(r.Context())
	if err != nil {
		slog.Error("list failed", "err", err)
		writeError(w, http.StatusInternalServerError, "could not list checks")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(checks), "checks": checks})
}

func (a *api) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz turns unready if the checker has not completed a sweep in 3 intervals,
// so a wedged worker takes the pod out of rotation.
func (a *api) readyz(w http.ResponseWriter, _ *http.Request) {
	last := a.checker.LastRun()
	if last.IsZero() || time.Since(last) > 3*a.checker.interval {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not ready", "last_sweep": last})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "last_sweep": last})
}

func (a *api) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": Version})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// instrument records request latency per route/method/status.
func instrument(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		code := strconv.Itoa(rec.code)
		httpDuration.WithLabelValues(route, r.Method, code).Observe(time.Since(start).Seconds())
		httpRequests.WithLabelValues(route, r.Method, code).Inc()
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}
