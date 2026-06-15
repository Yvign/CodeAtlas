package committer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"CodeAtlas/internal/core/graph"
)

func sampleRecord() graph.GraphRecord {
	return graph.GraphRecord{
		Version:     "1",
		GeneratedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		RepoOwner:   "acme",
		RepoName:    "myrepo",
		Branch:      "main",
		Nodes:       []graph.NodeRecord{{UUID: "u1", Name: "index.ts", Type: "file"}},
		Edges:       []graph.EdgeRecord{{Source: "a", Target: "b", Type: "dependency"}},
	}
}

func TestCommitGraphFile_GitHub_FirstCommit(t *testing.T) {
	record := sampleRecord()

	expectedJSON, _ := json.MarshalIndent(record, "", "  ")
	expectedContent := base64.StdEncoding.EncodeToString(expectedJSON)

	var capturedMethod, capturedPath string
	var capturedBody commitBody

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path

		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": map[string]any{"sha": "abc123sha"},
		})
	}))
	defer srv.Close()

	c := &Committer{Token: "tok", Provider: "github"}

	// Point the request at the test server by temporarily swapping the URL.
	// We do this by overriding contentURL via a local subtest helper that
	// replaces the host in the produced URL.
	origURL, _ := c.contentURL("acme", "myrepo")
	_ = origURL // used only to verify shape below

	// Re-route: build request manually via a wrapped client isn't possible
	// without refactoring, so we use a round-tripper override instead.
	transport := &rewriteTransport{base: srv.URL}
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transport}
	defer func() { http.DefaultClient = oldClient }()

	newSHA, err := c.CommitGraphFile(context.Background(), "acme", "myrepo", "main", record, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedMethod != http.MethodPut {
		t.Errorf("method: got %q, want PUT", capturedMethod)
	}
	if !strings.HasSuffix(capturedPath, "/repos/acme/myrepo/contents/.codeatlas/graph.json") {
		t.Errorf("path: got %q", capturedPath)
	}
	if capturedBody.Message != "codeatlas: regenerate graph" {
		t.Errorf("commit message: got %q", capturedBody.Message)
	}
	if capturedBody.Content != expectedContent {
		t.Errorf("content mismatch:\ngot  %s\nwant %s", capturedBody.Content, expectedContent)
	}
	if capturedBody.SHA != "" {
		t.Errorf("SHA should be empty for first commit, got %q", capturedBody.SHA)
	}
	if newSHA != "abc123sha" {
		t.Errorf("returned SHA: got %q, want %q", newSHA, "abc123sha")
	}
}

func TestCommitGraphFile_UnknownProvider(t *testing.T) {
	c := &Committer{Token: "tok", Provider: "bitbucket"}
	_, err := c.CommitGraphFile(context.Background(), "o", "r", "main", graph.GraphRecord{}, "")
	if err != ErrUnknownProvider {
		t.Errorf("expected ErrUnknownProvider, got %v", err)
	}
}

func TestCommitGraphFile_GitHub_NonCreatedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	transport := &rewriteTransport{base: srv.URL}
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transport}
	defer func() { http.DefaultClient = oldClient }()

	c := &Committer{Token: "tok", Provider: "github"}
	_, err := c.CommitGraphFile(context.Background(), "o", "r", "main", graph.GraphRecord{}, "")
	if err == nil {
		t.Fatal("expected error for non-201 status")
	}
	if !strings.Contains(err.Error(), "409") {
		t.Errorf("error should mention status code 409, got: %v", err)
	}
}

func TestCommitGraphFile_GitLab_FirstCommit(t *testing.T) {
	record := sampleRecord()
	expectedJSON, _ := json.MarshalIndent(record, "", "  ")
	expectedContent := base64.StdEncoding.EncodeToString(expectedJSON)

	var capturedURI string
	var capturedBody commitBody

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURI = r.RequestURI
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"blob_id": "gl-sha-999"})
	}))
	defer srv.Close()

	transport := &rewriteTransport{base: srv.URL}
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transport}
	defer func() { http.DefaultClient = oldClient }()

	c := &Committer{Token: "tok", Provider: "gitlab"}
	newSHA, err := c.CommitGraphFile(context.Background(), "acme", "myrepo", "main", record, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedURI, "acme%2Fmyrepo") {
		t.Errorf("request URI should contain URL-encoded owner/repo, got %q", capturedURI)
	}
	if capturedBody.Content != expectedContent {
		t.Errorf("content mismatch")
	}
	if capturedBody.SHA != "" {
		t.Errorf("SHA should be empty for first commit, got %q", capturedBody.SHA)
	}
	if newSHA != "gl-sha-999" {
		t.Errorf("returned SHA: got %q, want %q", newSHA, "gl-sha-999")
	}
}

