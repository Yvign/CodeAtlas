package fetcher

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

type QueueItem struct {
	Sha       string
	PageParam string
}

type ResponseTree struct {
	SHA       string    `json:"sha"`
	Tree      []SubTree `json:"tree"`
	Truncated bool      `json:"truncated"`
}

type SubTree struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

type Provider interface {
	TreeUrl(owner, repo, branch, page string) string
	BlobUrl(owner, repo, path, ref string) string
	ParseBlob(data []byte) (string, error)
	ParseTree(data []byte) ([]SubTree, error)
	NextPage(body []byte, header http.Header, currQueueItem QueueItem) []QueueItem
}

// --- GitHub ---

type GithubProvider struct {
	BaseUrl string
}

func NewGithubProvider() *GithubProvider {
	return &GithubProvider{BaseUrl: "https://api.github.com"}
}

func (g *GithubProvider) TreeUrl(owner, repo, branch, recursive string) string {
	return g.BaseUrl + "/repos/" + owner + "/" + repo + "/git/trees/" + branch + "?recursive=" + recursive
}

func (g *GithubProvider) BlobUrl(owner, repo, path, ref string) string {
	return g.BaseUrl + "/repos/" + owner + "/" + repo + "/git/blobs/" + ref
}

func (g *GithubProvider) ParseBlob(data []byte) (string, error) {
	var blob struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(bytes.NewBuffer(data)).Decode(&blob); err != nil {
		return "", err
	}
	content := strings.ReplaceAll(blob.Content, "\n", "")
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func (g *GithubProvider) ParseTree(data []byte) ([]SubTree, error) {
	var tree ResponseTree
	if err := json.NewDecoder(bytes.NewBuffer(data)).Decode(&tree); err != nil {
		return nil, err
	}
	return tree.Tree, nil
}

func (g *GithubProvider) NextPage(body []byte, _ http.Header, currQueueItem QueueItem) []QueueItem {
	var tree ResponseTree
	if err := json.NewDecoder(bytes.NewBuffer(body)).Decode(&tree); err != nil {
		return nil
	}
	if !tree.Truncated {
		return nil
	}
	return []QueueItem{{Sha: currQueueItem.Sha, PageParam: "0"}}
}

// --- GitLab ---

type GitlabProvider struct {
	BaseUrl string
}

func NewGitlabProvider() *GitlabProvider {
	return &GitlabProvider{BaseUrl: "https://gitlab.com/api/v4"}
}

func (g *GitlabProvider) TreeUrl(owner, repo, branch, page string) string {
	return g.BaseUrl + "/projects/" + owner + "%2F" + repo + "/repository/tree?ref=" + branch + "&per_page=100&page=" + page
}

func (g *GitlabProvider) BlobUrl(owner, repo, path, ref string) string {
	path = url.PathEscape(path)
	return g.BaseUrl + "/projects/" + owner + "%2F" + repo + "/repository/files/" + path + "?ref=" + ref
}

func (g *GitlabProvider) ParseBlob(data []byte) (string, error) {
	var blob struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(bytes.NewBuffer(data)).Decode(&blob); err != nil {
		return "", err
	}
	content := strings.ReplaceAll(blob.Content, "\n", "")
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// gitlabTreeNode mirrors the GitLab tree API response, which uses "id" for the SHA.
type gitlabTreeNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Path string `json:"path"`
	Mode string `json:"mode"`
}

func (g *GitlabProvider) ParseTree(data []byte) ([]SubTree, error) {
	var nodes []gitlabTreeNode
	if err := json.NewDecoder(bytes.NewBuffer(data)).Decode(&nodes); err != nil {
		return nil, err
	}
	subtrees := make([]SubTree, len(nodes))
	for i, n := range nodes {
		subtrees[i] = SubTree{Path: n.Path, Mode: n.Mode, Type: n.Type, SHA: n.ID}
	}
	return subtrees, nil
}

func (g *GitlabProvider) NextPage(_ []byte, header http.Header, currQueueItem QueueItem) []QueueItem {
	next := header.Get("X-Next-Page")
	if next == "" {
		return nil
	}
	return []QueueItem{{Sha: currQueueItem.Sha, PageParam: next}}
}