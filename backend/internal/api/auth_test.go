package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// ── Login tests ──────────────────────────────────────────────────────────────

func TestHandleGithubLogin(t *testing.T) {
	t.Setenv("CODEATLAS_GITHUB_CLIENT_ID", "test-client-id")

	req := httptest.NewRequest(http.MethodGet, "/auth/github/login", nil)
	w := httptest.NewRecorder()

	HandleGithubLogin(w, req)

	res := w.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusFound {
		t.Fatalf("expected status 302, got %d", res.StatusCode)
	}

	location := res.Header.Get("Location")
	if !strings.HasPrefix(location, "https://github.com/login/oauth/authorize") {
		t.Fatalf("Location %q does not start with expected GitHub URL", location)
	}

	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("failed to parse Location URL: %v", err)
	}
	q := parsed.Query()

	if q.Get("client_id") == "" {
		t.Error("Location URL missing client_id parameter")
	}

	stateInURL := q.Get("state")
	if stateInURL == "" {
		t.Fatal("Location URL missing state parameter")
	}
	if len(stateInURL) < 32 {
		t.Errorf("state %q is shorter than 32 characters", stateInURL)
	}

	var stateCookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "oauth_state" {
			stateCookie = c
			break
		}
	}
	if stateCookie == nil {
		t.Fatal("oauth_state cookie not set")
	}
	if stateCookie.Value != stateInURL {
		t.Errorf("cookie state %q does not match URL state %q", stateCookie.Value, stateInURL)
	}
}

// ── GitHub callback tests ─────────────────────────────────────────────────────

func TestHandleGithubCallback_MissingCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?state=abc&code=xyz", nil)
	w := httptest.NewRecorder()

	HandleGithubCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleGithubCallback_StateMismatch(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?state=wrong&code=xyz", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "correct"})
	w := httptest.NewRecorder()

	HandleGithubCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "invalid_state" {
		t.Errorf("expected error code 'invalid_state', got %v", errObj["code"])
	}
}

