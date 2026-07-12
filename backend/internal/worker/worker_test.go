package worker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"CodeAtlas/internal/core/graph"
)

func TestWorker_Run_HappyPath(t *testing.T) {
	// 3-file repo: one directory entry and two TypeScript blobs with an import
	// between them, giving us 3 nodes and 3 edges (1 dependency + 2 contains).
	treeBody := map[string]any{
		"sha": "root-tree-sha",
		"tree": []map[string]any{
			{"path": "src", "type": "tree", "sha": "sha-src", "mode": "040000"},
			{"path": "src/app.ts", "type": "blob", "sha": "sha-app", "mode": "100644"},
			{"path": "src/utils.ts", "type": "blob", "sha": "sha-utils", "mode": "100644"},
		},
		"truncated": false,
	}

	blobResponse := func(content string) map[string]string {
		return map[string]string{
			"content": base64.StdEncoding.EncodeToString([]byte(content)),
		}
	}

	var capturedPutBody struct {
		Content string `json:"content"`
		Message string `json:"message"`
		SHA     string `json:"sha"`
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/repos/acme/myrepo/git/trees/main", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(treeBody)
	})
	mux.HandleFunc("/repos/acme/myrepo/git/blobs/sha-app", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(blobResponse("import utils from './utils'"))
	})
	mux.HandleFunc("/repos/acme/myrepo/git/blobs/sha-utils", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(blobResponse("export const utils = {}"))
	})
	mux.HandleFunc("/repos/acme/myrepo/contents/.codeatlas/graph.json", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// No existing graph file yet.
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			_ = json.NewDecoder(r.Body).Decode(&capturedPutBody)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": map[string]any{"sha": "new-blob-sha"},
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Replace http.DefaultTransport so both the fetcher's custom http.Client
	// (Transport: nil → falls back to DefaultTransport) and the committer's
	// http.DefaultClient are redirected to the test server.
	realTransport := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{base: srv.URL, wrapped: realTransport}
	defer func() { http.DefaultTransport = realTransport }()

	var completedID, completedStatus, completedErrMsg string
	wkr := &Worker{
		Owner:         "acme",
		Repo:          "myrepo",
		Branch:        "main",
		Provider:      "github",
		Token:         "test-token",
		GraphID:       "graph-123",
		CommitEnabled: true,
		OnComplete: func(id, status, errMsg string, record *graph.GraphRecord) {
			completedID = id
			completedStatus = status
			completedErrMsg = errMsg
		},
	}

	if err := wkr.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// OnComplete must be called with "ready".
	if completedID != "graph-123" {
		t.Errorf("OnComplete graphID: got %q, want %q", completedID, "graph-123")
	}
	if completedStatus != "ready" {
		t.Errorf("OnComplete status: got %q, want %q", completedStatus, "ready")
	}
	if completedErrMsg != "" {
		t.Errorf("OnComplete errMsg: got %q, want empty", completedErrMsg)
	}

	// Decode the committed graph.json content and verify the payload.
	raw, err := base64.StdEncoding.DecodeString(capturedPutBody.Content)
	if err != nil {
		t.Fatalf("decode committed content: %v", err)
	}
	var committed graph.GraphRecord
	if err := json.Unmarshal(raw, &committed); err != nil {
		t.Fatalf("unmarshal committed record: %v", err)
	}

	// 3 nodes: src (tree), app.ts, utils.ts.
	if len(committed.Nodes) != 3 {
		t.Errorf("node count: got %d, want 3", len(committed.Nodes))
	}

	// Every committed node must carry its full file path.
	uuidByPath := make(map[string]string, len(committed.Nodes))
	for _, n := range committed.Nodes {
		if n.Path == "" {
			t.Errorf("node %q has empty Path", n.Name)
		}
		uuidByPath[n.Path] = n.UUID
	}

	// 3 edges: 1 dependency (app.ts→utils.ts) + 2 contains (src→app.ts,
	// src→utils.ts) — all using node UUIDs, not paths.
	type edgeKey struct{ src, tgt, typ string }
	edgeSet := make(map[edgeKey]bool, len(committed.Edges))
	for _, e := range committed.Edges {
		edgeSet[edgeKey{e.Source, e.Target, e.Type}] = true
	}
	wantEdges := []edgeKey{
		{uuidByPath["src/app.ts"], uuidByPath["src/utils.ts"], graph.EdgeTypeDependency},
		{uuidByPath["src"], uuidByPath["src/app.ts"], graph.EdgeTypeContains},
		{uuidByPath["src"], uuidByPath["src/utils.ts"], graph.EdgeTypeContains},
	}
	for _, we := range wantEdges {
		if !edgeSet[we] {
			t.Errorf("missing edge %s → %s (%s)", we.src, we.tgt, we.typ)
		}
	}
	if len(committed.Edges) != len(wantEdges) {
		t.Errorf("edge count: got %d, want %d", len(committed.Edges), len(wantEdges))
	}

	// Metadata set from Worker fields.
	if committed.Version != "1" {
		t.Errorf("Version: got %q, want %q", committed.Version, "1")
	}
	if committed.RepoOwner != "acme" {
		t.Errorf("RepoOwner: got %q, want %q", committed.RepoOwner, "acme")
	}
	if committed.RepoName != "myrepo" {
		t.Errorf("RepoName: got %q, want %q", committed.RepoName, "myrepo")
	}
	if committed.Branch != "main" {
		t.Errorf("Branch: got %q, want %q", committed.Branch, "main")
	}

	// First commit: SHA must be absent (omitempty on empty string).
	if capturedPutBody.SHA != "" {
		t.Errorf("PUT SHA: got %q, want empty (first commit)", capturedPutBody.SHA)
	}
}

