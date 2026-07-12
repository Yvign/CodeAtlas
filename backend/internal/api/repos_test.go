package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"CodeAtlas/internal/db"
)

const testEncKey = "0000000000000000000000000000000000000000000000000000000000000000"

// newTestReposHandler creates a ReposHandler with a fresh mock token repo.
func newTestReposHandler() (*ReposHandler, *db.MockTokenRepository) {
	mockTokens := db.NewMockTokenRepository()
	return &ReposHandler{Tokens: mockTokens}, mockTokens
}

// storeGithubToken encrypts plaintext and stores it in the given mock token repo under userID.
func storeGithubToken(t *testing.T, tokens *db.MockTokenRepository, userID, plaintext string) {
	t.Helper()
	enc, err := encryptToken(plaintext, testEncKey)
	if err != nil {
		t.Fatalf("storeGithubToken: %v", err)
	}
	tokens.Upsert(context.Background(), db.OAuthToken{
		UserID:      userID,
		Provider:    "github",
		AccessToken: enc,
	})
}

// storeGitlabToken encrypts plaintext and stores it in the given mock token repo under userID.
func storeGitlabToken(t *testing.T, tokens *db.MockTokenRepository, userID, plaintext string) {
	t.Helper()
	enc, err := encryptToken(plaintext, testEncKey)
	if err != nil {
		t.Fatalf("storeGitlabToken: %v", err)
	}
	tokens.Upsert(context.Background(), db.OAuthToken{
		UserID:      userID,
		Provider:    "gitlab",
		AccessToken: enc,
	})
}

// requestWithUser builds a request with userID injected into context.
func requestWithUser(method, target, userID string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	ctx := context.WithValue(req.Context(), userIDKey, userID)
	return req.WithContext(ctx)
}

// requestWithUserAndChi adds chi URL params on top of a user context.
func requestWithUserAndChi(method, target, userID string, params map[string]string) *http.Request {
	req := requestWithUser(method, target, userID)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	return req.WithContext(ctx)
}

// ── handleListRepos ───────────────────────────────────────────────────────────

func TestHandleListRepos_MissingProvider(t *testing.T) {
	h, _ := newTestReposHandler()
	req := requestWithUser(http.MethodGet, "/repos", "user-1")
	w := httptest.NewRecorder()

	h.HandleListRepos(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "invalid_provider" {
		t.Errorf("expected code 'invalid_provider', got %v", errObj["code"])
	}
}

func TestHandleListRepos_GitHub(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)

	h, mockTokens := newTestReposHandler()
	const userID = "gh-repos-user"
	storeGithubToken(t, mockTokens, userID, "gh-access-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Next-Page", "2")
		json.NewEncoder(w).Encode([]githubRepoRaw{
			{FullName: "alice/alpha", Private: false, DefaultBranch: "main"},
			{FullName: "alice/beta", Private: true, DefaultBranch: "master"},
		})
	}))
	defer srv.Close()

	orig := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { githubAPIBase = orig })

	req := requestWithUser(http.MethodGet, "/repos?provider=github", userID)
	w := httptest.NewRecorder()
	h.HandleListRepos(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body struct {
		Repos    []repoItem `json:"repos"`
		NextPage int        `json:"nextPage"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(body.Repos))
	}
	if body.Repos[0].FullName != "alice/alpha" {
		t.Errorf("repo[0].fullName = %q", body.Repos[0].FullName)
	}
	if body.Repos[1].Private != true {
		t.Errorf("repo[1].private should be true")
	}
	if body.NextPage != 2 {
		t.Errorf("expected nextPage 2, got %d", body.NextPage)
	}
}

func TestHandleListRepos_GitLab(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)

	h, mockTokens := newTestReposHandler()
	const userID = "gl-repos-user"
	storeGitlabToken(t, mockTokens, userID, "gl-access-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]gitlabProjectRaw{
			{PathWithNamespace: "bob/gamma", Visibility: "private", DefaultBranch: "main"},
			{PathWithNamespace: "bob/delta", Visibility: "public", DefaultBranch: "develop"},
		})
	}))
	defer srv.Close()

	orig := gitlabAPIBase
	gitlabAPIBase = srv.URL
	t.Cleanup(func() { gitlabAPIBase = orig })

	req := requestWithUser(http.MethodGet, "/repos?provider=gitlab", userID)
	w := httptest.NewRecorder()
	h.HandleListRepos(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body struct {
		Repos    []repoItem `json:"repos"`
		NextPage int        `json:"nextPage"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(body.Repos))
	}
	if body.Repos[0].FullName != "bob/gamma" {
		t.Errorf("repo[0].fullName = %q", body.Repos[0].FullName)
	}
	if body.Repos[0].Private != true {
		t.Errorf("repo[0] visibility=private should map to private=true")
	}
	if body.Repos[1].Private != false {
		t.Errorf("repo[1] visibility=public should map to private=false")
	}
	if body.NextPage != 0 {
		t.Errorf("expected nextPage 0, got %d", body.NextPage)
	}
}

// ── handleListBranches ────────────────────────────────────────────────────────

func TestHandleListBranches_GitHub(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)

	h, mockTokens := newTestReposHandler()
	const userID = "gh-branch-user"
	storeGithubToken(t, mockTokens, userID, "gh-access-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{
			{"name": "main"},
			{"name": "dev"},
			{"name": "feature-x"},
		})
	}))
	defer srv.Close()

	orig := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { githubAPIBase = orig })

	req := requestWithUserAndChi(http.MethodGet,
		"/repos/github/alice/myrepo/branches", userID,
		map[string]string{"provider": "github", "owner": "alice", "repo": "myrepo"},
	)
	w := httptest.NewRecorder()
	h.HandleListBranches(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body struct {
		Branches []string `json:"branches"`
		NextPage int      `json:"nextPage"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{"main", "dev", "feature-x"}
	if fmt.Sprintf("%v", body.Branches) != fmt.Sprintf("%v", want) {
		t.Errorf("branches = %v, want %v", body.Branches, want)
	}
	if body.NextPage != 0 {
		t.Errorf("expected nextPage 0, got %d", body.NextPage)
	}
}

func TestHandleListBranches_GitLab(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)

	h, mockTokens := newTestReposHandler()
	const userID = "gl-branch-user"
	storeGitlabToken(t, mockTokens, userID, "gl-access-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Next-Page", "2")
		json.NewEncoder(w).Encode([]map[string]string{
			{"name": "main"},
			{"name": "release"},
		})
	}))
	defer srv.Close()

	orig := gitlabAPIBase
	gitlabAPIBase = srv.URL
	t.Cleanup(func() { gitlabAPIBase = orig })

	req := requestWithUserAndChi(http.MethodGet,
		"/repos/gitlab/bob/myproject/branches", userID,
		map[string]string{"provider": "gitlab", "owner": "bob", "repo": "myproject"},
	)
	w := httptest.NewRecorder()
	h.HandleListBranches(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body struct {
		Branches []string `json:"branches"`
		NextPage int      `json:"nextPage"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{"main", "release"}
	if fmt.Sprintf("%v", body.Branches) != fmt.Sprintf("%v", want) {
		t.Errorf("branches = %v, want %v", body.Branches, want)
	}
	if body.NextPage != 2 {
		t.Errorf("expected nextPage 2, got %d", body.NextPage)
	}
}