func TestCommitGraphFile_GitHub_UpdateExistingFile(t *testing.T) {
	const existingSHA = "deadbeef"

	var capturedBody commitBody

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": map[string]any{"sha": "newsha456"},
		})
	}))
	defer srv.Close()

	transport := &rewriteTransport{base: srv.URL}
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transport}
	defer func() { http.DefaultClient = oldClient }()

	c := &Committer{Token: "tok", Provider: "github"}
	newSHA, err := c.CommitGraphFile(context.Background(), "acme", "myrepo", "main", sampleRecord(), existingSHA)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedBody.SHA != existingSHA {
		t.Errorf("request SHA: got %q, want %q", capturedBody.SHA, existingSHA)
	}
	if newSHA != "newsha456" {
		t.Errorf("returned SHA: got %q, want %q", newSHA, "newsha456")
	}
}

func TestCommitGraphFile_ConflictResolvedOnRetry(t *testing.T) {
	// pending: what we're trying to commit — has user-authored annotations on u1.
	pending := graph.GraphRecord{
		Version:     "2",
		GeneratedAt: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		RepoOwner:   "acme",
		RepoName:    "myrepo",
		Branch:      "main",
		Nodes: []graph.NodeRecord{
			{UUID: "u1", Name: "index.ts", Type: "file", Description: "entry point", Note: "important", Pos: graph.Position{X: 1, Y: 2}},
		},
		Edges: []graph.EdgeRecord{{Source: "u1", Target: "u2", Type: "dependency"}},
	}

	// latest: what's currently on the server — has an extra node u2 and stale annotations on u1.
	latest := graph.GraphRecord{
		Version:     "1",
		GeneratedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		RepoOwner:   "acme",
		RepoName:    "myrepo",
		Branch:      "main",
		Nodes: []graph.NodeRecord{
			{UUID: "u1", Name: "index.ts", Type: "file", Description: "old desc", Note: "old note", Pos: graph.Position{X: 10, Y: 20}},
			{UUID: "u2", Name: "utils.ts", Type: "file", SHA: "sha-u2", Path: "src/utils.ts"},
		},
		Edges: []graph.EdgeRecord{
			{Source: "u1", Target: "u2", Type: "dependency"},
			{Source: "u2", Target: "u3", Type: "dependency"},
		},
	}
	const latestSHA = "sha-from-server"
	const finalSHA = "sha-after-merge"

	latestJSON, _ := json.MarshalIndent(latest, "", "  ")
	latestEncoded := base64.StdEncoding.EncodeToString(latestJSON)

	callSeq := 0
	var capturedRetryBody commitBody

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callSeq++
		switch callSeq {
		case 1: // first PUT → conflict
			if r.Method != http.MethodPut {
				t.Errorf("call 1: want PUT, got %s", r.Method)
			}
			w.WriteHeader(http.StatusUnprocessableEntity)
		case 2: // GET → return latest
			if r.Method != http.MethodGet {
				t.Errorf("call 2: want GET, got %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sha":     latestSHA,
				"content": latestEncoded,
			})
		case 3: // second PUT → success
			if r.Method != http.MethodPut {
				t.Errorf("call 3: want PUT, got %s", r.Method)
			}
			_ = json.NewDecoder(r.Body).Decode(&capturedRetryBody)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": map[string]any{"sha": finalSHA},
			})
		default:
			t.Errorf("unexpected call #%d: %s %s", callSeq, r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	transport := &rewriteTransport{base: srv.URL}
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transport}
	defer func() { http.DefaultClient = oldClient }()

	c := &Committer{Token: "tok", Provider: "github"}
	newSHA, err := c.CommitGraphFile(context.Background(), "acme", "myrepo", "main", pending, "old-sha")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if newSHA != finalSHA {
		t.Errorf("returned SHA: got %q, want %q", newSHA, finalSHA)
	}
	if callSeq != 3 {
		t.Errorf("expected 3 calls (PUT, GET, PUT), got %d", callSeq)
	}

	// Retry PUT must carry the SHA fetched from the server.
	if capturedRetryBody.SHA != latestSHA {
		t.Errorf("retry PUT SHA: got %q, want %q", capturedRetryBody.SHA, latestSHA)
	}

	// Decode the committed content and verify the merge result.
	decoded, err := base64.StdEncoding.DecodeString(capturedRetryBody.Content)
	if err != nil {
		t.Fatalf("decode retry content: %v", err)
	}
	var committed graph.GraphRecord
	if err := json.Unmarshal(decoded, &committed); err != nil {
		t.Fatalf("unmarshal retry record: %v", err)
	}

	// Computed fields: nodes and edges come from latest.
	if len(committed.Nodes) != 2 {
		t.Errorf("merged nodes: got %d, want 2 (from latest)", len(committed.Nodes))
	}
	if len(committed.Edges) != 2 {
		t.Errorf("merged edges: got %d, want 2 (from latest)", len(committed.Edges))
	}

	// User-authored fields for u1: overwritten from pending.
	var u1 *graph.NodeRecord
	for i := range committed.Nodes {
		if committed.Nodes[i].UUID == "u1" {
			u1 = &committed.Nodes[i]
		}
	}
	if u1 == nil {
		t.Fatal("node u1 not found in merged record")
	}
	if u1.Description != "entry point" {
		t.Errorf("u1 Description: got %q, want %q", u1.Description, "entry point")
	}
	if u1.Note != "important" {
		t.Errorf("u1 Note: got %q, want %q", u1.Note, "important")
	}
	if u1.Pos != (graph.Position{X: 1, Y: 2}) {
		t.Errorf("u1 Pos: got %+v, want {1, 2}", u1.Pos)
	}

	// u2 exists only in latest (new file added between pending read and write).
	// Its computed fields must be preserved intact; user-authored fields must be zero.
	var u2 *graph.NodeRecord
	for i := range committed.Nodes {
		if committed.Nodes[i].UUID == "u2" {
			u2 = &committed.Nodes[i]
		}
	}
	if u2 == nil {
		t.Fatal("node u2 not found in merged record")
	}
	if u2.Name != "utils.ts" {
		t.Errorf("u2 Name: got %q, want %q", u2.Name, "utils.ts")
	}
	if u2.SHA != "sha-u2" {
		t.Errorf("u2 SHA: got %q, want %q", u2.SHA, "sha-u2")
	}
	if u2.Path != "src/utils.ts" {
		t.Errorf("u2 Path: got %q, want %q", u2.Path, "src/utils.ts")
	}
	if u2.Description != "" || u2.Note != "" || u2.Pos != (graph.Position{}) {
		t.Errorf("u2 user-authored fields should be zero, got Description=%q Note=%q Pos=%+v", u2.Description, u2.Note, u2.Pos)
	}

	// Metadata comes from pending.
	if committed.Version != "2" {
		t.Errorf("Version: got %q, want %q", committed.Version, "2")
	}
}

