package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"CodeAtlas/internal/core/committer"
	"CodeAtlas/internal/core/graph"
	"CodeAtlas/internal/worker"
)

// GraphRegistryEntry holds the in-memory state for a graph job.
type GraphRegistryEntry struct {
	mu           sync.RWMutex
	ID           string
	UserID       string
	Provider     string
	Owner        string
	Repo         string
	Branch       string
	Status       string // "processing" | "ready" | "failed"
	ErrorMsg     string
	LastOpenedAt time.Time
	CreatedAt    time.Time
}

type graphSummary struct {
	ID           string    `json:"id"`
	Provider     string    `json:"provider"`
	Owner        string    `json:"owner"`
	Repo         string    `json:"repo"`
	Branch       string    `json:"branch"`
	Status       string    `json:"status"`
	ErrorMsg     string    `json:"errorMsg,omitempty"`
	LastOpenedAt time.Time `json:"lastOpenedAt"`
	CreatedAt    time.Time `json:"createdAt"`
}

var (
	graphsByID   sync.Map // graphID → *GraphRegistryEntry
	graphsByRepo sync.Map // dedupKey → graphID
)

// spawnWorker is overridden in tests to avoid real network calls.
var spawnWorker = func(w *worker.Worker) {
	go w.Run(context.Background())
}

func graphDedupKey(userID, provider, owner, repo, branch string) string {
	return userID + ":" + provider + ":" + owner + ":" + repo + ":" + branch
}

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

// fetchGraphFile mirrors worker.fetchExistingGraph but returns a full GraphRecord.
func fetchGraphFile(provider, owner, repo, branch, token string) (graph.GraphRecord, error) {
	rec, _, err := fetchGraphFileWithSHA(provider, owner, repo, branch, token)
	return rec, err
}

// ── Request body ──────────────────────────────────────────────────────────────

type createGraphBody struct {
	Provider string `json:"provider"`
	Owner    string `json:"owner"`
	Repo     string `json:"repo"`
	Branch   string `json:"branch"`
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func HandleCreateGraph(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromContext(r)
	if !ok {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "unauthorized", "message": "unauthorized"},
		})
		return
	}

	var body createGraphBody
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

	token, err := resolveToken(body.Provider, userID)
	if err != nil {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "no_token", "message": "no token found for provider"},
		})
		return
	}

	key := graphDedupKey(userID, body.Provider, body.Owner, body.Repo, body.Branch)
	now := time.Now()

	var graphID string
	if existing, ok := graphsByRepo.Load(key); ok {
		graphID = existing.(string)
		if val, ok := graphsByID.Load(graphID); ok {
			entry := val.(*GraphRegistryEntry)
			entry.mu.Lock()
			entry.Status = "processing"
			entry.LastOpenedAt = now
			entry.mu.Unlock()
		}
	} else {
		graphID = uuid.New().String()
		entry := &GraphRegistryEntry{
			ID:           graphID,
			UserID:       userID,
			Provider:     body.Provider,
			Owner:        body.Owner,
			Repo:         body.Repo,
			Branch:       body.Branch,
			Status:       "processing",
			LastOpenedAt: now,
			CreatedAt:    now,
		}
		graphsByID.Store(graphID, entry)
		graphsByRepo.Store(key, graphID)
	}

	wk := &worker.Worker{
		Owner:    body.Owner,
		Repo:     body.Repo,
		Branch:   body.Branch,
		Provider: body.Provider,
		Token:    token,
		GraphID:  graphID,
		OnComplete: func(gid, status, errMsg string) {
			if val, ok := graphsByID.Load(gid); ok {
				entry := val.(*GraphRegistryEntry)
				entry.mu.Lock()
				entry.Status = status
				entry.ErrorMsg = errMsg
				entry.mu.Unlock()
			}
		},
	}
	spawnWorker(wk)

	WriteJSON(w, http.StatusAccepted, map[string]string{
		"graphId": graphID,
		"status":  "processing",
	})
}

