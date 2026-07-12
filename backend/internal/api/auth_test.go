package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"CodeAtlas/internal/db"
)

// newTestAuthHandler creates an AuthHandler with fresh mock repos.
func newTestAuthHandler() (*AuthHandler, *db.MockUserRepository, *db.MockTokenRepository) {
	mockUsers := db.NewMockUserRepository()
	mockTokens := db.NewMockTokenRepository()
	return &AuthHandler{Users: mockUsers, Tokens: mockTokens}, mockUsers, mockTokens
}

// jwtCookie extracts the codeatlas_jwt cookie from a recorder response, or nil.
func jwtCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == "codeatlas_jwt" {
			return c
		}
	}
	return nil
}

// ── Login tests ───────────────────────────────────────────────────────────────

func TestHandleGithubLogin(t *testing.T) {
	t.Setenv("CODEATLAS_GITHUB_CLIENT_ID", "test-client-id")

	h, _, _ := newTestAuthHandler()
	req := httptest.NewRequest(http.MethodGet, "/auth/github/login", nil)
	w := httptest.NewRecorder()

	h.HandleGithubLogin(w, req)

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
	h, _, _ := newTestAuthHandler()
	req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?state=abc&code=xyz", nil)
	w := httptest.NewRecorder()

	h.HandleGithubCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleGithubCallback_StateMismatch(t *testing.T) {
	h, _, _ := newTestAuthHandler()
	req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?state=wrong&code=xyz", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "correct"})
	w := httptest.NewRecorder()

	h.HandleGithubCallback(w, req)

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
	h, _, _ := newTestAuthHandler()
	req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?state=abc", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "abc"})
	w := httptest.NewRecorder()

	h.HandleGithubCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleGithubCallback_ValidFlow(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubTokenResponse{
			AccessToken: "test-access-token",
			Scope:       "repo",
			TokenType:   "bearer",
		})
	}))
	defer tokenServer.Close()

	userServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubUser{ID: 42, Login: "testuser"})
	}))
	defer userServer.Close()

	origToken := githubTokenEndpoint
	origUser := githubUserEndpoint
	githubTokenEndpoint = tokenServer.URL
	githubUserEndpoint = userServer.URL
	t.Cleanup(func() {
		githubTokenEndpoint = origToken
		githubUserEndpoint = origUser
	})

	t.Setenv("CODEATLAS_ENCRYPTION_KEY", fmt.Sprintf("%064x", 0))
	t.Setenv("CODEATLAS_JWT_SECRET", "test-jwt-secret")
	t.Setenv("CODEATLAS_GITHUB_CLIENT_ID", "test-id")
	t.Setenv("CODEATLAS_GITHUB_CLIENT_SECRET", "test-secret")

	h, _, _ := newTestAuthHandler()

	state := "teststate1234567890123456789012345"
	req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?state="+state+"&code=testcode", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: state})
	w := httptest.NewRecorder()

	h.HandleGithubCallback(w, req)

	// Expect 302 redirect to the frontend's /repos page
	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "http://localhost:5173/repos" {
		t.Errorf("expected redirect to http://localhost:5173/repos, got %q", loc)
	}

	// Expect codeatlas_jwt cookie to be set
	c := jwtCookie(w)
	if c == nil {
		t.Fatal("expected codeatlas_jwt cookie to be set")
	}
	if !c.HttpOnly {
		t.Error("expected HttpOnly cookie")
	}
	if c.MaxAge != 86400 {
		t.Errorf("expected MaxAge 86400, got %d", c.MaxAge)
	}

	// Verify JWT sub claim is non-empty
	parsed, err := jwt.Parse(c.Value, func(tok *jwt.Token) (any, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte("test-jwt-secret"), nil
	})
	if err != nil {
		t.Fatalf("failed to parse JWT from cookie: %v", err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("failed to cast claims")
	}
	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		t.Errorf("expected non-empty sub claim, got %v", claims["sub"])
	}

	// oauth_state cookie should be cleared
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "oauth_state" && ck.MaxAge >= 0 {
			t.Errorf("expected oauth_state cookie MaxAge < 0 (cleared), got %d", ck.MaxAge)
		}
	}
}

// ── GitLab callback tests ─────────────────────────────────────────────────────

func TestHandleGitlabCallback_StateMismatch(t *testing.T) {
	h, _, _ := newTestAuthHandler()
	req := httptest.NewRequest(http.MethodGet, "/auth/gitlab/callback?state=wrong&code=xyz", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "correct"})
	w := httptest.NewRecorder()

	h.HandleGitlabCallback(w, req)

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
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(gitlabTokenResponse{
			AccessToken:  "gitlab-access-token",
			RefreshToken: "gitlab-refresh-token",
			ExpiresIn:    7200,
			TokenType:    "Bearer",
		})
	}))
	defer tokenServer.Close()

	userServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(gitlabUser{ID: 99, Username: "gitlabuser"})
	}))
	defer userServer.Close()

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

	h, _, _ := newTestAuthHandler()

	state := "gitlabstate123456789012345678901234"
	req := httptest.NewRequest(http.MethodGet, "/auth/gitlab/callback?state="+state+"&code=gitlabcode", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: state})
	w := httptest.NewRecorder()

	h.HandleGitlabCallback(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "http://localhost:5173/repos" {
		t.Errorf("expected redirect to http://localhost:5173/repos, got %q", loc)
	}

	c := jwtCookie(w)
	if c == nil {
		t.Fatal("expected codeatlas_jwt cookie to be set")
	}
	if !c.HttpOnly {
		t.Error("expected HttpOnly cookie")
	}

	parsed, err := jwt.Parse(c.Value, func(tok *jwt.Token) (any, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte("test-jwt-secret"), nil
	})
	if err != nil {
		t.Fatalf("failed to parse JWT from cookie: %v", err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("failed to cast claims")
	}
	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		t.Errorf("expected non-empty sub claim, got %v", claims["sub"])
	}
}

