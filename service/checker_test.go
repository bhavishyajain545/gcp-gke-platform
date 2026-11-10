package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSweepMarksUpAndDown(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer up.Close()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer down.Close()

	store := NewMemoryStore()
	ctx := context.Background()
	upCheck, _ := store.Create(ctx, up.URL)
	downCheck, _ := store.Create(ctx, down.URL)
	deadCheck, _ := store.Create(ctx, "http://127.0.0.1:1") // nothing listens here

	checker := NewChecker(store, time.Minute, time.Second)
	checker.Sweep(ctx)

	got := map[string]Check{}
	list, _ := store.List(ctx)
	for _, c := range list {
		got[c.ID] = c
	}
	if got[upCheck.ID].Status != "up" || got[upCheck.ID].HTTPStatus != 200 {
		t.Errorf("up check: %+v", got[upCheck.ID])
	}
	if got[downCheck.ID].Status != "down" || got[downCheck.ID].HTTPStatus != 502 {
		t.Errorf("down check: %+v", got[downCheck.ID])
	}
	if got[deadCheck.ID].Status != "down" || got[deadCheck.ID].Error == "" {
		t.Errorf("dead check should carry an error: %+v", got[deadCheck.ID])
	}
	if checker.LastRun().IsZero() {
		t.Error("LastRun not set after sweep")
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	checker := NewChecker(NewMemoryStore(), 10*time.Millisecond, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { checker.Run(ctx); close(done) }()
	time.Sleep(35 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}
