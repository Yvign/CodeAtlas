package analyzer

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"CodeAtlas/internal/core/fetcher"
	"CodeAtlas/internal/core/graph"
)

type Node struct {
	UUID        string
	Type        string
	Name        string
	SHA         string
	Path        string
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
	for _, f := range files {
		nodeType := "file"
		if f.Type == "tree" {
			nodeType = "directory"
		}
		nodes = append(nodes, Node{
			UUID: uuid.NewString(),
			Type: nodeType,
			Name: path.Base(f.Path),
			SHA:  f.SHA,
			Path: f.Path,
		})
	}

	// Build a path → UUID lookup so dependency and parent-child edges
	// reference node identities instead of paths.
	pathToUUID := make(map[string]string, len(nodes))
	for _, n := range nodes {
		pathToUUID[n.Path] = n.UUID
	}

	var edges []Edge

	// Dependency edges (import resolution), deduplicated by source+target UUID.
	seenDepEdge := make(map[[2]string]bool)
	for _, f := range files {
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
		fmt.Printf("analyzer: %s → %d imports found\n", f.Path, len(imports))
		for _, raw := range imports {
			resolved := resolve(f.Path, raw, pathSet)
			if resolved == "" {
				continue
			}
			sourceUUID, sourceOK := pathToUUID[f.Path]
			targetUUID, targetOK := pathToUUID[resolved]
			if !sourceOK || !targetOK {
				continue
			}
			key := [2]string{sourceUUID, targetUUID}
			if seenDepEdge[key] {
				continue
			}
			seenDepEdge[key] = true
			edges = append(edges, Edge{Source: sourceUUID, Target: targetUUID, Type: graph.EdgeTypeDependency})
		}
	}

	for _, n := range nodes {
		dir := filepath.Dir(n.Path)
		if dir == "." {
			continue
		}
		childUUID, childOK := pathToUUID[n.Path]
		parentUUID, parentOK := pathToUUID[dir]
		if !childOK || !parentOK {
			continue
		}
		edges = append(edges, Edge{Source: parentUUID, Target: childUUID, Type: graph.EdgeTypeContains})
	}

	depEdgeCount := 0
	containsEdgeCount := 0
	for _, e := range edges {
		switch e.Type {
		case graph.EdgeTypeDependency:
			depEdgeCount++
		case graph.EdgeTypeContains:
			containsEdgeCount++
		}
	}
	fmt.Printf("analyzer: total %d nodes, %d dependency edges, %d contains edges\n", len(nodes), depEdgeCount, containsEdgeCount)

	return AnalysisResult{Nodes: nodes, Edges: edges}
}