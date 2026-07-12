package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"CodeAtlas/internal/core/graph"
	"CodeAtlas/internal/db"
	"CodeAtlas/internal/worker"
)

// ── test helpers ──────────────────────────────────────────────────────────────

// newTestGraphsHandler creates a GraphsHandler with fresh mock repos.
func newTestGraphsHandler() (*GraphsHandler, *db.MockGraphRepository, *db.MockTokenRepository) {
	mockGraphs := db.NewMockGraphRepository()
	mockTokens := db.NewMockTokenRepository()
	return &GraphsHandler{Graphs: mockGraphs, Tokens: mockTokens}, mockGraphs, mockTokens
}

func graphJSONBody(provider, owner, repo, branch string) *bytes.Reader {
	b, _ := json.Marshal(createGraphRequest{
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

// captureSpawnWorker replaces spawnWorker with a function that records the
// worker passed to it (without running it) and returns a getter for it.
func captureSpawnWorker(t *testing.T) func() *worker.Worker {
	t.Helper()
	var captured *worker.Worker
	orig := spawnWorker
	spawnWorker = func(w *worker.Worker) { captured = w }
	t.Cleanup(func() { spawnWorker = orig })
	return func() *worker.Worker { return captured }
}

// postGraphReqWithCommit builds a create-graph POST request with an explicit commit flag.
func postGraphReqWithCommit(userID, provider, owner, repo, branch string, commit bool) *http.Request {
	b, _ := json.Marshal(createGraphRequest{
		Provider: provider, Owner: owner, Repo: repo, Branch: branch, Commit: &commit,
	})
	req := httptest.NewRequest(http.MethodPost, "/graphs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), userIDKey, userID)
	return req.WithContext(ctx)
}

// uniqueUser creates a unique userID and stores an encrypted github token in tokens.
func uniqueUser(t *testing.T, tokens *db.MockTokenRepository) string {
	t.Helper()
	id := "test-" + uuid.New().String()
	storeGithubToken(t, tokens, id, "gh-access-token")
	return id
}

// doCreate posts a create-graph request and returns the decoded body.
func doCreate(t *testing.T, h *GraphsHandler, userID, provider, owner, repo, branch string) (code int, graphID string) {
	t.Helper()
	w := httptest.NewRecorder()
	h.HandleCreateGraph(w, postGraphReq(userID, provider, owner, repo, branch))
	var body struct {
		GraphID string `json:"graphId"`
	}
	json.NewDecoder(w.Body).Decode(&body)
	return w.Code, body.GraphID
}

// registerReadyEntry stores a "ready" graph entry in the mock repo and returns its ID.
func registerReadyEntry(t *testing.T, graphs *db.MockGraphRepository, userID, provider, owner, repo, branch string) string {
	t.Helper()
	rec, err := graphs.FindOrCreate(context.Background(), db.GraphRecord{
		UserID:   userID,
		Provider: provider,
		Owner:    owner,
		RepoName: repo,
		Branch:   branch,
	})
	if err != nil {
		t.Fatalf("registerReadyEntry: %v", err)
	}
	graphs.UpdateStatus(context.Background(), rec.ID, "ready", "")
	return rec.ID
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestCreateGraph_Valid(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	overrideSpawnWorker(t)
	h, mockGraphs, mockTokens := newTestGraphsHandler()
	userID := uniqueUser(t, mockTokens)

	code, graphID := doCreate(t, h, userID, "github", "alice", "myrepo", "main")

	if code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", code)
	}
	if graphID == "" {
		t.Fatal("expected non-empty graphId")
	}

	rec, _ := mockGraphs.FindByID(context.Background(), graphID)
	if rec == nil {
		t.Errorf("mock graph repo missing entry for %s", graphID)
	}
}

func TestCreateGraph_Dedup(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	overrideSpawnWorker(t)
	h, _, mockTokens := newTestGraphsHandler()
	userID := uniqueUser(t, mockTokens)

	code1, id1 := doCreate(t, h, userID, "github", "alice", "myrepo", "main")
	code2, id2 := doCreate(t, h, userID, "github", "alice", "myrepo", "main")

	if code1 != http.StatusAccepted || code2 != http.StatusAccepted {
		t.Fatalf("expected 202/202, got %d/%d", code1, code2)
	}
	if id1 != id2 {
		t.Errorf("dedup failed: first=%s second=%s", id1, id2)
	}
}

func TestCreateGraph_CommitOmitted_DefaultsToTrue(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	getWorker := captureSpawnWorker(t)
	h, _, mockTokens := newTestGraphsHandler()
	userID := uniqueUser(t, mockTokens)

	doCreate(t, h, userID, "github", "alice", "myrepo", "main")

	wk := getWorker()
	if wk == nil {
		t.Fatal("expected spawnWorker to be called")
	}
	if !wk.CommitEnabled {
		t.Error("expected CommitEnabled to default to true when commit is omitted")
	}
}

func TestCreateGraph_CommitFalse_DisablesCommit(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	getWorker := captureSpawnWorker(t)
	h, _, mockTokens := newTestGraphsHandler()
	userID := uniqueUser(t, mockTokens)

	w := httptest.NewRecorder()
	h.HandleCreateGraph(w, postGraphReqWithCommit(userID, "github", "alice", "myrepo", "main", false))

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Code)
	}

	wk := getWorker()
	if wk == nil {
		t.Fatal("expected spawnWorker to be called")
	}
	if wk.CommitEnabled {
		t.Error("expected CommitEnabled to be false when commit:false is sent")
	}
}

func TestGetGraph_ServesFromMemoryWhenCommitDisabled(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	h, mockGraphs, mockTokens := newTestGraphsHandler()
	userID := uniqueUser(t, mockTokens)
	graphID := registerReadyEntry(t, mockGraphs, userID, "github", "alice", "myrepo", "main")

	rec := sampleWriteGraph()
	inMemoryGraphs.Store(graphID, rec)
	t.Cleanup(func() { inMemoryGraphs.Delete(graphID) })

	// No provider mock server is set up: if the handler tried to fetch from
	// the provider instead of serving the in-memory record, this would fail.
	w := httptest.NewRecorder()
	h.HandleGetGraph(w, graphIDReq(http.MethodGet, userID, graphID))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Status string            `json:"status"`
		Graph  graph.GraphRecord `json:"graph"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ready" {
		t.Errorf("status: got %q, want %q", body.Status, "ready")
	}
	if len(body.Graph.Nodes) != len(rec.Nodes) {
		t.Errorf("expected %d nodes from in-memory record, got %d", len(rec.Nodes), len(body.Graph.Nodes))
	}
}

func TestGetGraph_Processing(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	overrideSpawnWorker(t)
	h, _, mockTokens := newTestGraphsHandler()
	userID := uniqueUser(t, mockTokens)

	_, graphID := doCreate(t, h, userID, "github", "alice", "myrepo", "main")

	w := httptest.NewRecorder()
	h.HandleGetGraph(w, graphIDReq(http.MethodGet, userID, graphID))

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
	h, _, mockTokens := newTestGraphsHandler()
	ownerID := uniqueUser(t, mockTokens)

	_, graphID := doCreate(t, h, ownerID, "github", "alice", "myrepo", "main")

	otherID := "other-" + uuid.New().String()
	w := httptest.NewRecorder()
	h.HandleGetGraph(w, graphIDReq(http.MethodGet, otherID, graphID))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestGetGraph_NotFound(t *testing.T) {
	h, _, _ := newTestGraphsHandler()
	userID := "nf-" + uuid.New().String()
	w := httptest.NewRecorder()
	h.HandleGetGraph(w, graphIDReq(http.MethodGet, userID, "nonexistent-id"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDeleteGraph(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	overrideSpawnWorker(t)
	h, _, mockTokens := newTestGraphsHandler()
	userID := uniqueUser(t, mockTokens)

	_, graphID := doCreate(t, h, userID, "github", "alice", "myrepo", "main")

	// DELETE
	dw := httptest.NewRecorder()
	h.HandleDeleteGraph(dw, graphIDReq(http.MethodDelete, userID, graphID))

	if dw.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", dw.Code)
	}

	// Subsequent GET should return 404
	gw := httptest.NewRecorder()
	h.HandleGetGraph(gw, graphIDReq(http.MethodGet, userID, graphID))

	if gw.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", gw.Code)
	}
}

func TestListGraphs_UserIsolation(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	overrideSpawnWorker(t)
	h, _, mockTokens := newTestGraphsHandler()

	userA := uniqueUser(t, mockTokens)
	userB := uniqueUser(t, mockTokens)

	doCreate(t, h, userA, "github", "alice", "repo-a", "main")
	doCreate(t, h, userB, "github", "bob", "repo-b", "main")

	w := httptest.NewRecorder()
	h.HandleListGraphs(w, listGraphsReq(userA))

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

// descriptionsReq builds a PATCH /graphs/{id}/nodes request (batched description updates).
func descriptionsReq(userID, graphID string, body any) *http.Request {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, "/graphs/"+graphID+"/nodes", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", graphID)
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

func TestCommitGraph_CommitsInMemoryRecordAndReturns200(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	h, mockGraphs, mockTokens := newTestGraphsHandler()
	userID := uniqueUser(t, mockTokens)
	rec := sampleWriteGraph()
	graphID := registerReadyEntry(t, mockGraphs, userID, "github", "alice", "myrepo", "main")

	inMemoryGraphs.Store(graphID, rec)
	t.Cleanup(func() { inMemoryGraphs.Delete(graphID) })

	var capturedMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		w.WriteHeader(http.StatusCreated)
		w.Write(githubPutResponse("sha-first-commit"))
	}))
	defer srv.Close()
	overrideHTTPClient(t, srv.URL)

	w := httptest.NewRecorder()
	h.HandleCommitGraph(w, graphIDReq(http.MethodPost, userID, graphID))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedMethod != http.MethodPut {
		t.Errorf("expected a PUT to create the file, got %s", capturedMethod)
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["committedAt"] == "" {
		t.Error("expected non-empty committedAt")
	}
	if _, stillPending := inMemoryGraphs.Load(graphID); stillPending {
		t.Error("expected in-memory record to be cleared after commit")
	}
}

func TestCommitGraph_NoPendingData_Returns409(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	h, mockGraphs, mockTokens := newTestGraphsHandler()
	userID := uniqueUser(t, mockTokens)
	graphID := registerReadyEntry(t, mockGraphs, userID, "github", "alice", "myrepo", "main")

	w := httptest.NewRecorder()
	h.HandleCommitGraph(w, graphIDReq(http.MethodPost, userID, graphID))

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateNodeDescriptions_CommitsAllInOneCall(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	h, mockGraphs, mockTokens := newTestGraphsHandler()
	userID := uniqueUser(t, mockTokens)
	rec := sampleWriteGraph()
	graphID := registerReadyEntry(t, mockGraphs, userID, "github", "alice", "myrepo", "main")

	putCount := 0
	var capturedContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			w.Write(githubGetResponse("sha-init", encodedGraph(t, rec)))
		case http.MethodPut:
			putCount++
			var body struct {
				Content string `json:"content"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			capturedContent = body.Content
			w.WriteHeader(http.StatusOK)
			w.Write(githubPutResponse("sha-updated"))
		}
	}))
	defer srv.Close()
	overrideHTTPClient(t, srv.URL)

	updates := []map[string]string{
		{"nodeId": "node-1", "description": "entry point"},
		{"nodeId": "node-2", "description": "helper functions"},
	}
	w := httptest.NewRecorder()
	h.HandleUpdateNodeDescriptions(w, descriptionsReq(userID, graphID, updates))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if putCount != 1 {
		t.Errorf("expected exactly 1 PUT call, got %d", putCount)
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["committedAt"] == "" {
		t.Error("expected non-empty committedAt")
	}

	decoded, err := base64.StdEncoding.DecodeString(capturedContent)
	if err != nil {
		t.Fatalf("decode committed content: %v", err)
	}
	var committed graph.GraphRecord
	json.Unmarshal(decoded, &committed)

	want := map[string]string{"node-1": "entry point", "node-2": "helper functions"}
	for _, n := range committed.Nodes {
		if desc, ok := want[n.UUID]; ok && n.Description != desc {
			t.Errorf("node %s: description = %q, want %q", n.UUID, n.Description, desc)
		}
	}
}