func TestWorker_Run_CommitDisabled_SkipsCommitAndReturnsRecord(t *testing.T) {
	treeBody := map[string]any{
		"sha": "root-tree-sha",
		"tree": []map[string]any{
			{"path": "src", "type": "tree", "sha": "sha-src", "mode": "040000"},
			{"path": "src/app.ts", "type": "blob", "sha": "sha-app", "mode": "100644"},
		},
		"truncated": false,
	}
	blobResponse := func(content string) map[string]string {
		return map[string]string{"content": base64.StdEncoding.EncodeToString([]byte(content))}
	}

	putCalled := false
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/myrepo/git/trees/main", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(treeBody)
	})
	mux.HandleFunc("/repos/acme/myrepo/git/blobs/sha-app", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(blobResponse("export const app = {}"))
	})
	mux.HandleFunc("/repos/acme/myrepo/contents/.codeatlas/graph.json", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// No existing graph file yet.
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			putCalled = true
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	realTransport := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{base: srv.URL, wrapped: realTransport}
	defer func() { http.DefaultTransport = realTransport }()

	var completedStatus, completedErrMsg string
	var completedRecord *graph.GraphRecord
	wkr := &Worker{
		Owner:         "acme",
		Repo:          "myrepo",
		Branch:        "main",
		Provider:      "github",
		Token:         "test-token",
		GraphID:       "graph-view-only",
		CommitEnabled: false,
		OnComplete: func(_, status, errMsg string, record *graph.GraphRecord) {
			completedStatus = status
			completedErrMsg = errMsg
			completedRecord = record
		},
	}

	if err := wkr.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if putCalled {
		t.Error("expected no PUT call when CommitEnabled is false")
	}
	if completedStatus != "ready" {
		t.Errorf("OnComplete status: got %q, want %q", completedStatus, "ready")
	}
	if completedErrMsg != "" {
		t.Errorf("OnComplete errMsg: got %q, want empty", completedErrMsg)
	}
	if completedRecord == nil {
		t.Fatal("expected non-nil GraphRecord when CommitEnabled is false")
	}
	if len(completedRecord.Nodes) != 2 {
		t.Errorf("record node count: got %d, want 2", len(completedRecord.Nodes))
	}
}

