package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"

	"CodeAtlas/internal/db"
)

// ReposHandler holds the repositories used by repo/branch listing endpoints.
type ReposHandler struct {
	Tokens db.TokenRepository
}

// overridden in tests to point at mock servers
var (
	githubAPIBase = "https://api.github.com"
	gitlabAPIBase = "https://gitlab.com"
)

var errNoToken = errors.New("no token found")

// resolveToken fetches and decrypts the stored access token for the given provider and userID.
func resolveToken(ctx context.Context, tokens db.TokenRepository, provider, userID string) (string, error) {
	t, err := tokens.FindByUserAndProvider(ctx, userID, provider)
	if err != nil {
		return "", err
	}
	if t == nil {
		return "", errNoToken
	}
	return decryptToken(t.AccessToken, os.Getenv("CODEATLAS_ENCRYPTION_KEY"))
}

func parsePage(r *http.Request) string {
	if p := r.URL.Query().Get("page"); p != "" {
		return p
	}
	return "1"
}

func parseNextPage(header string) int {
	if header == "" {
		return 0
	}
	n, err := strconv.Atoi(header)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// ── Output types ──────────────────────────────────────────────────────────────

type repoItem struct {
	FullName      string `json:"fullName"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"defaultBranch"`
}

// ── Provider raw types ────────────────────────────────────────────────────────

type githubRepoRaw struct {
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
}

type gitlabProjectRaw struct {
	PathWithNamespace string `json:"path_with_namespace"`
	Visibility        string `json:"visibility"`
	DefaultBranch     string `json:"default_branch"`
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (h *ReposHandler) HandleListRepos(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	if provider != "github" && provider != "gitlab" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"code": "invalid_provider", "message": "provider must be github or gitlab"},
		})
		return
	}

	userID, ok := UserIDFromContext(r)
	if !ok {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "unauthorized", "message": "unauthorized"},
		})
		return
	}

	token, err := resolveToken(r.Context(), h.Tokens, provider, userID)
	if err != nil {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "no_token", "message": "no token found for provider"},
		})
		return
	}

	page := parsePage(r)

	var apiURL string
	switch provider {
	case "github":
		apiURL = fmt.Sprintf("%s/user/repos?per_page=50&page=%s&sort=updated", githubAPIBase, page)
	case "gitlab":
		apiURL = fmt.Sprintf("%s/api/v4/projects?membership=true&per_page=50&page=%s&order_by=last_activity_at", gitlabAPIBase, page)
	}

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"code": "internal", "message": "failed to build request"},
		})
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if provider == "github" {
		req.Header.Set("Accept", "application/vnd.github+json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"code": "provider_error", "message": "failed to fetch repos from provider"},
		})
		return
	}
	defer resp.Body.Close()

	nextPage := parseNextPage(resp.Header.Get("X-Next-Page"))

	repos := make([]repoItem, 0)
	switch provider {
	case "github":
		var raw []githubRepoRaw
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			WriteJSON(w, http.StatusBadGateway, map[string]any{
				"error": map[string]string{"code": "parse_error", "message": "failed to parse provider response"},
			})
			return
		}
		for _, gr := range raw {
			repos = append(repos, repoItem{FullName: gr.FullName, Private: gr.Private, DefaultBranch: gr.DefaultBranch})
		}
	case "gitlab":
		var raw []gitlabProjectRaw
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			WriteJSON(w, http.StatusBadGateway, map[string]any{
				"error": map[string]string{"code": "parse_error", "message": "failed to parse provider response"},
			})
			return
		}
		for _, gp := range raw {
			repos = append(repos, repoItem{
				FullName:      gp.PathWithNamespace,
				Private:       gp.Visibility == "private",
				DefaultBranch: gp.DefaultBranch,
			})
		}
	}

	WriteJSON(w, http.StatusOK, map[string]any{"repos": repos, "nextPage": nextPage})
}

func (h *ReposHandler) HandleListBranches(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	owner := chi.URLParam(r, "owner")
	repo := chi.URLParam(r, "repo")

	if provider != "github" && provider != "gitlab" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"code": "invalid_provider", "message": "provider must be github or gitlab"},
		})
		return
	}

	userID, ok := UserIDFromContext(r)
	if !ok {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "unauthorized", "message": "unauthorized"},
		})
		return
	}

	token, err := resolveToken(r.Context(), h.Tokens, provider, userID)
	if err != nil {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "no_token", "message": "no token found for provider"},
		})
		return
	}

	page := parsePage(r)

	var apiURL string
	switch provider {
	case "github":
		apiURL = fmt.Sprintf("%s/repos/%s/%s/branches?per_page=50&page=%s", githubAPIBase, owner, repo, page)
	case "gitlab":
		// %2F is the URL-encoded slash between owner and repo for GitLab's project path
		apiURL = fmt.Sprintf("%s/api/v4/projects/%s%%2F%s/repository/branches?per_page=50&page=%s", gitlabAPIBase, owner, repo, page)
	}

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"code": "internal", "message": "failed to build request"},
		})
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"code": "provider_error", "message": "failed to fetch branches from provider"},
		})
		return
	}
	defer resp.Body.Close()

	nextPage := parseNextPage(resp.Header.Get("X-Next-Page"))

	var rawBranches []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rawBranches); err != nil {
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"code": "parse_error", "message": "failed to parse provider response"},
		})
		return
	}

	names := make([]string, 0, len(rawBranches))
	for _, b := range rawBranches {
		names = append(names, b.Name)
	}

	WriteJSON(w, http.StatusOK, map[string]any{"branches": names, "nextPage": nextPage})
}