// ── Cookie-based JWT middleware tests ─────────────────────────────────────────

func TestJWTMiddleware_MissingCookieReturns401(t *testing.T) {
	t.Setenv("CODEATLAS_JWT_SECRET", testJWTSecret)

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	w := httptest.NewRecorder()

	JWTMiddleware(echoUserID).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "missing_token" {
		t.Errorf("expected code 'missing_token', got %v", errObj["code"])
	}
}

func TestJWTMiddleware_ValidCookiePassesThrough(t *testing.T) {
	t.Setenv("CODEATLAS_JWT_SECRET", testJWTSecret)

	const wantUserID = "cookie-user-456"
	tok := makeToken(t, jwt.MapClaims{
		"sub": wantUserID,
		"exp": jwt.NewNumericDate(time.Now().Add(time.Hour)),
		"iat": jwt.NewNumericDate(time.Now()),
	})

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "codeatlas_jwt", Value: tok})
	w := httptest.NewRecorder()

	JWTMiddleware(echoUserID).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != wantUserID {
		t.Errorf("expected userID %q, got %q", wantUserID, got)
	}
}

// ── HandleMe tests ────────────────────────────────────────────────────────────

func TestHandleMe_ReturnsUserInfo(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)

	h, _, mockTokens := newTestAuthHandler()

	const userID = "me-user-123"
	enc, err := encryptToken("some-access-token", testEncKey)
	if err != nil {
		t.Fatalf("encryptToken: %v", err)
	}
	mockTokens.Upsert(context.Background(), db.OAuthToken{
		UserID:           userID,
		Provider:         "github",
		ProviderUsername: "octocat",
		AccessToken:      enc,
	})

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req = req.WithContext(context.WithValue(req.Context(), userIDKey, userID))
	w := httptest.NewRecorder()

	h.HandleMe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)

	if body["userID"] != userID {
		t.Errorf("userID: got %v, want %s", body["userID"], userID)
	}
	if body["provider"] != "github" {
		t.Errorf("provider: got %v, want github", body["provider"])
	}
	if body["username"] != "octocat" {
		t.Errorf("username: got %v, want octocat", body["username"])
	}
}

func TestHandleMe_PrefersGithubOverGitlab(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)

	h, _, mockTokens := newTestAuthHandler()

	const userID = "dual-provider-user"
	enc, _ := encryptToken("token", testEncKey)
	mockTokens.Upsert(context.Background(), db.OAuthToken{
		UserID: userID, Provider: "github", ProviderUsername: "gh-user", AccessToken: enc,
	})
	mockTokens.Upsert(context.Background(), db.OAuthToken{
		UserID: userID, Provider: "gitlab", ProviderUsername: "gl-user", AccessToken: enc,
	})

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req = req.WithContext(context.WithValue(req.Context(), userIDKey, userID))
	w := httptest.NewRecorder()

	h.HandleMe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	if body["provider"] != "github" {
		t.Errorf("expected github to take priority, got %v", body["provider"])
	}
}

func TestHandleMe_NoToken_Returns401(t *testing.T) {
	h, _, _ := newTestAuthHandler()

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req = req.WithContext(context.WithValue(req.Context(), userIDKey, "no-token-user"))
	w := httptest.NewRecorder()

	h.HandleMe(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// TestHandleMe_FullCookieFlow tests JWTMiddleware → HandleMe end-to-end.
func TestHandleMe_FullCookieFlow(t *testing.T) {
	t.Setenv("CODEATLAS_JWT_SECRET", testJWTSecret)
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)

	h, _, mockTokens := newTestAuthHandler()

	const userID = "full-flow-user"
	enc, _ := encryptToken("access-token", testEncKey)
	mockTokens.Upsert(context.Background(), db.OAuthToken{
		UserID:           userID,
		Provider:         "github",
		ProviderUsername: "flowuser",
		AccessToken:      enc,
	})

	tok := makeToken(t, jwt.MapClaims{
		"sub": userID,
		"exp": jwt.NewNumericDate(time.Now().Add(time.Hour)),
		"iat": jwt.NewNumericDate(time.Now()),
	})

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "codeatlas_jwt", Value: tok})
	w := httptest.NewRecorder()

	JWTMiddleware(http.HandlerFunc(h.HandleMe)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	if body["userID"] != userID {
		t.Errorf("userID: got %v, want %s", body["userID"], userID)
	}
	if body["username"] != "flowuser" {
		t.Errorf("username: got %v, want flowuser", body["username"])
	}
}

// ── HandleLogout tests ────────────────────────────────────────────────────────

func TestHandleLogout_ClearsCookie(t *testing.T) {
	h, _, _ := newTestAuthHandler()

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	w := httptest.NewRecorder()

	h.HandleLogout(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	c := jwtCookie(w)
	if c == nil {
		t.Fatal("expected codeatlas_jwt cookie in response (for clearing)")
	}
	if c.MaxAge != -1 {
		t.Errorf("expected MaxAge -1 (cleared), got %d", c.MaxAge)
	}
	if c.Value != "" {
		t.Errorf("expected empty cookie value, got %q", c.Value)
	}
}