func HandleGetGraph(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID, ok := UserIDFromContext(r)
	if !ok {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "unauthorized", "message": "unauthorized"},
		})
		return
	}

	val, ok := graphsByID.Load(id)
	if !ok {
		WriteJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]string{"code": "not_found", "message": "graph not found"},
		})
		return
	}

	entry := val.(*GraphRegistryEntry)
	entry.mu.RLock()
	ownerID := entry.UserID
	status := entry.Status
	errMsg := entry.ErrorMsg
	provider := entry.Provider
	owner := entry.Owner
	repo := entry.Repo
	branch := entry.Branch
	entry.mu.RUnlock()

	if ownerID != userID {
		WriteJSON(w, http.StatusForbidden, map[string]any{
			"error": map[string]string{"code": "forbidden", "message": "access denied"},
		})
		return
	}

	switch status {
	case "processing":
		WriteJSON(w, http.StatusOK, map[string]string{"status": "processing"})
	case "failed":
		WriteJSON(w, http.StatusOK, map[string]any{"status": "failed", "error": errMsg})
	case "ready":
		token, err := resolveToken(provider, userID)
		if err != nil {
			WriteJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]string{"code": "no_token", "message": "no token found for provider"},
			})
			return
		}
		rec, err := fetchGraphFile(provider, owner, repo, branch, token)
		if err != nil {
			WriteJSON(w, http.StatusBadGateway, map[string]any{
				"error": map[string]string{"code": "fetch_failed", "message": "failed to fetch graph file"},
			})
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"status": "ready", "graph": rec})
	default:
		WriteJSON(w, http.StatusOK, map[string]string{"status": status})
	}
}

func HandleListGraphs(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromContext(r)
	if !ok {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "unauthorized", "message": "unauthorized"},
		})
		return
	}

	var summaries []graphSummary
	graphsByID.Range(func(_, val any) bool {
		entry := val.(*GraphRegistryEntry)
		entry.mu.RLock()
		if entry.UserID == userID {
			summaries = append(summaries, graphSummary{
				ID:           entry.ID,
				Provider:     entry.Provider,
				Owner:        entry.Owner,
				Repo:         entry.Repo,
				Branch:       entry.Branch,
				Status:       entry.Status,
				ErrorMsg:     entry.ErrorMsg,
				LastOpenedAt: entry.LastOpenedAt,
				CreatedAt:    entry.CreatedAt,
			})
		}
		entry.mu.RUnlock()
		return true
	})

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].LastOpenedAt.After(summaries[j].LastOpenedAt)
	})

	if summaries == nil {
		summaries = []graphSummary{}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"graphs": summaries})
}

func HandleDeleteGraph(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID, ok := UserIDFromContext(r)
	if !ok {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "unauthorized", "message": "unauthorized"},
		})
		return
	}

	val, ok := graphsByID.Load(id)
	if !ok {
		WriteJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]string{"code": "not_found", "message": "graph not found"},
		})
		return
	}

	entry := val.(*GraphRegistryEntry)
	entry.mu.RLock()
	ownerID := entry.UserID
	provider := entry.Provider
	owner := entry.Owner
	repo := entry.Repo
	branch := entry.Branch
	entry.mu.RUnlock()

	if ownerID != userID {
		WriteJSON(w, http.StatusForbidden, map[string]any{
			"error": map[string]string{"code": "forbidden", "message": "access denied"},
		})
		return
	}

	graphsByID.Delete(id)
	graphsByRepo.Delete(graphDedupKey(userID, provider, owner, repo, branch))
	w.WriteHeader(http.StatusNoContent)
}

// loadGraphForWrite validates auth, loads the registry entry, decrypts the token,
// and fetches the current graph.json. It writes the appropriate error response and
// returns ok=false on any failure.
func loadGraphForWrite(w http.ResponseWriter, r *http.Request, graphID string) (record graph.GraphRecord, blobSHA string, token string, ok bool) {
	userID, authOK := UserIDFromContext(r)
	if !authOK {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "unauthorized", "message": "unauthorized"},
		})
		return
	}

	val, found := graphsByID.Load(graphID)
	if !found {
		WriteJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]string{"code": "not_found", "message": "graph not found"},
		})
		return
	}

	entry := val.(*GraphRegistryEntry)
	entry.mu.RLock()
	ownerID := entry.UserID
	provider := entry.Provider
	owner := entry.Owner
	repo := entry.Repo
	branch := entry.Branch
	entry.mu.RUnlock()

	if ownerID != userID {
		WriteJSON(w, http.StatusForbidden, map[string]any{
			"error": map[string]string{"code": "forbidden", "message": "access denied"},
		})
		return
	}

	tok, err := resolveToken(provider, userID)
	if err != nil {
		WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"code": "no_token", "message": "no token found for provider"},
		})
		return
	}

	rec, sha, err := fetchGraphFileWithSHA(provider, owner, repo, branch, tok)
	if err != nil {
		WriteJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]string{"code": "not_found", "message": "graph file not found"},
		})
		return
	}

	return rec, sha, tok, true
}

