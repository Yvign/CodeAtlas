package committer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"CodeAtlas/internal/core/graph"
)

var (
	ErrUnknownProvider      = errors.New("unknown provider: must be \"github\" or \"gitlab\"")
	ErrConflictUnresolvable = errors.New("conflict unresolvable after 3 attempts")
)

type Committer struct {
	Token    string
	Provider string
}

type commitBody struct {
	Message string `json:"message"`
	Content string `json:"content"`
	Branch  string `json:"branch"`
	SHA     string `json:"sha,omitempty"`
}

type githubResponse struct {
	Content struct {
		SHA string `json:"sha"`
	} `json:"content"`
}

type gitlabResponse struct {
	BlobID string `json:"blob_id"`
}

func (c *Committer) contentURL(owner, repo string) (string, error) {
	switch c.Provider {
	case "github":
		return fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/.codeatlas/graph.json", owner, repo), nil
	case "gitlab":
		return fmt.Sprintf("https://gitlab.com/api/v4/projects/%s%%2F%s/repository/files/.codeatlas%%2Fgraph.json", owner, repo), nil
	default:
		return "", ErrUnknownProvider
	}
}

// fetchLatest GETs the current .codeatlas/graph.json and returns the decoded
// GraphRecord and the blob SHA to use as the next PUT's currentSHA.
func (c *Committer) fetchLatest(ctx context.Context, owner, repo, branch string) (graph.GraphRecord, string, error) {
	base, err := c.contentURL(owner, repo)
	if err != nil {
		return graph.GraphRecord{}, "", err
	}
	url := base
	if c.Provider == "gitlab" {
		url = base + "?ref=" + branch
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return graph.GraphRecord{}, "", fmt.Errorf("build GET request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return graph.GraphRecord{}, "", fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return graph.GraphRecord{}, "", fmt.Errorf("fetch latest: status %d", resp.StatusCode)
	}

	var rawContent, blobSHA string
	switch c.Provider {
	case "github":
		var ghResp struct {
			SHA     string `json:"sha"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&ghResp); err != nil {
			return graph.GraphRecord{}, "", fmt.Errorf("decode GET response: %w", err)
		}
		rawContent = ghResp.Content
		blobSHA = ghResp.SHA
	case "gitlab":
		var glResp struct {
			BlobID  string `json:"blob_id"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&glResp); err != nil {
			return graph.GraphRecord{}, "", fmt.Errorf("decode GET response: %w", err)
		}
		rawContent = glResp.Content
		blobSHA = glResp.BlobID
	}

	// GitHub wraps base64 content with newlines every 60 characters.
	rawContent = strings.ReplaceAll(rawContent, "\n", "")

	decoded, err := base64.StdEncoding.DecodeString(rawContent)
	if err != nil {
		return graph.GraphRecord{}, "", fmt.Errorf("decode base64 content: %w", err)
	}

	var record graph.GraphRecord
	if err := json.Unmarshal(decoded, &record); err != nil {
		return graph.GraphRecord{}, "", fmt.Errorf("unmarshal graph record: %w", err)
	}

	return record, blobSHA, nil
}

// mergeRecords produces a merged GraphRecord for conflict resolution.
// Nodes and Edges come from latest (computed repo truth).
// Description and Pos are taken from pending for any node whose UUID
// matches (user-authored fields win). Metadata comes from pending.
func mergeRecords(pending, latest graph.GraphRecord) graph.GraphRecord {
	pendingByUUID := make(map[string]graph.NodeRecord, len(pending.Nodes))
	for _, n := range pending.Nodes {
		pendingByUUID[n.UUID] = n
	}

	merged := latest
	for i, n := range merged.Nodes {
		if p, ok := pendingByUUID[n.UUID]; ok {
			merged.Nodes[i].Description = p.Description
			merged.Nodes[i].Pos = p.Pos
		}
	}

	merged.Version = pending.Version
	merged.GeneratedAt = pending.GeneratedAt
	merged.RepoOwner = pending.RepoOwner
	merged.RepoName = pending.RepoName
	merged.Branch = pending.Branch

	return merged
}

func (c *Committer) CommitGraphFile(ctx context.Context, owner, repo, branch string, record graph.GraphRecord, currentSHA string) (newSHA string, err error) {
	url, err := c.contentURL(owner, repo)
	if err != nil {
		return "", err
	}

	pending := record
	sha := currentSHA

	for attempt := 0; attempt < 3; attempt++ {
		jsonBytes, err := json.MarshalIndent(pending, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal graph record: %w", err)
		}

		bodyStruct := commitBody{
			Message: "codeatlas: regenerate graph",
			Content: base64.StdEncoding.EncodeToString(jsonBytes),
			Branch:  branch,
			SHA:     sha,
		}
		bodyBytes, err := json.Marshal(bodyStruct)
		if err != nil {
			return "", fmt.Errorf("marshal request body: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("PUT %s: %w", url, err)
		}

		if resp.StatusCode == http.StatusUnprocessableEntity {
			resp.Body.Close()
			if attempt == 2 {
				break
			}
			latest, latestSHA, fetchErr := c.fetchLatest(ctx, owner, repo, branch)
			if fetchErr != nil {
				return "", fetchErr
			}
			pending = mergeRecords(pending, latest)
			sha = latestSHA
			continue
		}

		switch c.Provider {
		case "github":
			if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				return "", fmt.Errorf("github: unexpected status %d", resp.StatusCode)
			}
			var parsed githubResponse
			decodeErr := json.NewDecoder(resp.Body).Decode(&parsed)
			resp.Body.Close()
			if decodeErr != nil {
				return "", fmt.Errorf("decode github response: %w", decodeErr)
			}
			return parsed.Content.SHA, nil

		case "gitlab":
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				return "", fmt.Errorf("gitlab: unexpected status %d", resp.StatusCode)
			}
			var parsed gitlabResponse
			decodeErr := json.NewDecoder(resp.Body).Decode(&parsed)
			resp.Body.Close()
			if decodeErr != nil {
				return "", fmt.Errorf("decode gitlab response: %w", decodeErr)
			}
			return parsed.BlobID, nil

		default:
			resp.Body.Close()
			return "", ErrUnknownProvider
		}
	}

	return "", ErrConflictUnresolvable
}
