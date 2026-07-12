package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"CodeAtlas/internal/db"
)

// AuthHandler holds the repositories used by auth endpoints.
type AuthHandler struct {
	Users  db.UserRepository
	Tokens db.TokenRepository
}

// ── Shared helpers ────────────────────────────────────────────────────────────

func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func setStateCookie(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// validateState checks the "state" query param against the oauth_state cookie.
// Writes the error response and returns false on failure; clears the cookie and
// returns true on success.
func validateState(w http.ResponseWriter, r *http.Request) bool {
	stateParam := r.URL.Query().Get("state")
	cookie, err := r.Cookie("oauth_state")
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"code": "missing_cookie", "message": "missing oauth_state cookie"},
		})
		return false
	}
	if stateParam != cookie.Value {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"code": "invalid_state", "message": "state mismatch"},
		})
		return false
	}
	clearStateCookie(w)
	return true
}

func issueJWT(userID string) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub": userID,
		"exp": jwt.NewNumericDate(now.Add(24 * time.Hour)),
		"iat": jwt.NewNumericDate(now),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(os.Getenv("CODEATLAS_JWT_SECRET")))
}

// ── GitHub ────────────────────────────────────────────────────────────────────

// overridden in tests to point at mock servers
var (
	githubTokenEndpoint = "https://github.com/login/oauth/access_token"
	githubUserEndpoint  = "https://api.github.com/user"
)

type githubTokenResponse struct {
	AccessToken string `json:"access_token"`
	Scope       string `json:"scope"`
	TokenType   string `json:"token_type"`
}

type githubUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

func (h *AuthHandler) HandleGithubLogin(w http.ResponseWriter, r *http.Request) {
	state, err := generateState()
	if err != nil {
		http.Error(w, "failed to generate state", http.StatusInternalServerError)
		return
	}
	setStateCookie(w, state)

	params := url.Values{}
	params.Set("client_id", os.Getenv("CODEATLAS_GITHUB_CLIENT_ID"))
	params.Set("scope", "repo")
	params.Set("state", state)

	http.Redirect(w, r, "https://github.com/login/oauth/authorize?"+params.Encode(), http.StatusFound)
}

func (h *AuthHandler) HandleGithubCallback(w http.ResponseWriter, r *http.Request) {
	// Step 1: State validation
	if !validateState(w, r) {
		return
	}

	// Step 2: Code exchange
	code := r.URL.Query().Get("code")
	if code == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"code": "missing_code", "message": "missing code parameter"},
		})
		return
	}
	tokenResp, err := exchangeGithubCode(code)
	if err != nil || tokenResp.AccessToken == "" {
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"code": "token_exchange_failed", "message": "failed to exchange code for access token"},
		})
		return
	}

	// Step 3: Fetch GitHub user profile
	ghUser, err := fetchGithubUser(tokenResp.AccessToken)
	if err != nil {
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"code": "user_fetch_failed", "message": "failed to fetch GitHub user"},
		})
		return
	}

	// Step 4: Find or create user
	providerUserID := fmt.Sprintf("%d", ghUser.ID)
	userID, err := h.Users.FindOrCreateByProvider(r.Context(), "github", providerUserID, ghUser.Login)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"code": "db_error", "message": "failed to find or create user"},
		})
		return
	}

	// Step 5: Encrypt and upsert OAuth token
	encryptedToken, err := encryptToken(tokenResp.AccessToken, os.Getenv("CODEATLAS_ENCRYPTION_KEY"))
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"code": "encryption_failed", "message": "failed to encrypt access token"},
		})
		return
	}
	if err := h.Tokens.Upsert(r.Context(), db.OAuthToken{
		UserID:           userID,
		Provider:         "github",
		ProviderUserID:   providerUserID,
		ProviderUsername: ghUser.Login,
		AccessToken:      encryptedToken,
	}); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"code": "db_error", "message": "failed to store token"},
		})
		return
	}

	// Step 6: Issue JWT and set HttpOnly cookie
	signed, err := issueJWT(userID)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"code": "jwt_failed", "message": "failed to sign JWT"},
		})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "codeatlas_jwt",
		Value:    signed,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		MaxAge:   86400,
	})

	frontendURL := os.Getenv("CODEATLAS_FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:5173"
	}
	http.Redirect(w, r, frontendURL+"/repos", http.StatusFound)
}

func exchangeGithubCode(code string) (*githubTokenResponse, error) {
	body, err := json.Marshal(map[string]string{
		"client_id":     os.Getenv("CODEATLAS_GITHUB_CLIENT_ID"),
		"client_secret": os.Getenv("CODEATLAS_GITHUB_CLIENT_SECRET"),
		"code":          code,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, githubTokenEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var tokenResp githubTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, err
	}
	return &tokenResp, nil
}

func fetchGithubUser(accessToken string) (*githubUser, error) {
	req, err := http.NewRequest(http.MethodGet, githubUserEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub user API returned %d", resp.StatusCode)
	}

	var user githubUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, err
	}
	return &user, nil
}

// ── GitLab ────────────────────────────────────────────────────────────────────

// overridden in tests to point at mock servers
var (
	gitlabTokenEndpoint = "https://gitlab.com/oauth/token"
	gitlabUserEndpoint  = "https://gitlab.com/api/v4/user"
)

type gitlabTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

type gitlabUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