// entryMeta reads provider/owner/repo/branch from the registry without locking
// the caller's flow; safe to call after loadGraphForWrite has confirmed the entry
// exists and the caller is authorised.
func entryMeta(graphID string) (provider, owner, repo, branch string) {
	val, _ := graphsByID.Load(graphID)
	e := val.(*GraphRegistryEntry)
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.Provider, e.Owner, e.Repo, e.Branch
}

func HandleUpdateNodeDescription(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	nodeID := chi.URLParam(r, "nodeId")

	record, blobSHA, token, ok := loadGraphForWrite(w, r, id)
	if !ok {
		return
	}

	var body struct {
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"code": "invalid_body", "message": "invalid request body"},
		})
		return
	}

	nodeIdx := -1
	for i, n := range record.Nodes {
		if n.UUID == nodeID {
			nodeIdx = i
			break
		}
	}
	if nodeIdx == -1 {
		WriteJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]string{"code": "not_found", "message": "node not found"},
		})
		return
	}
	record.Nodes[nodeIdx].Description = body.Description

	provider, owner, repo, branch := entryMeta(id)
	c := &committer.Committer{Provider: provider, Token: token}
	if _, err := c.CommitGraphFile(r.Context(), owner, repo, branch, record, blobSHA); err != nil {
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"code": "commit_failed", "message": "failed to commit graph file"},
		})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"committedAt": time.Now().UTC().Format(time.RFC3339)})
}

func HandleAddNote(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	nodeID := chi.URLParam(r, "nodeId")

	record, blobSHA, token, ok := loadGraphForWrite(w, r, id)
	if !ok {
		return
	}

	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"code": "invalid_body", "message": "invalid request body"},
		})
		return
	}

	nodeIdx := -1
	for i, n := range record.Nodes {
		if n.UUID == nodeID {
			nodeIdx = i
			break
		}
	}
	if nodeIdx == -1 {
		WriteJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]string{"code": "not_found", "message": "node not found"},
		})
		return
	}
	record.Nodes[nodeIdx].Note = body.Content

	provider, owner, repo, branch := entryMeta(id)
	c := &committer.Committer{Provider: provider, Token: token}
	if _, err := c.CommitGraphFile(r.Context(), owner, repo, branch, record, blobSHA); err != nil {
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"code": "commit_failed", "message": "failed to commit graph file"},
		})
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]string{"committedAt": time.Now().UTC().Format(time.RFC3339)})
}

func HandleUpdateNote(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	noteID := chi.URLParam(r, "noteId")

	record, blobSHA, token, ok := loadGraphForWrite(w, r, id)
	if !ok {
		return
	}

	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"code": "invalid_body", "message": "invalid request body"},
		})
		return
	}

	nodeIdx := -1
	for i, n := range record.Nodes {
		if n.UUID == noteID {
			nodeIdx = i
			break
		}
	}
	if nodeIdx == -1 {
		WriteJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]string{"code": "not_found", "message": "node not found"},
		})
		return
	}
	record.Nodes[nodeIdx].Note = body.Content

	provider, owner, repo, branch := entryMeta(id)
	c := &committer.Committer{Provider: provider, Token: token}
	if _, err := c.CommitGraphFile(r.Context(), owner, repo, branch, record, blobSHA); err != nil {
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"code": "commit_failed", "message": "failed to commit graph file"},
		})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"committedAt": time.Now().UTC().Format(time.RFC3339)})
}

func HandleDeleteNote(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	noteID := chi.URLParam(r, "noteId")

	record, blobSHA, token, ok := loadGraphForWrite(w, r, id)
	if !ok {
		return
	}

	nodeIdx := -1
	for i, n := range record.Nodes {
		if n.UUID == noteID {
			nodeIdx = i
			break
		}
	}
	if nodeIdx == -1 {
		WriteJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]string{"code": "not_found", "message": "node not found"},
		})
		return
	}
	record.Nodes[nodeIdx].Note = ""

	provider, owner, repo, branch := entryMeta(id)
	c := &committer.Committer{Provider: provider, Token: token}
	if _, err := c.CommitGraphFile(r.Context(), owner, repo, branch, record, blobSHA); err != nil {
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"code": "commit_failed", "message": "failed to commit graph file"},
		})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"committedAt": time.Now().UTC().Format(time.RFC3339)})
}

func HandleUpdateLayout(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	record, blobSHA, token, ok := loadGraphForWrite(w, r, id)
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

	provider, owner, repo, branch := entryMeta(id)
	c := &committer.Committer{Provider: provider, Token: token}
	if _, err := c.CommitGraphFile(r.Context(), owner, repo, branch, record, blobSHA); err != nil {
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"code": "commit_failed", "message": "failed to commit graph file"},
		})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"committedAt": time.Now().UTC().Format(time.RFC3339)})
}
