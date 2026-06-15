package analyzer

import (
	"path"
	"strings"

	"CodeAtlas/internal/core/fetcher"
	"CodeAtlas/internal/core/graph"
)

type Node struct {
	Type        string
	Name        string
	SHA         string
	Description string
	Notes       string
}

type Edge struct {
	Source string
	Target string
	Type   string
}

type AnalysisResult struct {
	Nodes []Node
	Edges []Edge
}

func Analyze(files []fetcher.FileData) AnalysisResult {
	pathSet := make(map[string]bool, len(files))
	for _, f := range files {
		pathSet[f.Path] = true
	}

	var nodes []Node
	var edges []Edge

	for _, f := range files {
		nodes = append(nodes, Node{
			Type: "file",
			Name: path.Base(f.Path),
			SHA:  f.SHA,
		})

		if f.Type != "blob" {
			continue
		}

		ext := strings.ToLower(path.Ext(f.Path))
		validExt := false
		for _, known := range knownExts {
			if ext == known {
				validExt = true
				break
			}
		}
		if !validExt {
			continue
		}

		imports := ParseFile(f.RawFileData, f.Path)
		for _, raw := range imports {
			resolved := resolve(f.Path, raw, pathSet)
			if resolved == "" {
				continue
			}
			edges = append(edges, Edge{Source: f.Path, Target: resolved, Type: graph.EdgeTypeDependency})
		}
	}

	for _, f := range files {
		dir := path.Dir(f.Path)
		if dir == "." {
			continue
		}
		if pathSet[dir] {
			edges = append(edges, Edge{Source: dir, Target: f.Path, Type: graph.EdgeTypeContains})
		}
	}

	return AnalysisResult{Nodes: nodes, Edges: edges}
}