func (h *AuthHandler) HandleGitlabLogin(w http.ResponseWriter, r *http.Request) {
	state, err := generateState()
	if err != nil {
		http.Error(w, "failed to generate state", http.StatusInternalServerError)
		return
	}
	setStateCookie(w, state)

	params := url.Values{}
	params.Set("client_id", os.Getenv("CODEATLAS_GITLAB_CLIENT_ID"))
	params.Set("redirect_uri", os.Getenv("CODEATLAS_APP_BASE_URL")+"/auth/gitlab/callback")
	params.Set("response_type", "code")
	params.Set("scope", "read_repository write_repository")
	params.Set("state", state)

	http.Redirect(w, r, "https://gitlab.com/oauth/authorize?"+params.Encode(), http.StatusFound)
}

func (h *AuthHandler) HandleGitlabCallback(w http.ResponseWriter, r *http.Request) {
	// Step 1: State validation
	if !validateState(w, r) {
		return
	}

	// Step 2: Code exchange
	code := r.URL.Query().Get("code")
	if code == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"code": "missing_code", "message": "missing code parameter"},
		})
		return
	}
	tokenResp, err := exchangeGitlabCode(code)
	if err != nil || tokenResp.AccessToken == "" {
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"code": "token_exchange_failed", "message": "failed to exchange code for access token"},
		})
		return
	}

	// Step 3: Fetch GitLab user profile
	glUser, err := fetchGitlabUser(tokenResp.AccessToken)
	if err != nil {
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"code": "user_fetch_failed", "message": "failed to fetch GitLab user"},
		})
		return
	}

	// Step 4: Find or create user
	providerUserID := fmt.Sprintf("%d", glUser.ID)
	userID, err := h.Users.FindOrCreateByProvider(r.Context(), "gitlab", providerUserID, glUser.Username)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"code": "db_error", "message": "failed to find or create user"},
		})
		return
	}

	// Step 5: Encrypt and upsert OAuth token
	keyHex := os.Getenv("CODEATLAS_ENCRYPTION_KEY")
	encryptedAccess, err := encryptToken(tokenResp.AccessToken, keyHex)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"code": "encryption_failed", "message": "failed to encrypt access token"},
		})
		return
	}

	expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	oauthToken := db.OAuthToken{
		UserID:           userID,
		Provider:         "gitlab",
		ProviderUserID:   providerUserID,
		ProviderUsername: glUser.Username,
		AccessToken:      encryptedAccess,
		TokenExpiresAt:   &expiresAt,
	}

	if tokenResp.RefreshToken != "" {
		encryptedRefresh, err := encryptToken(tokenResp.RefreshToken, keyHex)
		if err != nil {
			WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"error": map[string]string{"code": "encryption_failed", "message": "failed to encrypt refresh token"},
			})
			return
		}
		oauthToken.RefreshToken = encryptedRefresh
	}

	if err := h.Tokens.Upsert(r.Context(), oauthToken); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"code": "db_error", "message": "failed to store token"},
		})
		return
	}

	// Step 6: Issue JWT and set HttpOnly cookie
	signed, err := issueJWT(userID)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"code": "jwt_failed", "message": "failed to sign JWT"},
		})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "codeatlas_jwt",
		Value:    signed,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		MaxAge:   86400,
	})

	frontendURL := os.Getenv("CODEATLAS_FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:5173"
	}
	http.Redirect(w, r, frontendURL+"/repos", http.StatusFound)
}

func exchangeGitlabCode(code string) (*gitlabTokenResponse, error) {
	body, err := json.Marshal(map[string]string{
		"client_id":     os.Getenv("CODEATLAS_GITLAB_CLIENT_ID"),
		"client_secret": os.Getenv("CODEATLAS_GITLAB_CLIENT_SECRET"),
		"code":          code,
		"grant_type":    "authorization_code",
		"redirect_uri":  os.Getenv("CODEATLAS_APP_BASE_URL") + "/auth/gitlab/callback",
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, gitlabTokenEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var tokenResp gitlabTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, err
	}
	return &tokenResp, nil
}

func fetchGitlabUser(accessToken string) (*gitlabUser, error) {
	req, err := http.NewRequest(http.MethodGet, gitlabUserEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitLab user API returned %d", resp.StatusCode)
	}

	var user gitlabUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, err
	}
	return &user, nil
}

// ── Logout ────────────────────────────────────────────────────────────────────

// HandleLogout clears the JWT cookie and returns 200.
func (h *AuthHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "codeatlas_jwt",
		Value:    "",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		MaxAge:   -1,
	})
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleMe returns the authenticated user's ID, provider, and username.
func (h *AuthHandler) HandleMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromContext(r)
	if !ok {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "unauthorized", "message": "unauthorized"},
		})
		return
	}

	var token *db.OAuthToken
	var provider string
	for _, p := range []string{"github", "gitlab"} {
		t, err := h.Tokens.FindByUserAndProvider(r.Context(), userID, p)
		if err != nil {
			WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"error": map[string]string{"code": "db_error", "message": "failed to look up token"},
			})
			return
		}
		if t != nil {
			token = t
			provider = p
			break
		}
	}
	if token == nil {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "no_token", "message": "no token found for user"},
		})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"userID":   userID,
		"provider": provider,
		"username": token.ProviderUsername,
	})
}
