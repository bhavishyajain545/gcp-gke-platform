package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestAPI(t *testing.T) (http.Handler, *MemoryStore, *Checker) {
	t.Helper()
	store := NewMemoryStore()
	checker := NewChecker(store, time.Minute, 2*time.Second)
	return NewRouter(store, checker), store, checker
}

func TestCreateAndListChecks(t *testing.T) {
	h, _, _ := newTestAPI(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/checks", strings.NewReader(`{"url":"https://example.com/health"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /checks: want 201, got %d: %s", rec.Code, rec.Body)
	}
	var created Check
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Status != "pending" || created.URL != "https://example.com/health" {
		t.Fatalf("unexpected check: %+v", created)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/checks", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /checks: want 200, got %d", rec.Code)
	}
	var list struct {
		Count  int     `json:"count"`
		Checks []Check `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Count != 1 || list.Checks[0].ID != created.ID {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func TestCreateCheckValidation(t *testing.T) {
	h, _, _ := newTestAPI(t)
	for _, body := range []string{`{"url":"ftp://x"}`, `{"url":"not a url"}`, `{}`, `nope`} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/checks", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: want 400, got %d", body, rec.Code)
		}
	}
}

func TestReadyzReflectsChecker(t *testing.T) {
	h, _, checker := newTestAPI(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("before first sweep: want 503, got %d", rec.Code)
	}
	checker.Sweep(context.Background())
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("after sweep: want 200, got %d", rec.Code)
	}
}

func TestMetricsExposed(t *testing.T) {
	h, _, _ := newTestAPI(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/checks", nil))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "http_request_duration_seconds") {
		t.Fatalf("metrics missing: %d", rec.Code)
	}
}
