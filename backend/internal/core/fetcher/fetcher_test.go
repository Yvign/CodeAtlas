package fetcher

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func makeTreeHandler(tree ResponseTree) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tree)
	}
}

func makeBlobHandler(content string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		encoded := base64.StdEncoding.EncodeToString([]byte(content))
		json.NewEncoder(w).Encode(map[string]string{"content": encoded})
	}
}

func TestFetchFileTree_Basic(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/git/trees/main", makeTreeHandler(ResponseTree{
		SHA: "abc",
		Tree: []SubTree{
			{Path: "main.ts", Type: "blob", SHA: "sha1"},
			{Path: "index.js", Type: "blob", SHA: "sha2"},
		},
		Truncated: false,
	}))
	mux.HandleFunc("/repos/owner/repo/git/blobs/sha1", makeBlobHandler("const x = 1"))
	mux.HandleFunc("/repos/owner/repo/git/blobs/sha2", makeBlobHandler("module.exports = {}"))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	files, errs, err := FetchfileTree(&GithubProvider{BaseUrl: srv.URL}, "owner", "repo", "main", "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("unexpected file errors: %v", errs)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}

	byPath := make(map[string]FileData)
	for _, f := range files {
		byPath[f.Path] = f
	}
	if byPath["main.ts"].RawFileData != "const x = 1" {
		t.Errorf("unexpected content for main.ts: %q", byPath["main.ts"].RawFileData)
	}
	if byPath["index.js"].RawFileData != "module.exports = {}" {
		t.Errorf("unexpected content for index.js: %q", byPath["index.js"].RawFileData)
	}
}

func TestFetchFileTree_FilteredPaths(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/git/trees/main", makeTreeHandler(ResponseTree{
		Tree: []SubTree{
			{Path: "node_modules/lodash/index.js", Type: "blob", SHA: "sha1"},
			{Path: "dist/bundle.js", Type: "blob", SHA: "sha2"},
			{Path: "src/app.ts", Type: "blob", SHA: "sha3"},
		},
	}))
	mux.HandleFunc("/repos/owner/repo/git/blobs/sha3", makeBlobHandler("export {}"))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	files, _, err := FetchfileTree(&GithubProvider{BaseUrl: srv.URL}, "owner", "repo", "main", "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file after filtering, got %d", len(files))
	}
	if files[0].Path != "src/app.ts" {
		t.Errorf("expected src/app.ts, got %q", files[0].Path)
	}
}

func TestFetchFileTree_ExtensionFilter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/git/trees/main", makeTreeHandler(ResponseTree{
		Tree: []SubTree{
			{Path: "index.ts", Type: "blob", SHA: "sha1"},
			{Path: "config.json", Type: "blob", SHA: "sha2"},
			{Path: "style.css", Type: "blob", SHA: "sha3"},
			{Path: "README.md", Type: "blob", SHA: "sha4"},
		},
	}))
	mux.HandleFunc("/repos/owner/repo/git/blobs/sha1", makeBlobHandler("export const x = 1"))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	files, _, err := FetchfileTree(&GithubProvider{BaseUrl: srv.URL}, "owner", "repo", "main", "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file (only .ts), got %d", len(files))
	}
	if files[0].Path != "index.ts" {
		t.Errorf("expected index.ts, got %q", files[0].Path)
	}
}

func TestFetchFileTree_Truncated(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/git/trees/main", func(w http.ResponseWriter, r *http.Request) {
		calls++
		recursive := r.URL.Query().Get("recursive")
		if recursive == "1" {
			// first call: return truncated
			json.NewEncoder(w).Encode(ResponseTree{Truncated: true})
		} else {
			// second call: non-recursive, return a tree item and a blob
			json.NewEncoder(w).Encode(ResponseTree{
				Tree: []SubTree{
					{Path: "main.ts", Type: "blob", SHA: "sha1"},
				},
			})
		}
	})
	mux.HandleFunc("/repos/owner/repo/git/blobs/sha1", makeBlobHandler("const x = 1"))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	files, _, err := FetchfileTree(&GithubProvider{BaseUrl: srv.URL}, "owner", "repo", "main", "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if calls != 2 {
		t.Errorf("expected 2 tree calls (recursive + non-recursive), got %d", calls)
	}
}

func TestFetchFileTree_BlobFetchError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/git/trees/main", makeTreeHandler(ResponseTree{
		Tree: []SubTree{
			{Path: "broken.ts", Type: "blob", SHA: "sha1"},
		},
	}))
	mux.HandleFunc("/repos/owner/repo/git/blobs/sha1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, fileErrs, err := FetchfileTree(&GithubProvider{BaseUrl: srv.URL}, "owner", "repo", "main", "token")
	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}
	if len(fileErrs) != 1 {
		t.Fatalf("expected 1 file error, got %d", len(fileErrs))
	}
	fdErr, ok := fileErrs[0].(*FileDataErr)
	if !ok {
		t.Fatalf("expected *FileDataErr, got %T", fileErrs[0])
	}
	if fdErr.StatusCode != ErrCodeRawFetch {
		t.Errorf("expected ErrCodeRawFetch, got %d", fdErr.StatusCode)
	}
}

