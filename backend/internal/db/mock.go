package db

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MockUserRepository implements UserRepository using an in-memory map.
type MockUserRepository struct {
	mu    sync.Mutex
	users map[string]string // "provider:providerUserID" → userID
}

func NewMockUserRepository() *MockUserRepository {
	return &MockUserRepository{users: make(map[string]string)}
}

func (m *MockUserRepository) FindOrCreateByProvider(_ context.Context, provider, providerUserID, _ string) (string, error) {
	key := provider + ":" + providerUserID
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.users[key]; ok {
		return id, nil
	}
	id := uuid.New().String()
	m.users[key] = id
	return id, nil
}

// MockTokenRepository implements TokenRepository using an in-memory map.
type MockTokenRepository struct {
	mu     sync.Mutex
	tokens map[string]*OAuthToken // "userID:provider" → *OAuthToken
}

func NewMockTokenRepository() *MockTokenRepository {
	return &MockTokenRepository{tokens: make(map[string]*OAuthToken)}
}

func (m *MockTokenRepository) FindByUserAndProvider(_ context.Context, userID, provider string) (*OAuthToken, error) {
	key := userID + ":" + provider
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[key]
	if !ok {
		return nil, nil
	}
	cp := *t
	return &cp, nil
}

func (m *MockTokenRepository) Upsert(_ context.Context, token OAuthToken) error {
	key := token.UserID + ":" + token.Provider
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := token
	m.tokens[key] = &cp
	return nil
}

func (m *MockTokenRepository) UpdateAccessToken(_ context.Context, userID, provider, encryptedToken string, expiresAt time.Time) error {
	key := userID + ":" + provider
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tokens[key]; ok {
		t.AccessToken = encryptedToken
		t.TokenExpiresAt = &expiresAt
	}
	return nil
}

// MockGraphRepository implements GraphRepository using an in-memory map.
type MockGraphRepository struct {
	mu     sync.Mutex
	graphs map[string]*GraphRecord // id → *GraphRecord
	keys   map[string]string       // dedupKey → id
}

func NewMockGraphRepository() *MockGraphRepository {
	return &MockGraphRepository{
		graphs: make(map[string]*GraphRecord),
		keys:   make(map[string]string),
	}
}

func (m *MockGraphRepository) FindByID(_ context.Context, id string) (*GraphRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.graphs[id]
	if !ok {
		return nil, nil
	}
	cp := *g
	return &cp, nil
}

func (m *MockGraphRepository) FindByUser(_ context.Context, userID string) ([]*GraphRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*GraphRecord
	for _, g := range m.graphs {
		if g.UserID == userID {
			cp := *g
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (m *MockGraphRepository) FindOrCreate(_ context.Context, entry GraphRecord) (*GraphRecord, error) {
	key := entry.UserID + ":" + entry.Provider + ":" + entry.Owner + ":" + entry.RepoName + ":" + entry.Branch
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.keys[key]; ok {
		if g, ok := m.graphs[id]; ok {
			g.Status = "processing"
			g.ErrorMessage = ""
			cp := *g
			return &cp, nil
		}
	}
	id := uuid.New().String()
	g := &GraphRecord{
		ID:        id,
		UserID:    entry.UserID,
		Provider:  entry.Provider,
		Owner:     entry.Owner,
		RepoName:  entry.RepoName,
		Branch:    entry.Branch,
		Status:    "processing",
		CreatedAt: time.Now(),
	}
	m.graphs[id] = g
	m.keys[key] = id
	cp := *g
	return &cp, nil
}

func (m *MockGraphRepository) UpdateStatus(_ context.Context, id, status, errorMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok := m.graphs[id]; ok {
		g.Status = status
		g.ErrorMessage = errorMsg
	}
	return nil
}

func (m *MockGraphRepository) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.graphs[id]
	if !ok {
		return nil
	}
	key := g.UserID + ":" + g.Provider + ":" + g.Owner + ":" + g.RepoName + ":" + g.Branch
	delete(m.graphs, id)
	delete(m.keys, key)
	return nil
}