func TestCommitGraphFile_ConflictUnresolvable(t *testing.T) {
	latest := sampleRecord()
	latestJSON, _ := json.MarshalIndent(latest, "", "  ")
	latestEncoded := base64.StdEncoding.EncodeToString(latestJSON)

	putCount, getCount := 0, 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			putCount++
			w.WriteHeader(http.StatusUnprocessableEntity)
		case http.MethodGet:
			getCount++
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sha":     "some-sha",
				"content": latestEncoded,
			})
		}
	}))
	defer srv.Close()

	transport := &rewriteTransport{base: srv.URL}
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transport}
	defer func() { http.DefaultClient = oldClient }()

	c := &Committer{Token: "tok", Provider: "github"}
	_, err := c.CommitGraphFile(context.Background(), "acme", "myrepo", "main", sampleRecord(), "")
	if !errors.Is(err, ErrConflictUnresolvable) {
		t.Errorf("expected ErrConflictUnresolvable, got %v", err)
	}
	if putCount != 3 {
		t.Errorf("expected 3 PUT attempts, got %d", putCount)
	}
	if getCount != 2 {
		t.Errorf("expected 2 GET fetches (not after last failure), got %d", getCount)
	}
}

// rewriteTransport redirects all outgoing requests to a test server.
type rewriteTransport struct {
	base string
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(rt.base, "http://")
	return http.DefaultTransport.RoundTrip(req)
}
