package graph

import (
	"testing"

	"github.com/google/uuid"
)

func TestMerge_EmptyBoth(t *testing.T) {
	result := Merge(GraphStructure{}, GraphStructure{})
	if len(result.Nodes) != 0 || len(result.Edges) != 0 {
		t.Fatal("expected empty result for empty inputs")
	}
}

func TestMerge_NoExisting_AssignsNewUUIDs(t *testing.T) {
	fresh := GraphStructure{
		Nodes: []Node{
			{Name: "foo", Path: "src/foo.ts", SHA: "abc123"},
			{Name: "bar", Path: "src/bar.ts", SHA: "def456"},
		},
		Edges: []Edge{{Source: "src/foo.ts", Target: "src/bar.ts"}},
	}

	result := Merge(fresh, GraphStructure{})

	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(result.Nodes))
	}
	for _, n := range result.Nodes {
		if _, err := uuid.Parse(n.UUID); err != nil {
			t.Errorf("node %q has invalid UUID: %q", n.Name, n.UUID)
		}
	}
	if len(result.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(result.Edges))
	}
}

func TestMerge_SHAMatch_PreservesUUIDAndAnnotations(t *testing.T) {
	existingUUID := uuid.NewString()
	existing := GraphStructure{
		Nodes: []Node{
			{UUID: existingUUID, Name: "foo", Path: "src/foo.ts", SHA: "abc123",
				Description: "desc", Notes: "notes"},
		},
	}
	fresh := GraphStructure{
		Nodes: []Node{
			{Name: "foo", Path: "src/foo.ts", SHA: "abc123"},
		},
	}

	result := Merge(fresh, existing)

	if result.Nodes[0].UUID != existingUUID {
		t.Errorf("expected UUID %q, got %q", existingUUID, result.Nodes[0].UUID)
	}
	if result.Nodes[0].Description != "desc" {
		t.Error("expected description to be preserved")
	}
	if result.Nodes[0].Notes != "notes" {
		t.Error("expected notes to be preserved")
	}
}

func TestMerge_PathMatch_WhenSHADiffers(t *testing.T) {
	existingUUID := uuid.NewString()
	existing := GraphStructure{
		Nodes: []Node{
			{UUID: existingUUID, Name: "foo", Path: "src/foo.ts", SHA: "old-sha",
				Description: "desc", Notes: "notes"},
		},
	}
	fresh := GraphStructure{
		Nodes: []Node{
			{Name: "foo", Path: "src/foo.ts", SHA: "new-sha"},
		},
	}

	result := Merge(fresh, existing)

	if result.Nodes[0].UUID != existingUUID {
		t.Errorf("expected UUID %q via path match, got %q", existingUUID, result.Nodes[0].UUID)
	}
	if result.Nodes[0].SHA != "new-sha" {
		t.Error("expected fresh SHA to be kept")
	}
}

func TestMerge_NameMatch_UniqueNamePreservesUUID(t *testing.T) {
	existingUUID := uuid.NewString()
	existing := GraphStructure{
		Nodes: []Node{
			{UUID: existingUUID, Name: "uniqueName", Description: "d", Notes: "n"},
		},
	}
	fresh := GraphStructure{
		Nodes: []Node{
			{Name: "uniqueName", Path: "new/path.ts"},
		},
	}

	result := Merge(fresh, existing)

	if result.Nodes[0].UUID != existingUUID {
		t.Errorf("expected UUID %q via name match, got %q", existingUUID, result.Nodes[0].UUID)
	}
}

func TestMerge_NameMatch_AmbiguousNameAssignsNewUUID(t *testing.T) {
	existing := GraphStructure{
		Nodes: []Node{
			{UUID: uuid.NewString(), Name: "common"},
			{UUID: uuid.NewString(), Name: "common"},
		},
	}
	fresh := GraphStructure{
		Nodes: []Node{
			{Name: "common"},
		},
	}

	result := Merge(fresh, existing)

	if _, err := uuid.Parse(result.Nodes[0].UUID); err != nil {
		t.Errorf("expected a valid new UUID for ambiguous name, got %q", result.Nodes[0].UUID)
	}
	// Should not match either existing UUID since there are two candidates
	for _, ex := range existing.Nodes {
		if result.Nodes[0].UUID == ex.UUID {
			t.Error("ambiguous name match should not reuse an existing UUID")
		}
	}
}

func TestMerge_SHAClaimedNodeNotReusedByPath(t *testing.T) {
	existingUUID := uuid.NewString()
	existing := GraphStructure{
		Nodes: []Node{
			{UUID: existingUUID, Name: "foo", Path: "src/foo.ts", SHA: "abc123"},
		},
	}
	// Two fresh nodes: first matches by SHA, second has same path but different SHA
	fresh := GraphStructure{
		Nodes: []Node{
			{Name: "foo", Path: "src/foo.ts", SHA: "abc123"},
			{Name: "bar", Path: "src/foo.ts", SHA: "other"},
		},
	}

	result := Merge(fresh, existing)

	if result.Nodes[0].UUID != existingUUID {
		t.Errorf("first node should claim existing UUID via SHA")
	}
	if result.Nodes[1].UUID == existingUUID {
		t.Error("second node should not reuse already-claimed UUID")
	}
	if _, err := uuid.Parse(result.Nodes[1].UUID); err != nil {
		t.Errorf("second node should have a new valid UUID, got %q", result.Nodes[1].UUID)
	}
}

func TestMerge_FreshEdgesPassThrough(t *testing.T) {
	fresh := GraphStructure{
		Nodes: []Node{{Name: "a"}, {Name: "b"}},
		Edges: []Edge{
			{Source: "a", Target: "b"},
			{Source: "b", Target: "a"},
		},
	}

	result := Merge(fresh, GraphStructure{})

	if len(result.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(result.Edges))
	}
	if result.Edges[0].Source != "a" || result.Edges[1].Source != "b" {
		t.Error("edges did not pass through correctly")
	}
}

func TestMerge_NodesWithoutSHAOrPath_FallToNameThenNew(t *testing.T) {
	existingUUID := uuid.NewString()
	existing := GraphStructure{
		Nodes: []Node{
			{UUID: existingUUID, Name: "orphan"},
		},
	}
	fresh := GraphStructure{
		Nodes: []Node{
			{Name: "orphan"}, // no SHA, no path
		},
	}

	result := Merge(fresh, existing)

	if result.Nodes[0].UUID != existingUUID {
		t.Errorf("expected name-match to find existing UUID, got %q", result.Nodes[0].UUID)
	}
}
