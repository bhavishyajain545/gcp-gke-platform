package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// Check is one monitored URL and the result of its last probe.
type Check struct {
	ID          string    `json:"id" firestore:"-"`
	URL         string    `json:"url" firestore:"url"`
	Status      string    `json:"status" firestore:"status"` // pending | up | down
	HTTPStatus  int       `json:"http_status" firestore:"http_status"`
	LatencyMs   int64     `json:"latency_ms" firestore:"latency_ms"`
	Error       string    `json:"error,omitempty" firestore:"error"`
	LastChecked time.Time `json:"last_checked" firestore:"last_checked"`
	CreatedAt   time.Time `json:"created_at" firestore:"created_at"`
}

var ErrNotFound = errors.New("check not found")

// Store is the persistence boundary; the checker and handlers only see this.
type Store interface {
	Create(ctx context.Context, url string) (Check, error)
	List(ctx context.Context) ([]Check, error)
	Update(ctx context.Context, c Check) error
	Close() error
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------------------------------------------------------------- in-memory

type MemoryStore struct {
	mu     sync.RWMutex
	checks map[string]Check
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{checks: map[string]Check{}} }

func (m *MemoryStore) Create(_ context.Context, url string) (Check, error) {
	c := Check{ID: newID(), URL: url, Status: "pending", CreatedAt: time.Now().UTC()}
	m.mu.Lock()
	m.checks[c.ID] = c
	m.mu.Unlock()
	return c, nil
}

func (m *MemoryStore) List(_ context.Context) ([]Check, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Check, 0, len(m.checks))
	for _, c := range m.checks {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *MemoryStore) Update(_ context.Context, c Check) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.checks[c.ID]; !ok {
		return ErrNotFound
	}
	m.checks[c.ID] = c
	return nil
}

func (m *MemoryStore) Close() error { return nil }

// ---------------------------------------------------------------- firestore

// FirestoreStore keeps one document per check. Auth comes from the pod's
// Kubernetes service account mapped to a GCP service account (Workload
// Identity), so there is nothing to configure here.
type FirestoreStore struct {
	client *firestore.Client
	coll   string
}

func NewFirestoreStore(ctx context.Context, project, collection string) (*FirestoreStore, error) {
	if project == "" {
		return nil, errors.New("GOOGLE_CLOUD_PROJECT is required for the firestore store")
	}
	client, err := firestore.NewClient(ctx, project)
	if err != nil {
		return nil, err
	}
	return &FirestoreStore{client: client, coll: collection}, nil
}

func (f *FirestoreStore) Create(ctx context.Context, url string) (Check, error) {
	c := Check{ID: newID(), URL: url, Status: "pending", CreatedAt: time.Now().UTC()}
	_, err := f.client.Collection(f.coll).Doc(c.ID).Set(ctx, c)
	return c, err
}

func (f *FirestoreStore) List(ctx context.Context) ([]Check, error) {
	iter := f.client.Collection(f.coll).OrderBy("created_at", firestore.Asc).Documents(ctx)
	defer iter.Stop()
	var out []Check
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		var c Check
		if err := doc.DataTo(&c); err != nil {
			return nil, err
		}
		c.ID = doc.Ref.ID
		out = append(out, c)
	}
	if out == nil {
		out = []Check{}
	}
	return out, nil
}

func (f *FirestoreStore) Update(ctx context.Context, c Check) error {
	_, err := f.client.Collection(f.coll).Doc(c.ID).Set(ctx, c)
	return err
}

func (f *FirestoreStore) Close() error { return f.client.Close() }
