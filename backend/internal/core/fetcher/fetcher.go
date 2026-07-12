package fetcher

import (
	"bytes"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type FileData struct {
	Path        string `json:"path"`
	Type        string `json:"type"`
	SHA         string `json:"sha"`
	RawFileData string `json:"content"`
}

type FileDataErr struct {
	FileName   string
	StatusCode int
	Message    string
}

func (d *FileDataErr) Error() string {
	return fmt.Sprintf("file %s not fetched, status code: %d, message: %s", d.FileName, d.StatusCode, d.Message)
}

const (
	ErrCodeRawFetch = 101
	ErrCodeDecode   = 102
	blobBatchSize   = 20
)

var filterFilesPath = map[string]bool{
	"node_modules": true,
	"dist":         true,
	"build":        true,
	".git":         true,
	".vscode":      true,
	".idea":        true,
}

// allowedExtensions controls which blobs get their content fetched and
// later parsed for imports. Files with other extensions still become graph
// nodes — they just don't get content fetched or parsed for edges.
var allowedExtensions = map[string]bool{
	".js":  true,
	".ts":  true,
	".jsx": true,
	".tsx": true,
}

// parseRateLimitRemaining returns the remaining request count and whether
// the header was present. A missing header is distinct from a genuine zero.
func parseRateLimitRemaining(headers http.Header) (int, bool) {
	raw := headers.Get("X-RateLimit-Remaining")
	if raw == "" {
		raw = headers.Get("RateLimit-Remaining")
	}
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return n, true
}

// checkRateLimit sleeps for 60 seconds when remaining drops below blobBatchSize,
// giving the quota time to recover before the next batch fires.
func checkRateLimit(remaining int, sleep func(time.Duration)) {
	if remaining < blobBatchSize {
		sleep(60 * time.Second)
	}
}

func reqRawData(blobURL string, token string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", blobURL, nil)
	if err != nil {
		return nil, -1, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	client := http.Client{Timeout: 30 * time.Second}
	do, err := client.Do(req)
	if err != nil {
		return nil, -1, err
	}
	defer do.Body.Close()

	remaining, hasRateLimit := parseRateLimitRemaining(do.Header)
	if !hasRateLimit {
		remaining = -1
	}

	if do.StatusCode != http.StatusOK {
		return nil, remaining, fmt.Errorf("unexpected status code: %d", do.StatusCode)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(do.Body); err != nil {
		return nil, remaining, err
	}
	return buf.Bytes(), remaining, nil
}

func reqTreeRaw(treeURL string, token string) ([]byte, http.Header, error) {
	req, err := http.NewRequest("GET", treeURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	client := http.Client{Timeout: 30 * time.Second}
	do, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer do.Body.Close()
	if do.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("unexpected status code: %d", do.StatusCode)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(do.Body); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), do.Header, nil
}

type blobResult struct {
	data      FileData
	remaining int
	err       error
	errCode   int
}

func fetchBlobBatch(provider Provider, owner, repo, token string, batch []FileData, sleep func(time.Duration)) ([]FileData, []error) {
	results := make([]blobResult, len(batch))
	var wg sync.WaitGroup

	for i, item := range batch {
		wg.Add(1)
		go func(i int, item FileData) {
			defer wg.Done()
			raw, remaining, err := reqRawData(provider.BlobUrl(owner, repo, item.Path, item.SHA), token)
			if err != nil {
				results[i] = blobResult{data: item, remaining: remaining, err: err, errCode: ErrCodeRawFetch}
				return
			}
			content, err := provider.ParseBlob(raw)
			if err != nil {
				results[i] = blobResult{data: item, remaining: remaining, err: err, errCode: ErrCodeDecode}
				return
			}
			item.RawFileData = content
			results[i] = blobResult{data: item, remaining: remaining}
		}(i, item)
	}
	wg.Wait()

	out := make([]FileData, len(batch))
	copy(out, batch)
	var errs []error
	minRemaining := -1
	for i, r := range results {
		if r.err != nil {
			errs = append(errs, &FileDataErr{FileName: r.data.Path, StatusCode: r.errCode, Message: r.err.Error()})
			continue
		}
		out[i] = r.data
		// Track the lowest remaining count seen across the batch.
		if r.remaining != -1 && (minRemaining == -1 || r.remaining < minRemaining) {
			minRemaining = r.remaining
		}
	}
	// Check once after all results are collected, not per-file.
	if minRemaining != -1 {
		checkRateLimit(minRemaining, sleep)
	}
	return out, errs
}

func FetchfileTree(provider Provider, owner, repo, branch, token string) ([]FileData, []error, error) {
	var queue []QueueItem
	var fetchedFiles []FileData
	queue = append(queue, QueueItem{Sha: branch, PageParam: "1"})

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		body, headers, err := reqTreeRaw(provider.TreeUrl(owner, repo, current.Sha, current.PageParam), token)
		if err != nil {
			return nil, nil, err
		}

		subtrees, err := provider.ParseTree(body)
		if err != nil {
			return nil, nil, err
		}

		for _, item := range subtrees {
			parts := strings.Split(item.Path, "/")
			filtered := false
			for _, p := range parts {
				if filterFilesPath[p] {
					filtered = true
					break
				}
			}
			if filtered {
				continue
			}
			fetchedFiles = append(fetchedFiles, FileData{Path: item.Path, Type: item.Type, SHA: item.SHA})
		}

		// GitHub only: when fetching flat (PageParam="0"), enqueue each subtree for individual recursive fetch.
		// GitLab never sets PageParam="0" — its flat per-page listing covers the full tree.
		if current.PageParam == "0" {
			for _, item := range subtrees {
				if item.Type == "tree" {
					queue = append(queue, QueueItem{Sha: item.SHA, PageParam: "1"})
				}
			}
		}

		queue = append(queue, provider.NextPage(body, headers, current)...)
	}

	// Collect blobs whose content is needed for import parsing. Every other
	// file (and every tree) still passes through as a node, just without
	// fetched content.
	var blobs []FileData
	var blobIndices []int
	for i, item := range fetchedFiles {
		if item.Type != "blob" {
			continue
		}
		if !allowedExtensions[strings.ToLower(filepath.Ext(item.Path))] {
			continue
		}
		blobs = append(blobs, item)
		blobIndices = append(blobIndices, i)
	}
	fmt.Printf("fetcher: %d files after extension filter (from %d total)\n", len(blobs), len(fetchedFiles))

	var fileDataErr []error
	for start := 0; start < len(blobs); start += blobBatchSize {
		end := start + blobBatchSize
		if end > len(blobs) {
			end = len(blobs)
		}
		batch := blobs[start:end]
		results, errs := fetchBlobBatch(provider, owner, repo, token, batch, time.Sleep)
		fileDataErr = append(fileDataErr, errs...)
		for j, res := range results {
			fetchedFiles[blobIndices[start+j]] = res
		}
	}

	return fetchedFiles, fileDataErr, nil	
}
