package worker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"CodeAtlas/internal/core/analyzer"
	"CodeAtlas/internal/core/committer"
	"CodeAtlas/internal/core/fetcher"
	"CodeAtlas/internal/core/graph"
)

type Worker struct {
	Owner         string
	Repo          string
	Branch        string
	Provider      string
	Token         string
	GraphID       string
	CommitEnabled bool
	OnComplete    func(graphID string, status string, errMsg string, record *graph.GraphRecord)
}

func (w *Worker) Run(ctx context.Context) error {
	fail := func(err error) error {
		w.OnComplete(w.GraphID, "failed", err.Error(), nil)
		return err
	}

	var p fetcher.Provider
	switch w.Provider {
	case "github":
		p = fetcher.NewGithubProvider()
	case "gitlab":
		p = fetcher.NewGitlabProvider()
	default:
		return fail(fmt.Errorf("unknown provider %q", w.Provider))
	}

	// Step 1: fetch file tree
	files, _, err := fetcher.FetchfileTree(p, w.Owner, w.Repo, w.Branch, w.Token)
	if err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}

	// Step 2: analyze → GraphStructure
	result := analyzer.Analyze(files)
	freshNodes := make([]graph.Node, len(result.Nodes))
	for i, n := range result.Nodes {
		freshNodes[i] = graph.Node{UUID: n.UUID, Type: n.Type, Name: n.Name, SHA: n.SHA, Path: n.Path}
	}
	freshEdges := make([]graph.Edge, len(result.Edges))
	for i, e := range result.Edges {
		freshEdges[i] = graph.Edge{Source: e.Source, Target: e.Target, Type: e.Type}
	}
	fresh := graph.GraphStructure{Nodes: freshNodes, Edges: freshEdges}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}

	// Step 3: fetch existing graph.json (404 → empty structure)
	existing, currentSHA, err := w.fetchExistingGraph(ctx)
	if err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}

	// Step 4: merge fresh analysis with existing (preserves user annotations)
	merged := graph.Merge(fresh, existing)

	// Step 5: build GraphRecord from merged structure
	mergedNodes := make([]graph.NodeRecord, len(merged.Nodes))
	for i, n := range merged.Nodes {
		mergedNodes[i] = graph.NodeRecord{
			UUID:        n.UUID,
			Type:        n.Type,
			Name:        n.Name,
			SHA:         n.SHA,
			Path:        n.Path,
			Description: n.Description,
		}
	}
	mergedEdges := make([]graph.EdgeRecord, len(merged.Edges))
	for i, e := range merged.Edges {
		mergedEdges[i] = graph.EdgeRecord{Source: e.Source, Target: e.Target, Type: e.Type}
	}
	record := graph.GraphRecord{
		Version:     "1",
		GeneratedAt: time.Now().UTC(),
		RepoOwner:   w.Owner,
		RepoName:    w.Repo,
		Branch:      w.Branch,
		Nodes:       mergedNodes,
		Edges:       mergedEdges,
	}

	// Step 6: commit
	if w.CommitEnabled {
		c := &committer.Committer{Token: w.Token, Provider: w.Provider}
		if _, err := c.CommitGraphFile(ctx, w.Owner, w.Repo, w.Branch, record, currentSHA); err != nil {
			return fail(err)
		}
		w.OnComplete(w.GraphID, "ready", "", nil)
	} else {
		w.OnComplete(w.GraphID, "ready", "", &record)
	}
	return nil
}

// fetchExistingGraph GETs .codeatlas/graph.json from the provider.
// On 404 it returns an empty GraphStructure and empty SHA (first commit).
// On 200 it decodes the file into a GraphStructure and returns the blob SHA.
func (w *Worker) fetchExistingGraph(ctx context.Context) (graph.GraphStructure, string, error) {
	var url string
	switch w.Provider {
	case "github":
		url = fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/.codeatlas/graph.json", w.Owner, w.Repo)
	case "gitlab":
		url = fmt.Sprintf("https://gitlab.com/api/v4/projects/%s%%2F%s/repository/files/.codeatlas%%2Fgraph.json?ref=%s", w.Owner, w.Repo, w.Branch)
	default:
		return graph.GraphStructure{}, "", fmt.Errorf("unknown provider %q", w.Provider)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return graph.GraphStructure{}, "", fmt.Errorf("build GET request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+w.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return graph.GraphStructure{}, "", fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
		return graph.GraphStructure{}, "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return graph.GraphStructure{}, "", fmt.Errorf("fetch existing graph: status %d", resp.StatusCode)
	}

	var rawContent, blobSHA string
	switch w.Provider {
	case "github":
		var ghResp struct {
			SHA     string `json:"sha"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&ghResp); err != nil {
			return graph.GraphStructure{}, "", fmt.Errorf("decode response: %w", err)
		}
		rawContent = ghResp.Content
		blobSHA = ghResp.SHA
	case "gitlab":
		var glResp struct {
			BlobID  string `json:"blob_id"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&glResp); err != nil {
			return graph.GraphStructure{}, "", fmt.Errorf("decode response: %w", err)
		}
		rawContent = glResp.Content
		blobSHA = glResp.BlobID
	}

	// GitHub wraps base64 content with newlines every 60 characters.
	rawContent = strings.ReplaceAll(rawContent, "\n", "")

	decoded, err := base64.StdEncoding.DecodeString(rawContent)
	if err != nil {
		return graph.GraphStructure{}, "", fmt.Errorf("decode base64: %w", err)
	}

	var rec graph.GraphRecord
	if err := json.Unmarshal(decoded, &rec); err != nil {
		return graph.GraphStructure{}, "", fmt.Errorf("unmarshal graph record: %w", err)
	}

	nodes := make([]graph.Node, len(rec.Nodes))
	for i, n := range rec.Nodes {
		nodes[i] = graph.Node{
			UUID:        n.UUID,
			Type:        n.Type,
			Name:        n.Name,
			SHA:         n.SHA,
			Path:        n.Path,
			Description: n.Description,
		}
	}
	edges := make([]graph.Edge, len(rec.Edges))
	for i, e := range rec.Edges {
		edges[i] = graph.Edge{Source: e.Source, Target: e.Target, Type: e.Type}
	}

	return graph.GraphStructure{Nodes: nodes, Edges: edges}, blobSHA, nil
}
