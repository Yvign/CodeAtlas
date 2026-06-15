package graph

import "time"

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type NodeRecord struct {
	UUID        string   `json:"uuid"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	SHA         string   `json:"sha"`
	Path        string   `json:"path"`
	Description string   `json:"description"`
	Note        string   `json:"note"`
	Pos         Position `json:"pos"`
}

type EdgeRecord struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

type GraphRecord struct {
	Version     string       `json:"version"`
	GeneratedAt time.Time    `json:"generatedAt"`
	RepoOwner   string       `json:"repoOwner"`
	RepoName    string       `json:"repoName"`
	Branch      string       `json:"branch"`
	Nodes       []NodeRecord `json:"nodes"`
	Edges       []EdgeRecord `json:"edges"`
}
