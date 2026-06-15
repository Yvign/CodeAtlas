package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"CodeAtlas/internal/core/graph"
	"CodeAtlas/internal/worker"
)

// ── test helpers ──────────────────────────────────────────────────────────────

func graphJSONBody(provider, owner, repo, branch string) *bytes.Reader {
	b, _ := json.Marshal(createGraphBody{
		Provider: provider, Owner: owner, Repo: repo, Branch: branch,
	})
	return bytes.NewReader(b)
}

func postGraphReq(userID, provider, owner, repo, branch string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/graphs", graphJSONBody(provider, owner, repo, branch))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), userIDKey, userID)
	return req.WithContext(ctx)
}

func graphIDReq(method, userID, graphID string) *http.Request {
	req := httptest.NewRequest(method, "/graphs/"+graphID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", graphID)
	ctx := context.WithValue(req.Context(), userIDKey, userID)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	return req.WithContext(ctx)
}

func listGraphsReq(userID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/graphs", nil)
	ctx := context.WithValue(req.Context(), userIDKey, userID)
	return req.WithContext(ctx)
}

// overrideSpawnWorker replaces spawnWorker with a no-op and registers cleanup.
func overrideSpawnWorker(t *testing.T) {
	t.Helper()
	orig := spawnWorker
	spawnWorker = func(_ *worker.Worker) {}
	t.Cleanup(func() { spawnWorker = orig })
}

// cleanupUser removes all sync.Map entries created for a userID.
func cleanupUser(userID string) {
	graphsByID.Range(func(k, v any) bool {
		if v.(*GraphRegistryEntry).UserID == userID {
			graphsByID.Delete(k)
		}
		return true
	})
	graphsByRepo.Range(func(k, v any) bool {
		if strings.HasPrefix(k.(string), userID+":") {
			graphsByRepo.Delete(k)
		}
		return true
	})
}

// uniqueUser generates a unique test userID and schedules cleanup.
func uniqueUser(t *testing.T) string {
	t.Helper()
	id := "test-" + uuid.New().String()
	storeGithubToken(t, id, "gh-access-token")
	t.Cleanup(func() { cleanupUser(id) })
	return id
}

// doCreate posts a create-graph request and returns the decoded body.
func doCreate(t *testing.T, userID, provider, owner, repo, branch string) (code int, graphID string) {
	t.Helper()
	w := httptest.NewRecorder()
	HandleCreateGraph(w, postGraphReq(userID, provider, owner, repo, branch))
	var body struct {
		GraphID string `json:"graphId"`
	}
	json.NewDecoder(w.Body).Decode(&body)
	return w.Code, body.GraphID
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestCreateGraph_Valid(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	overrideSpawnWorker(t)
	userID := uniqueUser(t)

	code, graphID := doCreate(t, userID, "github", "alice", "myrepo", "main")

	if code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", code)
	}
	if graphID == "" {
		t.Fatal("expected non-empty graphId")
	}

	// Entry should be in the store
	if _, ok := graphsByID.Load(graphID); !ok {
		t.Errorf("graphsByID missing entry for %s", graphID)
	}
}

func TestCreateGraph_Dedup(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	overrideSpawnWorker(t)
	userID := uniqueUser(t)

	code1, id1 := doCreate(t, userID, "github", "alice", "myrepo", "main")
	code2, id2 := doCreate(t, userID, "github", "alice", "myrepo", "main")

	if code1 != http.StatusAccepted || code2 != http.StatusAccepted {
		t.Fatalf("expected 202/202, got %d/%d", code1, code2)
	}
	if id1 != id2 {
		t.Errorf("dedup failed: first=%s second=%s", id1, id2)
	}
}

func TestGetGraph_Processing(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	overrideSpawnWorker(t)
	userID := uniqueUser(t)

	_, graphID := doCreate(t, userID, "github", "alice", "myrepo", "main")

	w := httptest.NewRecorder()
	HandleGetGraph(w, graphIDReq(http.MethodGet, userID, graphID))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	if body["status"] != "processing" {
		t.Errorf("expected status 'processing', got %v", body["status"])
	}
}

func TestGetGraph_Forbidden(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	overrideSpawnWorker(t)
	ownerID := uniqueUser(t)

	_, graphID := doCreate(t, ownerID, "github", "alice", "myrepo", "main")

	// Different user tries to access the graph
	otherID := "other-" + uuid.New().String()
	w := httptest.NewRecorder()
	HandleGetGraph(w, graphIDReq(http.MethodGet, otherID, graphID))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestGetGraph_NotFound(t *testing.T) {
	userID := "nf-" + uuid.New().String()
	w := httptest.NewRecorder()
	HandleGetGraph(w, graphIDReq(http.MethodGet, userID, "nonexistent-id"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDeleteGraph(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	overrideSpawnWorker(t)
	userID := uniqueUser(t)

	_, graphID := doCreate(t, userID, "github", "alice", "myrepo", "main")

	// DELETE
	dw := httptest.NewRecorder()
	HandleDeleteGraph(dw, graphIDReq(http.MethodDelete, userID, graphID))

	if dw.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", dw.Code)
	}

	// Subsequent GET should return 404
	gw := httptest.NewRecorder()
	HandleGetGraph(gw, graphIDReq(http.MethodGet, userID, graphID))

	if gw.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", gw.Code)
	}
}

func TestListGraphs_UserIsolation(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	overrideSpawnWorker(t)

	userA := uniqueUser(t)
	userB := uniqueUser(t)

	doCreate(t, userA, "github", "alice", "repo-a", "main")
	doCreate(t, userB, "github", "bob", "repo-b", "main")

	w := httptest.NewRecorder()
	HandleListGraphs(w, listGraphsReq(userA))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body struct {
		Graphs []graphSummary `json:"graphs"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Graphs) != 1 {
		t.Fatalf("expected 1 graph for userA, got %d", len(body.Graphs))
	}
	if body.Graphs[0].Owner != "alice" {
		t.Errorf("expected owner 'alice', got %q", body.Graphs[0].Owner)
	}
}

// ── write-handler test infrastructure ────────────────────────────────────────

// rewriteTransport redirects all outgoing requests to a single test server,
// preserving the original path. This lets tests intercept both GET calls from
// fetchGraphFileWithSHA and PUT calls from committer.CommitGraphFile.
type rewriteTransport struct{ base string }

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(rt.base, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

// overrideHTTPClient installs a rewriteTransport pointing at srvURL and
// restores the original client on test cleanup.
func overrideHTTPClient(t *testing.T, srvURL string) {
	t.Helper()
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: &rewriteTransport{base: srvURL}}
	t.Cleanup(func() { http.DefaultClient = old })
}

// registerReadyEntry stores a ready entry in graphsByID and schedules cleanup.
func registerReadyEntry(t *testing.T, userID, provider, owner, repo, branch string) string {
	t.Helper()
	id := uuid.New().String()
	entry := &GraphRegistryEntry{
		ID:       id,
		UserID:   userID,
		Provider: provider,
		Owner:    owner,
		Repo:     repo,
		Branch:   branch,
		Status:   "ready",
		CreatedAt: time.Now(),
		LastOpenedAt: time.Now(),
	}
	graphsByID.Store(id, entry)
	t.Cleanup(func() { graphsByID.Delete(id) })
	return id
}

// encodedGraph base64-encodes a GraphRecord as JSON, matching the GitHub
// contents-API "content" field format.
func encodedGraph(t *testing.T, rec graph.GraphRecord) string {
	t.Helper()
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("encodedGraph marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// githubGetResponse builds the JSON body returned by the GitHub contents GET.
func githubGetResponse(sha, content string) []byte {
	b, _ := json.Marshal(map[string]any{"sha": sha, "content": content})
	return b
}

// githubPutResponse builds the JSON body returned by the GitHub contents PUT.
func githubPutResponse(newSHA string) []byte {
	b, _ := json.Marshal(map[string]any{"content": map[string]string{"sha": newSHA}})
	return b
}

// nodeReq builds a request with chi URL params set for {id} and {nodeId}.
func nodeReq(method, userID, graphID, nodeID string, body any) *http.Request {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, "/graphs/"+graphID+"/nodes/"+nodeID, r)
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", graphID)
	rctx.URLParams.Add("nodeId", nodeID)
	ctx := context.WithValue(req.Context(), userIDKey, userID)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	return req.WithContext(ctx)
}

// noteReq builds a request with chi URL params set for {id} and {noteId}.
func noteReq(method, userID, graphID, noteID string, body any) *http.Request {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, "/graphs/"+graphID+"/notes/"+noteID, r)
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", graphID)
	rctx.URLParams.Add("noteId", noteID)
	ctx := context.WithValue(req.Context(), userIDKey, userID)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	return req.WithContext(ctx)
}

// layoutReq builds a PATCH /graphs/{id}/layout request.
func layoutReq(userID, graphID string, body any) *http.Request {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, "/graphs/"+graphID+"/layout", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", graphID)
	ctx := context.WithValue(req.Context(), userIDKey, userID)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	return req.WithContext(ctx)
}

// sampleWriteGraph returns a GraphRecord with three named nodes for write tests.
func sampleWriteGraph() graph.GraphRecord {
	return graph.GraphRecord{
		Version:   "1",
		RepoOwner: "alice",
		RepoName:  "myrepo",
		Branch:    "main",
		Nodes: []graph.NodeRecord{
			{UUID: "node-1", Name: "main.go", Type: "file"},
			{UUID: "node-2", Name: "helper.go", Type: "file"},
			{UUID: "node-3", Name: "util.go", Type: "file"},
		},
		Edges: []graph.EdgeRecord{},
	}
}

// ── handler tests ─────────────────────────────────────────────────────────────

func TestUpdateNodeDescription_UpdatesAndReturns200(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	userID := uniqueUser(t)
	rec := sampleWriteGraph()
	graphID := registerReadyEntry(t, userID, "github", "alice", "myrepo", "main")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			w.Write(githubGetResponse("sha-init", encodedGraph(t, rec)))
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
			w.Write(githubPutResponse("sha-updated"))
		}
	}))
	defer srv.Close()
	overrideHTTPClient(t, srv.URL)

	w := httptest.NewRecorder()
	HandleUpdateNodeDescription(w, nodeReq(http.MethodPatch, userID, graphID, "node-1",
		map[string]string{"description": "entry point"}))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["committedAt"] == "" {
		t.Error("expected non-empty committedAt")
	}
}

func TestAddNote_SetsNoteAndReturns201(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	userID := uniqueUser(t)
	rec := sampleWriteGraph()
	graphID := registerReadyEntry(t, userID, "github", "alice", "myrepo", "main")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			w.Write(githubGetResponse("sha-init", encodedGraph(t, rec)))
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
			w.Write(githubPutResponse("sha-note"))
		}
	}))
	defer srv.Close()
	overrideHTTPClient(t, srv.URL)

	w := httptest.NewRecorder()
	HandleAddNote(w, nodeReq(http.MethodPost, userID, graphID, "node-2",
		map[string]string{"content": "remember to refactor"}))

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["committedAt"] == "" {
		t.Error("expected non-empty committedAt")
	}
}

func TestUpdateNote_UpdatesNoteContentAndReturns200(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	userID := uniqueUser(t)
	rec := sampleWriteGraph()
	rec.Nodes[0].Note = "old note"
	graphID := registerReadyEntry(t, userID, "github", "alice", "myrepo", "main")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			w.Write(githubGetResponse("sha-init", encodedGraph(t, rec)))
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
			w.Write(githubPutResponse("sha-updated-note"))
		}
	}))
	defer srv.Close()
	overrideHTTPClient(t, srv.URL)

	w := httptest.NewRecorder()
	HandleUpdateNote(w, noteReq(http.MethodPatch, userID, graphID, "node-1",
		map[string]string{"content": "new note"}))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["committedAt"] == "" {
		t.Error("expected non-empty committedAt")
	}
}

func TestDeleteNote_ClearsNoteAndReturns200(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	userID := uniqueUser(t)
	rec := sampleWriteGraph()
	rec.Nodes[1].Note = "to be deleted"
	graphID := registerReadyEntry(t, userID, "github", "alice", "myrepo", "main")

	var putBody struct {
		Content string `json:"content"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			w.Write(githubGetResponse("sha-init", encodedGraph(t, rec)))
		case http.MethodPut:
			json.NewDecoder(r.Body).Decode(&putBody)
			w.WriteHeader(http.StatusOK)
			w.Write(githubPutResponse("sha-del"))
		}
	}))
	defer srv.Close()
	overrideHTTPClient(t, srv.URL)

	w := httptest.NewRecorder()
	HandleDeleteNote(w, noteReq(http.MethodDelete, userID, graphID, "node-2", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["committedAt"] == "" {
		t.Error("expected non-empty committedAt")
	}

	// Verify the committed content has an empty note for node-2.
	decoded, err := base64.StdEncoding.DecodeString(putBody.Content)
	if err != nil {
		t.Fatalf("decode committed content: %v", err)
	}
	var committed graph.GraphRecord
	json.Unmarshal(decoded, &committed)
	for _, n := range committed.Nodes {
		if n.UUID == "node-2" && n.Note != "" {
			t.Errorf("expected empty note for node-2, got %q", n.Note)
		}
	}
}

func TestUpdateLayout_CommitsAllThreePositionsInOneCall(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	userID := uniqueUser(t)
	rec := sampleWriteGraph()
	graphID := registerReadyEntry(t, userID, "github", "alice", "myrepo", "main")

	putCount := 0
	var capturedContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			w.Write(githubGetResponse("sha-init", encodedGraph(t, rec)))
		case http.MethodPut:
			putCount++
			var body struct{ Content string `json:"content"` }
			json.NewDecoder(r.Body).Decode(&body)
			capturedContent = body.Content
			w.WriteHeader(http.StatusOK)
			w.Write(githubPutResponse("sha-layout"))
		}
	}))
	defer srv.Close()
	overrideHTTPClient(t, srv.URL)

	positions := []map[string]any{
		{"nodeId": "node-1", "x": 10.0, "y": 20.0},
		{"nodeId": "node-2", "x": 30.0, "y": 40.0},
		{"nodeId": "node-3", "x": 50.0, "y": 60.0},
	}
	w := httptest.NewRecorder()
	HandleUpdateLayout(w, layoutReq(userID, graphID, positions))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if putCount != 1 {
		t.Errorf("expected exactly 1 PUT call, got %d", putCount)
	}

	decoded, err := base64.StdEncoding.DecodeString(capturedContent)
	if err != nil {
		t.Fatalf("decode committed content: %v", err)
	}
	var committed graph.GraphRecord
	json.Unmarshal(decoded, &committed)

	want := map[string]graph.Position{
		"node-1": {X: 10, Y: 20},
		"node-2": {X: 30, Y: 40},
		"node-3": {X: 50, Y: 60},
	}
	for _, n := range committed.Nodes {
		if pos, ok := want[n.UUID]; ok {
			if n.Pos != pos {
				t.Errorf("node %s: pos = %+v, want %+v", n.UUID, n.Pos, pos)
			}
		}
	}
}