func TestReqTreeRaw_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, _, err := reqTreeRaw(srv.URL, "token")
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

// --- GitLab provider tests ---

// makeGitlabTreeHandler encodes nodes as the bare JSON array GitLab returns.
// nextPage is written into X-Next-Page; pass "" for the last page.
func makeGitlabTreeHandler(nodes []gitlabTreeNode, nextPage string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if nextPage != "" {
			w.Header().Set("X-Next-Page", nextPage)
		}
		json.NewEncoder(w).Encode(nodes)
	}
}

func TestGitlab_Basic(t *testing.T) {
	mux := http.NewServeMux()
	// Go 1.22+ ServeMux matches on the escaped path, so %2F must appear in the pattern.
	mux.HandleFunc("/projects/owner%2Frepo/repository/tree", makeGitlabTreeHandler([]gitlabTreeNode{
		{ID: "id1", Name: "main.ts", Type: "blob", Path: "main.ts", Mode: "100644"},
		{ID: "id2", Name: "index.js", Type: "blob", Path: "index.js", Mode: "100644"},
	}, ""))
	mux.HandleFunc("/projects/owner%2Frepo/repository/files/main.ts", makeBlobHandler("const x = 1"))
	mux.HandleFunc("/projects/owner%2Frepo/repository/files/index.js", makeBlobHandler("module.exports = {}"))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	files, errs, err := FetchfileTree(&GitlabProvider{BaseUrl: srv.URL}, "owner", "repo", "main", "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("unexpected file errors: %v", errs)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	byPath := make(map[string]FileData)
	for _, f := range files {
		byPath[f.Path] = f
	}
	if byPath["main.ts"].RawFileData != "const x = 1" {
		t.Errorf("unexpected content for main.ts: %q", byPath["main.ts"].RawFileData)
	}
	if byPath["index.js"].RawFileData != "module.exports = {}" {
		t.Errorf("unexpected content for index.js: %q", byPath["index.js"].RawFileData)
	}
}

func TestGitlab_Pagination(t *testing.T) {
	page1 := []gitlabTreeNode{{ID: "id1", Name: "a.ts", Type: "blob", Path: "a.ts", Mode: "100644"}}
	page2 := []gitlabTreeNode{{ID: "id2", Name: "b.tsx", Type: "blob", Path: "b.tsx", Mode: "100644"}}

	mux := http.NewServeMux()
	mux.HandleFunc("/projects/owner%2Frepo/repository/tree", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			json.NewEncoder(w).Encode(page2) // no X-Next-Page → last page
		} else {
			w.Header().Set("X-Next-Page", "2")
			json.NewEncoder(w).Encode(page1)
		}
	})
	mux.HandleFunc("/projects/owner%2Frepo/repository/files/a.ts", makeBlobHandler("export const a = 1"))
	mux.HandleFunc("/projects/owner%2Frepo/repository/files/b.tsx", makeBlobHandler("export const b = 2"))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	files, errs, err := FetchfileTree(&GitlabProvider{BaseUrl: srv.URL}, "owner", "repo", "main", "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("unexpected file errors: %v", errs)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files across pages, got %d", len(files))
	}
	byPath := make(map[string]FileData)
	for _, f := range files {
		byPath[f.Path] = f
	}
	if byPath["a.ts"].RawFileData != "export const a = 1" {
		t.Errorf("unexpected content for a.ts: %q", byPath["a.ts"].RawFileData)
	}
	if byPath["b.tsx"].RawFileData != "export const b = 2" {
		t.Errorf("unexpected content for b.tsx: %q", byPath["b.tsx"].RawFileData)
	}
}

func TestGitlab_FilteredPaths(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/projects/owner%2Frepo/repository/tree", makeGitlabTreeHandler([]gitlabTreeNode{
		{ID: "id1", Name: "index.js", Type: "blob", Path: "node_modules/pkg/index.js", Mode: "100644"},
		{ID: "id2", Name: "app.ts", Type: "blob", Path: "src/app.ts", Mode: "100644"},
	}, ""))
	// url.PathEscape("src/app.ts") = "src%2Fapp.ts"
	mux.HandleFunc("/projects/owner%2Frepo/repository/files/src%2Fapp.ts", makeBlobHandler("export {}"))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	files, _, err := FetchfileTree(&GitlabProvider{BaseUrl: srv.URL}, "owner", "repo", "main", "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file after filtering, got %d", len(files))
	}
	if files[0].Path != "src/app.ts" {
		t.Errorf("expected src/app.ts, got %q", files[0].Path)
	}
}

func TestGitlab_BlobFetchError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/projects/owner%2Frepo/repository/tree", makeGitlabTreeHandler([]gitlabTreeNode{
		{ID: "id1", Name: "broken.ts", Type: "blob", Path: "broken.ts", Mode: "100644"},
	}, ""))
	mux.HandleFunc("/projects/owner%2Frepo/repository/files/broken.ts", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, fileErrs, err := FetchfileTree(&GitlabProvider{BaseUrl: srv.URL}, "owner", "repo", "main", "token")
	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}
	if len(fileErrs) != 1 {
		t.Fatalf("expected 1 file error, got %d", len(fileErrs))
	}
	fdErr, ok := fileErrs[0].(*FileDataErr)
	if !ok {
		t.Fatalf("expected *FileDataErr, got %T", fileErrs[0])
	}
	if fdErr.StatusCode != ErrCodeRawFetch {
		t.Errorf("expected ErrCodeRawFetch, got %d", fdErr.StatusCode)
	}
}