func TestWorker_Run_AnnotationsPreserved(t *testing.T) {
	// Existing stored graph has app.ts annotated. The SHA matches what the
	// mock tree will return, so graph.Merge must pick it up by SHA and carry
	// the Description and UUID through into the committed record.
	existingRecord := graph.GraphRecord{
		Version:   "1",
		RepoOwner: "acme",
		RepoName:  "myrepo",
		Branch:    "main",
		Nodes: []graph.NodeRecord{
			{
				UUID:        "existing-uuid-1",
				Type:        "file",
				Name:        "app.ts",
				SHA:         "sha-app",
				Description: "entry point",
			},
		},
		Edges: []graph.EdgeRecord{},
	}
	existingJSON, _ := json.MarshalIndent(existingRecord, "", "  ")
	existingEncoded := base64.StdEncoding.EncodeToString(existingJSON)

	treeBody := map[string]any{
		"sha": "root-tree-sha",
		"tree": []map[string]any{
			{"path": "src", "type": "tree", "sha": "sha-src", "mode": "040000"},
			{"path": "src/app.ts", "type": "blob", "sha": "sha-app", "mode": "100644"},
			{"path": "src/utils.ts", "type": "blob", "sha": "sha-utils", "mode": "100644"},
		},
		"truncated": false,
	}
	blobResponse := func(content string) map[string]string {
		return map[string]string{"content": base64.StdEncoding.EncodeToString([]byte(content))}
	}

	var capturedPutBody struct {
		Content string `json:"content"`
		SHA     string `json:"sha"`
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/myrepo/git/trees/main", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(treeBody)
	})
	mux.HandleFunc("/repos/acme/myrepo/git/blobs/sha-app", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(blobResponse("import utils from './utils'"))
	})
	mux.HandleFunc("/repos/acme/myrepo/git/blobs/sha-utils", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(blobResponse("export const utils = {}"))
	})
	mux.HandleFunc("/repos/acme/myrepo/contents/.codeatlas/graph.json", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sha":     "existing-blob-sha",
				"content": existingEncoded,
			})
		case http.MethodPut:
			_ = json.NewDecoder(r.Body).Decode(&capturedPutBody)
			w.WriteHeader(http.StatusOK) // 200 for update
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": map[string]any{"sha": "updated-blob-sha"},
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	realTransport := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{base: srv.URL, wrapped: realTransport}
	defer func() { http.DefaultTransport = realTransport }()

	var completedStatus string
	wkr := &Worker{
		Owner:         "acme",
		Repo:          "myrepo",
		Branch:        "main",
		Provider:      "github",
		Token:         "test-token",
		GraphID:       "graph-456",
		CommitEnabled: true,
		OnComplete: func(_, status, _ string, _ *graph.GraphRecord) {
			completedStatus = status
		},
	}

	if err := wkr.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if completedStatus != "ready" {
		t.Errorf("OnComplete status: got %q, want %q", completedStatus, "ready")
	}

	// The PUT must carry the blob SHA returned by the GET.
	if capturedPutBody.SHA != "existing-blob-sha" {
		t.Errorf("PUT SHA: got %q, want %q", capturedPutBody.SHA, "existing-blob-sha")
	}

	raw, err := base64.StdEncoding.DecodeString(capturedPutBody.Content)
	if err != nil {
		t.Fatalf("decode committed content: %v", err)
	}
	var committed graph.GraphRecord
	if err := json.Unmarshal(raw, &committed); err != nil {
		t.Fatalf("unmarshal committed record: %v", err)
	}

	// Find app.ts by SHA — Merge should have matched it from the existing record.
	var appNode *graph.NodeRecord
	for i := range committed.Nodes {
		if committed.Nodes[i].SHA == "sha-app" {
			appNode = &committed.Nodes[i]
			break
		}
	}
	if appNode == nil {
		t.Fatal("app.ts node (sha: sha-app) not found in committed record")
	}
	if appNode.UUID != "existing-uuid-1" {
		t.Errorf("UUID: got %q, want %q", appNode.UUID, "existing-uuid-1")
	}
	if appNode.Description != "entry point" {
		t.Errorf("Description: got %q, want %q", appNode.Description, "entry point")
	}
}

func TestWorker_FetchFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the tree endpoint is reached; return 500 to trigger a fetch error.
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	realTransport := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{base: srv.URL, wrapped: realTransport}
	defer func() { http.DefaultTransport = realTransport }()

	callCount := 0
	var completedStatus, completedErrMsg string

	wkr := &Worker{
		Owner:         "acme",
		Repo:          "myrepo",
		Branch:        "main",
		Provider:      "github",
		Token:         "test-token",
		GraphID:       "graph-789",
		CommitEnabled: true,
		OnComplete: func(_, status, errMsg string, _ *graph.GraphRecord) {
			callCount++
			completedStatus = status
			completedErrMsg = errMsg
		},
	}

	err := wkr.Run(context.Background())

	if err == nil {
		t.Error("Run: expected non-nil error, got nil")
	}
	if callCount != 1 {
		t.Errorf("OnComplete call count: got %d, want 1", callCount)
	}
	if completedStatus != "failed" {
		t.Errorf("OnComplete status: got %q, want %q", completedStatus, "failed")
	}
	if completedErrMsg == "" {
		t.Error("OnComplete errMsg: want non-empty, got empty string")
	}
}

// rewriteTransport redirects all outgoing requests to a test server while
// using the original transport for the actual connection.
type rewriteTransport struct {
	base    string
	wrapped http.RoundTripper
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(rt.base, "http://")
	return rt.wrapped.RoundTrip(req)
}