func TestWriteHandlers_ForbiddenWhenUserIDMismatch(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	ownerID := uniqueUser(t)
	otherID := "other-" + uuid.New().String()
	graphID := registerReadyEntry(t, ownerID, "github", "alice", "myrepo", "main")

	cases := []struct {
		name    string
		handler http.HandlerFunc
		req     *http.Request
	}{
		{
			"updateNodeDescription",
			HandleUpdateNodeDescription,
			nodeReq(http.MethodPatch, otherID, graphID, "node-1", map[string]string{"description": "x"}),
		},
		{
			"addNote",
			HandleAddNote,
			nodeReq(http.MethodPost, otherID, graphID, "node-1", map[string]string{"content": "x"}),
		},
		{
			"updateNote",
			HandleUpdateNote,
			noteReq(http.MethodPatch, otherID, graphID, "node-1", map[string]string{"content": "x"}),
		},
		{
			"deleteNote",
			HandleDeleteNote,
			noteReq(http.MethodDelete, otherID, graphID, "node-1", nil),
		},
		{
			"updateLayout",
			HandleUpdateLayout,
			layoutReq(otherID, graphID, []map[string]any{{"nodeId": "node-1", "x": 1.0, "y": 2.0}}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.handler(w, tc.req)
			if w.Code != http.StatusForbidden {
				t.Errorf("expected 403, got %d", w.Code)
			}
		})
	}
}