func TestUpdateLayout_CommitsAllThreePositionsInOneCall(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	h, mockGraphs, mockTokens := newTestGraphsHandler()
	userID := uniqueUser(t, mockTokens)
	rec := sampleWriteGraph()
	graphID := registerReadyEntry(t, mockGraphs, userID, "github", "alice", "myrepo", "main")

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
	h.HandleUpdateLayout(w, layoutReq(userID, graphID, positions))

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
	h, mockGraphs, mockTokens := newTestGraphsHandler()
	ownerID := uniqueUser(t, mockTokens)
	otherID := "other-" + uuid.New().String()
	graphID := registerReadyEntry(t, mockGraphs, ownerID, "github", "alice", "myrepo", "main")

	cases := []struct {
		name    string
		handler http.HandlerFunc
		req     *http.Request
	}{
		{
			"updateNodeDescriptions",
			h.HandleUpdateNodeDescriptions,
			descriptionsReq(otherID, graphID, []map[string]string{{"nodeId": "node-1", "description": "x"}}),
		},
		{
			"updateLayout",
			h.HandleUpdateLayout,
			layoutReq(otherID, graphID, []map[string]any{{"nodeId": "node-1", "x": 1.0, "y": 2.0}}),
		},
		{
			"commitGraph",
			h.HandleCommitGraph,
			graphIDReq(http.MethodPost, otherID, graphID),
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

func TestUpdateNodeDescriptions_ConflictRetryReturns200(t *testing.T) {
	t.Setenv("CODEATLAS_ENCRYPTION_KEY", testEncKey)
	h, mockGraphs, mockTokens := newTestGraphsHandler()
	userID := uniqueUser(t, mockTokens)

	initial := sampleWriteGraph()
	latest := sampleWriteGraph()
	latest.Nodes[0].Description = "server-desc"
	latest.Nodes = append(latest.Nodes, graph.NodeRecord{UUID: "node-4", Name: "new.go", Type: "file"})

	graphID := registerReadyEntry(t, mockGraphs, userID, "github", "alice", "myrepo", "main")

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
	h.HandleUpdateNodeDescriptions(w, descriptionsReq(userID, graphID,
		[]map[string]string{{"nodeId": "node-1", "description": "user desc"}}))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if callSeq != 4 {
		t.Errorf("expected 4 provider calls (GET, PUT422, GET, PUT200), got %d", callSeq)
	}

	decoded, err := base64.StdEncoding.DecodeString(finalBody.Content)
	if err != nil {
		t.Fatalf("decode final committed content: %v", err)
	}
	var committed graph.GraphRecord
	json.Unmarshal(decoded, &committed)

	if len(committed.Nodes) != 4 {
		t.Errorf("merged record: expected 4 nodes (from latest), got %d", len(committed.Nodes))
	}

	for _, n := range committed.Nodes {
		if n.UUID == "node-1" && n.Description != "user desc" {
			t.Errorf("node-1 Description: got %q, want %q", n.Description, "user desc")
		}
	}
}
