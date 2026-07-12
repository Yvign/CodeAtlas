package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"CodeAtlas/internal/core/committer"
	"CodeAtlas/internal/core/graph"
	"CodeAtlas/internal/db"
	"CodeAtlas/internal/worker"
)

// inMemoryGraphs holds GraphRecords produced with CommitEnabled=false, keyed
// by graphID, since there's no committed .codeatlas/graph.json to fetch back.
var inMemoryGraphs sync.Map

// GraphsHandler holds the repositories used by graph endpoints.
type GraphsHandler struct {
	Graphs db.GraphRepository
	Tokens db.TokenRepository
}

type graphSummary struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	Owner     string    `json:"owner"`
	Repo      string    `json:"repo"`
	Branch    string    `json:"branch"`
	Status    string    `json:"status"`
	ErrorMsg  string    `json:"errorMsg,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// spawnWorker is overridden in tests to avoid real network calls.
var spawnWorker = func(w *worker.Worker) {
	go w.Run(context.Background())
}

// ── Request body ──────────────────────────────────────────────────────────────

type createGraphRequest struct {
	Provider string `json:"provider"`
	Owner    string `json:"owner"`
	Repo     string `json:"repo"`
	Branch   string `json:"branch"`
	Commit   *bool  `json:"commit"`
}

// ── Provider helpers ──────────────────────────────────────────────────────────

// fetchGraphFileWithSHA fetches .codeatlas/graph.json and returns the decoded
// record together with the blob SHA needed for subsequent writes.
func fetchGraphFileWithSHA(provider, owner, repo, branch, token string) (graph.GraphRecord, string, error) {
	var apiURL string
	switch provider {
	case "github":
		apiURL = fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/.codeatlas/graph.json", owner, repo)
	case "gitlab":
		apiURL = fmt.Sprintf("https://gitlab.com/api/v4/projects/%s%%2F%s/repository/files/.codeatlas%%2Fgraph.json?ref=%s", owner, repo, branch)
	default:
		return graph.GraphRecord{}, "", fmt.Errorf("unknown provider %q", provider)
	}

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return graph.GraphRecord{}, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return graph.GraphRecord{}, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return graph.GraphRecord{}, "", fmt.Errorf("fetch graph.json: status %d", resp.StatusCode)
	}

	var rawContent, blobSHA string
	switch provider {
	case "github":
		var ghResp struct {
			SHA     string `json:"sha"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&ghResp); err != nil {
			return graph.GraphRecord{}, "", fmt.Errorf("decode github response: %w", err)
		}
		rawContent = ghResp.Content
		blobSHA = ghResp.SHA
	case "gitlab":
		var glResp struct {
			BlobID  string `json:"blob_id"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&glResp); err != nil {
			return graph.GraphRecord{}, "", fmt.Errorf("decode gitlab response: %w", err)
		}
		rawContent = glResp.Content
		blobSHA = glResp.BlobID
	}

	rawContent = strings.ReplaceAll(rawContent, "\n", "")
	decoded, err := base64.StdEncoding.DecodeString(rawContent)
	if err != nil {
		return graph.GraphRecord{}, "", fmt.Errorf("decode base64: %w", err)
	}
	var rec graph.GraphRecord
	if err := json.Unmarshal(decoded, &rec); err != nil {
		return graph.GraphRecord{}, "", fmt.Errorf("unmarshal graph record: %w", err)
	}
	return rec, blobSHA, nil
}

func fetchGraphFile(provider, owner, repo, branch, token string) (graph.GraphRecord, error) {
	rec, _, err := fetchGraphFileWithSHA(provider, owner, repo, branch, token)
	return rec, err
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (h *GraphsHandler) HandleCreateGraph(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromContext(r)
	if !ok {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "unauthorized", "message": "unauthorized"},
		})
		return
	}

	var body createGraphRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"code": "invalid_body", "message": "invalid request body"},
		})
		return
	}
	if body.Provider == "" || body.Owner == "" || body.Repo == "" || body.Branch == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"code": "missing_fields", "message": "provider, owner, repo, and branch are required"},
		})
		return
	}
	if body.Provider != "github" && body.Provider != "gitlab" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"code": "invalid_provider", "message": "provider must be github or gitlab"},
		})
		return
	}

	token, err := resolveToken(r.Context(), h.Tokens, body.Provider, userID)
	if err != nil {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "no_token", "message": "no token found for provider"},
		})
		return
	}

	rec, err := h.Graphs.FindOrCreate(r.Context(), db.GraphRecord{
		UserID:   userID,
		Provider: body.Provider,
		Owner:    body.Owner,
		RepoName: body.Repo,
		Branch:   body.Branch,
	})
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"code": "db_error", "message": "failed to create graph entry"},
		})
		return
	}

	commitEnabled := true
	if body.Commit != nil {
		commitEnabled = *body.Commit
	}

	wk := &worker.Worker{
		Owner:         body.Owner,
		Repo:          body.Repo,
		Branch:        body.Branch,
		Provider:      body.Provider,
		Token:         token,
		GraphID:       rec.ID,
		CommitEnabled: commitEnabled,
		OnComplete: func(gid, status, errMsg string, record *graph.GraphRecord) {
			if record != nil {
				inMemoryGraphs.Store(gid, *record)
			}
			h.Graphs.UpdateStatus(context.Background(), gid, status, errMsg)
		},
	}
	spawnWorker(wk)

	WriteJSON(w, http.StatusAccepted, map[string]string{
		"graphId": rec.ID,
		"status":  "processing",
	})
}

func (h *GraphsHandler) HandleGetGraph(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID, ok := UserIDFromContext(r)
	if !ok {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "unauthorized", "message": "unauthorized"},
		})
		return
	}

	rec, err := h.Graphs.FindByID(r.Context(), id)
	if err != nil || rec == nil {
		WriteJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]string{"code": "not_found", "message": "graph not found"},
		})
		return
	}

	if rec.UserID != userID {
		WriteJSON(w, http.StatusForbidden, map[string]any{
			"error": map[string]string{"code": "forbidden", "message": "access denied"},
		})
		return
	}

	switch rec.Status {
	case "processing":
		WriteJSON(w, http.StatusOK, map[string]string{"status": "processing"})
	case "failed":
		WriteJSON(w, http.StatusOK, map[string]any{"status": "failed", "error": rec.ErrorMessage})
	case "ready":
		if stored, ok := inMemoryGraphs.Load(id); ok {
			WriteJSON(w, http.StatusOK, map[string]any{"status": "ready", "graph": stored})
			return
		}
		token, err := resolveToken(r.Context(), h.Tokens, rec.Provider, userID)
		if err != nil {
			WriteJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]string{"code": "no_token", "message": "no token found for provider"},
			})
			return
		}
		graphRec, err := fetchGraphFile(rec.Provider, rec.Owner, rec.RepoName, rec.Branch, token)
		if err != nil {
			WriteJSON(w, http.StatusBadGateway, map[string]any{
				"error": map[string]string{"code": "fetch_failed", "message": "failed to fetch graph file"},
			})
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"status": "ready", "graph": graphRec})
	default:
		WriteJSON(w, http.StatusOK, map[string]string{"status": rec.Status})
	}
}

func (h *GraphsHandler) HandleListGraphs(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromContext(r)
	if !ok {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "unauthorized", "message": "unauthorized"},
		})
		return
	}

	records, err := h.Graphs.FindByUser(r.Context(), userID)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"code": "db_error", "message": "failed to list graphs"},
		})
		return
	}

	summaries := make([]graphSummary, 0, len(records))
	for _, rec := range records {
		summaries = append(summaries, graphSummary{
			ID:        rec.ID,
			Provider:  rec.Provider,
			Owner:     rec.Owner,
			Repo:      rec.RepoName,
			Branch:    rec.Branch,
			Status:    rec.Status,
			ErrorMsg:  rec.ErrorMessage,
			CreatedAt: rec.CreatedAt,
		})
	}

	WriteJSON(w, http.StatusOK, map[string]any{"graphs": summaries})
}

func (h *GraphsHandler) HandleDeleteGraph(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID, ok := UserIDFromContext(r)
	if !ok {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "unauthorized", "message": "unauthorized"},
		})
		return
	}

	rec, err := h.Graphs.FindByID(r.Context(), id)
	if err != nil || rec == nil {
		WriteJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]string{"code": "not_found", "message": "graph not found"},
		})
		return
	}

	if rec.UserID != userID {
		WriteJSON(w, http.StatusForbidden, map[string]any{
			"error": map[string]string{"code": "forbidden", "message": "access denied"},
		})
		return
	}

	if err := h.Graphs.Delete(r.Context(), id); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"code": "db_error", "message": "failed to delete graph"},
		})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// loadGraphForWrite validates auth, loads the registry entry, decrypts the token,
// and fetches the current graph.json. It writes the appropriate error response and
// returns ok=false on any failure.
func (h *GraphsHandler) loadGraphForWrite(w http.ResponseWriter, r *http.Request, graphID string) (record graph.GraphRecord, blobSHA string, token string, meta *db.GraphRecord, ok bool) {
	userID, authOK := UserIDFromContext(r)
	if !authOK {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "unauthorized", "message": "unauthorized"},
		})
		return
	}

	rec, err := h.Graphs.FindByID(r.Context(), graphID)
	if err != nil || rec == nil {
		WriteJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]string{"code": "not_found", "message": "graph not found"},
		})
		return
	}

	if rec.UserID != userID {
		WriteJSON(w, http.StatusForbidden, map[string]any{
			"error": map[string]string{"code": "forbidden", "message": "access denied"},
		})
		return
	}

	tok, err := resolveToken(r.Context(), h.Tokens, rec.Provider, userID)
	if err != nil {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "no_token", "message": "no token found for provider"},
		})
		return
	}

	graphRecord, sha, err := fetchGraphFileWithSHA(rec.Provider, rec.Owner, rec.RepoName, rec.Branch, tok)
	if err != nil {
		WriteJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]string{"code": "graph_not_committed", "message": "graph has not been committed to the repository yet"},
		})
		return
	}

	return graphRecord, sha, tok, rec, true
}

// HandleCommitGraph commits an in-memory GraphRecord (produced with
// CommitEnabled=false) to the repository as .codeatlas/graph.json for the
// first time, so subsequent node/layout writes can find it.
func (h *GraphsHandler) HandleCommitGraph(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID, ok := UserIDFromContext(r)
	if !ok {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "unauthorized", "message": "unauthorized"},
		})
		return
	}

	rec, err := h.Graphs.FindByID(r.Context(), id)
	if err != nil || rec == nil {
		WriteJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]string{"code": "not_found", "message": "graph not found"},
		})
		return
	}

	if rec.UserID != userID {
		WriteJSON(w, http.StatusForbidden, map[string]any{
			"error": map[string]string{"code": "forbidden", "message": "access denied"},
		})
		return
	}

	stored, found := inMemoryGraphs.Load(id)
	if !found {
		WriteJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]string{"code": "already_committed", "message": "graph has no pending in-memory data to commit"},
		})
		return
	}
	record := stored.(graph.GraphRecord)

	token, err := resolveToken(r.Context(), h.Tokens, rec.Provider, userID)
	if err != nil {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "no_token", "message": "no token found for provider"},
		})
		return
	}

	c := &committer.Committer{Provider: rec.Provider, Token: token}
	if _, err := c.CommitGraphFile(r.Context(), rec.Owner, rec.RepoName, rec.Branch, record, ""); err != nil {
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"code": "commit_failed", "message": "failed to commit graph file"},
		})
		return
	}

	inMemoryGraphs.Delete(id)

	WriteJSON(w, http.StatusOK, map[string]string{"committedAt": time.Now().UTC().Format(time.RFC3339)})
}

// HandleUpdateNodeDescriptions applies a batch of pending description edits
// (accumulated client-side across any number of nodes) in a single commit,
// mirroring HandleUpdateLayout's batching of position edits.
func (h *GraphsHandler) HandleUpdateNodeDescriptions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	record, blobSHA, token, meta, ok := h.loadGraphForWrite(w, r, id)
	if !ok {
		return
	}

	var updates []struct {
		NodeID      string `json:"nodeId"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"code": "invalid_body", "message": "invalid request body"},
		})
		return
	}

	byUUID := make(map[string]int, len(record.Nodes))
	for i, n := range record.Nodes {
		byUUID[n.UUID] = i
	}
	for _, u := range updates {
		if i, found := byUUID[u.NodeID]; found {
			record.Nodes[i].Description = u.Description
		}
	}

	c := &committer.Committer{Provider: meta.Provider, Token: token}
	if _, err := c.CommitGraphFile(r.Context(), meta.Owner, meta.RepoName, meta.Branch, record, blobSHA); err != nil {
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"code": "commit_failed", "message": "failed to commit graph file"},
		})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"committedAt": time.Now().UTC().Format(time.RFC3339)})
}

func (h *GraphsHandler) HandleUpdateLayout(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	record, blobSHA, token, meta, ok := h.loadGraphForWrite(w, r, id)
	if !ok {
		return
	}

	var positions []struct {
		NodeID string  `json:"nodeId"`
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
	}
	if err := json.NewDecoder(r.Body).Decode(&positions); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"code": "invalid_body", "message": "invalid request body"},
		})
		return
	}

	byUUID := make(map[string]int, len(record.Nodes))
	for i, n := range record.Nodes {
		byUUID[n.UUID] = i
	}
	for _, p := range positions {
		if i, found := byUUID[p.NodeID]; found {
			record.Nodes[i].Pos.X = p.X
			record.Nodes[i].Pos.Y = p.Y
		}
	}

	c := &committer.Committer{Provider: meta.Provider, Token: token}
	if _, err := c.CommitGraphFile(r.Context(), meta.Owner, meta.RepoName, meta.Branch, record, blobSHA); err != nil {
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"code": "commit_failed", "message": "failed to commit graph file"},
		})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"committedAt": time.Now().UTC().Format(time.RFC3339)})
}