func TestUpdateNodeDescription_ConflictRetryReturns200(t *testing.T) {
	// M-3.8: provider returns 422 on first PUT, then 200 on retry.
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	userID := uniqueUser(t)

	initial := sampleWriteGraph()
	// "latest" has an extra node; node-1 has a different description server-side.
	latest := sampleWriteGraph()
	latest.Nodes[0].Description = "server-desc"
	latest.Nodes = append(latest.Nodes, graph.NodeRecord{UUID: "node-4", Name: "new.go", Type: "file"})

	graphID := registerReadyEntry(t, userID, "github", "alice", "myrepo", "main")

	callSeq := 0
	var finalBody struct {
		Content string `json:"content"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callSeq++
		switch callSeq {
		case 1: // loadGraphForWrite GET → initial graph
			w.WriteHeader(http.StatusOK)
			w.Write(githubGetResponse("sha-init", encodedGraph(t, initial)))
		case 2: // CommitGraphFile first PUT → conflict
			w.WriteHeader(http.StatusUnprocessableEntity)
		case 3: // CommitGraphFile fetchLatest GET → latest graph
			w.WriteHeader(http.StatusOK)
			w.Write(githubGetResponse("sha-latest", encodedGraph(t, latest)))
		case 4: // CommitGraphFile retry PUT → success
			json.NewDecoder(r.Body).Decode(&finalBody)
			w.WriteHeader(http.StatusOK)
			w.Write(githubPutResponse("sha-final"))
		default:
			t.Errorf("unexpected call #%d %s %s", callSeq, r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	overrideHTTPClient(t, srv.URL)

	w := httptest.NewRecorder()
	HandleUpdateNodeDescription(w, nodeReq(http.MethodPatch, userID, graphID, "node-1",
		map[string]string{"description": "user desc"}))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if callSeq != 4 {
		t.Errorf("expected 4 provider calls (GET, PUT422, GET, PUT200), got %d", callSeq)
	}

	// Decode the merged record from the final PUT.
	decoded, err := base64.StdEncoding.DecodeString(finalBody.Content)
	if err != nil {
		t.Fatalf("decode final committed content: %v", err)
	}
	var committed graph.GraphRecord
	json.Unmarshal(decoded, &committed)

	// Merged structure comes from latest (4 nodes).
	if len(committed.Nodes) != 4 {
		t.Errorf("merged record: expected 4 nodes (from latest), got %d", len(committed.Nodes))
	}

	// node-1 description must be the user-authored value from pending.
	for _, n := range committed.Nodes {
		if n.UUID == "node-1" && n.Description != "user desc" {
			t.Errorf("node-1 Description: got %q, want %q", n.Description, "user desc")
		}
	}
}
