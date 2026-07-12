package graph

import "github.com/google/uuid"

type Node struct {
	UUID        string `json:"uuid"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	SHA         string `json:"sha"`
	Description string `json:"description"`
	Notes       string `json:"notes"`
	Path        string `json:"path"`
}

const (
	EdgeTypeContains   = "contains"
	EdgeTypeDependency = "dependency"
)

type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

type GraphStructure struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

func Merge(newGraph GraphStructure, existingGraph GraphStructure) GraphStructure {
	// Step 1 — index existing nodes
	bySHA := make(map[string]Node, len(existingGraph.Nodes))
	byPath := make(map[string]Node, len(existingGraph.Nodes))
	for _, n := range existingGraph.Nodes {
		if n.SHA != "" {
			bySHA[n.SHA] = n
		}
		if n.Path != "" {
			byPath[n.Path] = n
		}
	}

	claimed := make(map[string]bool) // keyed by existing node UUID

	type deferred struct {
		idx  int
		name string
	}
	var deferred_ []deferred
	merged := make([]Node, len(newGraph.Nodes))

	// Step 2 — SHA then path match
	for i, fn := range newGraph.Nodes {
		merged[i] = fn

		if fn.SHA != "" {
			if ex, ok := bySHA[fn.SHA]; ok {
				merged[i].UUID = ex.UUID
				merged[i].Description = ex.Description
				merged[i].Notes = ex.Notes
				merged[i].Path = ex.Path
				claimed[ex.UUID] = true
				continue
			}
		}

		if fn.Path != "" {
			if ex, ok := byPath[fn.Path]; ok && !claimed[ex.UUID] {
				merged[i].UUID = ex.UUID
				merged[i].Description = ex.Description
				merged[i].Notes = ex.Notes
				merged[i].Path = ex.Path
				claimed[ex.UUID] = true
				continue
			}
		}

		deferred_ = append(deferred_, deferred{idx: i, name: fn.Name})
	}

	// Step 3 — build byName from unclaimed existing nodes
	byName := make(map[string][]Node)
	for _, n := range existingGraph.Nodes {
		if !claimed[n.UUID] {
			byName[n.Name] = append(byName[n.Name], n)
		}
	}

	// Step 4 — resolve deferred nodes by name
	for _, d := range deferred_ {
		matches := byName[d.name]
		if len(matches) == 1 {
			merged[d.idx].UUID = matches[0].UUID
			merged[d.idx].Description = matches[0].Description
			merged[d.idx].Notes = matches[0].Notes
			merged[d.idx].Path = matches[0].Path
		} else {
			merged[d.idx].UUID = uuid.NewString()
		}
	}

	// Step 5 — remap edges from each fresh node's provisional UUID (assigned
	// per-run by the analyzer) to its final merged UUID, so edges built from
	// those provisional UUIDs stay valid once node identities are reconciled.
	oldToFinal := make(map[string]string, len(newGraph.Nodes))
	for i, fn := range newGraph.Nodes {
		if fn.UUID == "" {
			continue
		}
		oldToFinal[fn.UUID] = merged[i].UUID
	}
	mergedEdges := make([]Edge, len(newGraph.Edges))
	for i, e := range newGraph.Edges {
		mergedEdges[i] = e
		if final, ok := oldToFinal[e.Source]; ok {
			mergedEdges[i].Source = final
		}
		if final, ok := oldToFinal[e.Target]; ok {
			mergedEdges[i].Target = final
		}
	}

	return GraphStructure{
		Nodes: merged,
		Edges: mergedEdges,
	}
}