// --- M-1.3 tests ---

func TestFetchBlobBatch_60Files(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/git/trees/main", func(w http.ResponseWriter, r *http.Request) {
		var subtrees []SubTree
		for i := range 60 {
			subtrees = append(subtrees, SubTree{
				Path: fmt.Sprintf("file_%d.ts", i),
				Type: "blob",
				SHA:  fmt.Sprintf("sha_%d", i),
			})
		}
		json.NewEncoder(w).Encode(ResponseTree{Tree: subtrees})
	})
	for i := range 60 {
		path := fmt.Sprintf("/repos/owner/repo/git/blobs/sha_%d", i)
		content := fmt.Sprintf("export const file%d = 1", i)
		mux.HandleFunc(path, makeBlobHandler(content))
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()

	files, errs, err := FetchfileTree(&GithubProvider{BaseUrl: srv.URL}, "owner", "repo", "main", "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("unexpected file errors: %v", errs)
	}
	if len(files) != 60 {
		t.Fatalf("expected 60 files, got %d", len(files))
	}
	byPath := make(map[string]FileData)
	for _, f := range files {
		byPath[f.Path] = f
	}
	for i := range 60 {
		path := fmt.Sprintf("file_%d.ts", i)
		want := fmt.Sprintf("export const file%d = 1", i)
		if byPath[path].RawFileData != want {
			t.Errorf("file %s: got %q, want %q", path, byPath[path].RawFileData, want)
		}
	}
}

func TestFetchBlobBatch_Concurrent(t *testing.T) {
	const handlerDelay = 5 * time.Millisecond

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/git/trees/main", func(w http.ResponseWriter, r *http.Request) {
		var subtrees []SubTree
		for i := range 60 {
			subtrees = append(subtrees, SubTree{
				Path: fmt.Sprintf("file_%d.ts", i),
				Type: "blob",
				SHA:  fmt.Sprintf("sha_%d", i),
			})
		}
		json.NewEncoder(w).Encode(ResponseTree{Tree: subtrees})
	})
	for i := range 60 {
		path := fmt.Sprintf("/repos/owner/repo/git/blobs/sha_%d", i)
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(handlerDelay)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"content": base64.StdEncoding.EncodeToString([]byte("data"))})
		})
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()

	start := time.Now()
	_, _, err := FetchfileTree(&GithubProvider{BaseUrl: srv.URL}, "owner", "repo", "main", "token")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 3 batches × 5ms concurrent ≈ 15ms; 60 sequential × 5ms = 300ms.
	// 200ms cleanly separates the two.
	if elapsed > 200*time.Millisecond {
		t.Errorf("expected concurrent fetch under 200ms, took %v", elapsed)
	}
}

func TestFetchBlobBatch_RateLimit(t *testing.T) {
	mux := http.NewServeMux()
	for i := range 5 {
		path := fmt.Sprintf("/repos/owner/repo/git/blobs/sha_%d", i)
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "5") // below blobBatchSize(20)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"content": base64.StdEncoding.EncodeToString([]byte("data"))})
		})
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()

	var batch []FileData
	for i := range 5 {
		batch = append(batch, FileData{
			Path: fmt.Sprintf("file_%d.go", i),
			Type: "blob",
			SHA:  fmt.Sprintf("sha_%d", i),
		})
	}

	var slept bool
	mockSleep := func(d time.Duration) { slept = true }

	_, errs := fetchBlobBatch(&GithubProvider{BaseUrl: srv.URL}, "owner", "repo", "token", batch, mockSleep)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if !slept {
		t.Error("expected checkRateLimit to sleep when remaining < blobBatchSize")
	}
}

func TestReqRawData_InvalidBase64(t *testing.T) {
	// Drive it through FetchfileTree so the decode error surfaces as a FileDataErr
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/git/trees/main", makeTreeHandler(ResponseTree{
		Tree: []SubTree{{Path: "bad.ts", Type: "blob", SHA: "sha1"}},
	}))
	mux.HandleFunc("/repos/owner/repo/git/blobs/sha1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"content": "!!!not-valid-base64!!!"})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, fileErrs, err := FetchfileTree(&GithubProvider{BaseUrl: srv.URL}, "owner", "repo", "main", "token")
	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}
	if len(fileErrs) != 1 {
		t.Fatalf("expected 1 file error, got %d", len(fileErrs))
	}
	fdErr, ok := fileErrs[0].(*FileDataErr)
	if !ok {
		t.Fatalf("expected *FileDataErr")
	}
	if fdErr.StatusCode != ErrCodeDecode {
		t.Errorf("expected ErrCodeDecode, got %d", fdErr.StatusCode)
	}
}