func TestHandleGithubCallback_MissingCode(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?state=abc", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "abc"})
	w := httptest.NewRecorder()

	HandleGithubCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleGithubCallback_ValidFlow(t *testing.T) {
	// Mock GitHub token endpoint
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubTokenResponse{
			AccessToken: "test-access-token",
			Scope:       "repo",
			TokenType:   "bearer",
		})
	}))
	defer tokenServer.Close()

	// Mock GitHub user endpoint
	userServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubUser{ID: 42, Login: "testuser"})
	}))
	defer userServer.Close()

	// Override package-level endpoint vars
	origToken := githubTokenEndpoint
	origUser := githubUserEndpoint
	githubTokenEndpoint = tokenServer.URL
	githubUserEndpoint = userServer.URL
	t.Cleanup(func() {
		githubTokenEndpoint = origToken
		githubUserEndpoint = origUser
	})

	// 32-byte key expressed as 64 hex characters
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", fmt.Sprintf("%064x", 0))
	t.Setenv("CODEATLAS_JWT_SECRET", "test-jwt-secret")
	t.Setenv("CODEATLAS_GITHUB_CLIENT_ID", "test-id")
	t.Setenv("CODEATLAS_GITHUB_CLIENT_SECRET", "test-secret")

	state := "teststate1234567890123456789012345"
	req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?state="+state+"&code=testcode", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: state})
	w := httptest.NewRecorder()

	HandleGithubCallback(w, req)

	res := w.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	var body struct {
		Token    string `json:"token"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if body.Token == "" {
		t.Error("expected non-empty token")
	}
	if body.Username != "testuser" {
		t.Errorf("expected username 'testuser', got %q", body.Username)
	}

	// Verify JWT sub claim
	parsed, err := jwt.Parse(body.Token, func(tok *jwt.Token) (any, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", tok.Header["alg"])
		}
		return []byte("test-jwt-secret"), nil
	})
	if err != nil {
		t.Fatalf("failed to parse JWT: %v", err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("failed to cast claims")
	}
	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		t.Errorf("expected non-empty sub claim, got %v", claims["sub"])
	}

	// Verify oauth_state cookie is cleared (MaxAge < 0)
	var stateCookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "oauth_state" {
			stateCookie = c
			break
		}
	}
	if stateCookie == nil {
		t.Fatal("expected Set-Cookie for oauth_state (to clear it), but none found")
	}
	if stateCookie.MaxAge >= 0 {
		t.Errorf("expected oauth_state cookie MaxAge < 0 (cleared), got %d", stateCookie.MaxAge)
	}
}

// ── GitLab callback tests ─────────────────────────────────────────────────────

func TestHandleGitlabCallback_StateMismatch(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/auth/gitlab/callback?state=wrong&code=xyz", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "correct"})
	w := httptest.NewRecorder()

	HandleGitlabCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "invalid_state" {
		t.Errorf("expected error code 'invalid_state', got %v", errObj["code"])
	}
}

func TestHandleGitlabCallback_ValidFlow(t *testing.T) {
	// Mock GitLab token endpoint
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(gitlabTokenResponse{
			AccessToken:  "gitlab-access-token",
			RefreshToken: "gitlab-refresh-token",
			ExpiresIn:    7200,
			TokenType:    "Bearer",
		})
	}))
	defer tokenServer.Close()

	// Mock GitLab user endpoint
	userServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(gitlabUser{ID: 99, Username: "gitlabuser"})
	}))
	defer userServer.Close()

	// Override package-level endpoint vars
	origToken := gitlabTokenEndpoint
	origUser := gitlabUserEndpoint
	gitlabTokenEndpoint = tokenServer.URL
	gitlabUserEndpoint = userServer.URL
	t.Cleanup(func() {
		gitlabTokenEndpoint = origToken
		gitlabUserEndpoint = origUser
	})

	t.Setenv("CODEATLAS_ENCRYPTION_KEY", fmt.Sprintf("%064x", 0))
	t.Setenv("CODEATLAS_JWT_SECRET", "test-jwt-secret")
	t.Setenv("CODEATLAS_GITLAB_CLIENT_ID", "test-gitlab-id")
	t.Setenv("CODEATLAS_GITLAB_CLIENT_SECRET", "test-gitlab-secret")
	t.Setenv("CODEATLAS_APP_BASE_URL", "http://localhost:8080")

	state := "gitlabstate123456789012345678901234"
	req := httptest.NewRequest(http.MethodGet, "/auth/gitlab/callback?state="+state+"&code=gitlabcode", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: state})
	w := httptest.NewRecorder()

	HandleGitlabCallback(w, req)

	res := w.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	var body struct {
		Token    string `json:"token"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if body.Token == "" {
		t.Error("expected non-empty token")
	}
	if body.Username != "gitlabuser" {
		t.Errorf("expected username 'gitlabuser', got %q", body.Username)
	}

	// Verify JWT sub claim
	parsed, err := jwt.Parse(body.Token, func(tok *jwt.Token) (any, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", tok.Header["alg"])
		}
		return []byte("test-jwt-secret"), nil
	})
	if err != nil {
		t.Fatalf("failed to parse JWT: %v", err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("failed to cast claims")
	}
	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		t.Errorf("expected non-empty sub claim, got %v", claims["sub"])
	}

	// Verify oauth_state cookie is cleared (MaxAge < 0)
	var stateCookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "oauth_state" {
			stateCookie = c
			break
		}
	}
	if stateCookie == nil {
		t.Fatal("expected Set-Cookie for oauth_state (to clear it), but none found")
	}
	if stateCookie.MaxAge >= 0 {
		t.Errorf("expected oauth_state cookie MaxAge < 0 (cleared), got %d", stateCookie.MaxAge)
	}
}
