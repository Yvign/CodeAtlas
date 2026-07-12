package analyzer

import (
	"testing"

	"CodeAtlas/internal/core/fetcher"
	"CodeAtlas/internal/core/graph"
)

// --- ParseFile tests ---

func TestParseFile_BasicImport(t *testing.T) {
	src := `import foo from './foo'`
	imports := ParseFile(src, "index.ts")
	if len(imports) != 1 || imports[0] != "./foo" {
		t.Errorf("expected [./foo], got %v", imports)
	}
}

func TestParseFile_NamedImport(t *testing.T) {
	src := `import { bar, baz } from "../utils"`
	imports := ParseFile(src, "src/app.tsx")
	if len(imports) != 1 || imports[0] != "../utils" {
		t.Errorf("expected [../utils], got %v", imports)
	}
}

func TestParseFile_ExportFrom(t *testing.T) {
	src := `export { foo } from './shared'`
	imports := ParseFile(src, "index.ts")
	if len(imports) != 1 || imports[0] != "./shared" {
		t.Errorf("expected [./shared], got %v", imports)
	}
}

func TestParseFile_MultipleImports(t *testing.T) {
	src := `import a from './a'
import b from './b'
import c from './c'`
	imports := ParseFile(src, "main.ts")
	if len(imports) != 3 {
		t.Fatalf("expected 3 imports, got %d: %v", len(imports), imports)
	}
}

func TestParseFile_SkipsNodeModules(t *testing.T) {
	src := `import React from 'react'
import { useState } from 'react'
import foo from './local'`
	imports := ParseFile(src, "app.tsx")
	// 'react' imports are not relative so resolver will drop them,
	// but ParseFile itself should still return them as raw strings.
	if len(imports) != 3 {
		t.Fatalf("expected 3 raw imports, got %d: %v", len(imports), imports)
	}
}

func TestParseFile_UnsupportedLanguage(t *testing.T) {
	// .go is not in knownExts but ParseFile should not panic; grammar may be unavailable
	imports := ParseFile("package main", "main.go")
	// No TS/JS import nodes — result should be empty (nil or empty slice)
	if len(imports) != 0 {
		t.Errorf("expected no imports for Go file, got %v", imports)
	}
}

func TestParseFile_EmptyFile(t *testing.T) {
	imports := ParseFile("", "empty.ts")
	if len(imports) != 0 {
		t.Errorf("expected no imports for empty file, got %v", imports)
	}
}

// --- resolve tests ---

func TestResolve_DirectExtension(t *testing.T) {
	paths := map[string]bool{"src/utils.ts": true}
	got := resolve("src/app.ts", "./utils.ts", paths)
	if got != "src/utils.ts" {
		t.Errorf("got %q", got)
	}
}

func TestResolve_AppendExtension(t *testing.T) {
	paths := map[string]bool{"src/utils.tsx": true}
	got := resolve("src/app.ts", "./utils", paths)
	if got != "src/utils.tsx" {
		t.Errorf("got %q", got)
	}
}

func TestResolve_IndexFallback(t *testing.T) {
	paths := map[string]bool{"src/utils/index.ts": true}
	got := resolve("src/app.ts", "./utils", paths)
	if got != "src/utils/index.ts" {
		t.Errorf("got %q", got)
	}
}

func TestResolve_ParentDirectory(t *testing.T) {
	paths := map[string]bool{"lib/helper.ts": true}
	got := resolve("src/app.ts", "../lib/helper", paths)
	if got != "lib/helper.ts" {
		t.Errorf("got %q", got)
	}
}

func TestResolve_NonRelativeReturnsEmpty(t *testing.T) {
	paths := map[string]bool{"node_modules/react/index.js": true}
	got := resolve("src/app.ts", "react", paths)
	if got != "" {
		t.Errorf("expected empty for non-relative import, got %q", got)
	}
}

func TestResolve_UnresolvableReturnsEmpty(t *testing.T) {
	paths := map[string]bool{}
	got := resolve("src/app.ts", "./missing", paths)
	if got != "" {
		t.Errorf("expected empty for missing path, got %q", got)
	}
}

func TestResolve_ExtensionPrecedenceOrder(t *testing.T) {
	// .ts comes before .tsx in knownExts — .ts should win
	paths := map[string]bool{
		"src/comp.ts":  true,
		"src/comp.tsx": true,
	}
	got := resolve("src/app.ts", "./comp", paths)
	if got != "src/comp.ts" {
		t.Errorf("expected src/comp.ts, got %q", got)
	}
}

// --- Analyze tests ---

func TestAnalyze_NodeCreatedForEveryFile(t *testing.T) {
	files := []fetcher.FileData{
		{Path: "src/app.ts", Type: "blob", SHA: "sha1"},
		{Path: "src/utils.ts", Type: "blob", SHA: "sha2"},
		{Path: "README.md", Type: "blob", SHA: "sha3"},
		{Path: "src", Type: "tree", SHA: "sha4"},
	}
	result := Analyze(files)
	if len(result.Nodes) != 4 {
		t.Errorf("expected 4 nodes, got %d", len(result.Nodes))
	}
}

func TestAnalyze_NodeType_DirectoryForTreeFileForBlob(t *testing.T) {
	files := []fetcher.FileData{
		{Path: "src", Type: "tree", SHA: "sha1"},
		{Path: "src/app.ts", Type: "blob", SHA: "sha2"},
	}
	result := Analyze(files)

	dir := nodeByPath(t, result.Nodes, "src")
	if dir.Type != "directory" {
		t.Errorf("tree node Type: got %q, want %q", dir.Type, "directory")
	}

	file := nodeByPath(t, result.Nodes, "src/app.ts")
	if file.Type != "file" {
		t.Errorf("blob node Type: got %q, want %q", file.Type, "file")
	}
}

func TestAnalyze_NodeFields(t *testing.T) {
	files := []fetcher.FileData{
		{Path: "src/app.ts", Type: "blob", SHA: "abc123"},
	}
	result := Analyze(files)
	if len(result.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(result.Nodes))
	}
	n := result.Nodes[0]
	if n.Name != "app.ts" {
		t.Errorf("Name: got %q, want %q", n.Name, "app.ts")
	}
	if n.SHA != "abc123" {
		t.Errorf("SHA: got %q, want %q", n.SHA, "abc123")
	}
	if n.Type != "file" {
		t.Errorf("Type: got %q, want %q", n.Type, "file")
	}
	if n.Path != "src/app.ts" {
		t.Errorf("Path: got %q, want %q", n.Path, "src/app.ts")
	}
}

func TestAnalyze_AllNodesHaveNonEmptyPath(t *testing.T) {
	files := []fetcher.FileData{
		{Path: "src", Type: "tree", SHA: "sha1"},
		{Path: "src/app.ts", Type: "blob", SHA: "sha2"},
		{Path: "README.md", Type: "blob", SHA: "sha3"},
	}
	result := Analyze(files)
	for _, n := range result.Nodes {
		if n.Path == "" {
			t.Errorf("node %q has empty Path", n.Name)
		}
	}
}

func TestAnalyze_EdgeResolvedImport(t *testing.T) {
	files := []fetcher.FileData{
		{Path: "src/app.ts", Type: "blob", SHA: "sha1", RawFileData: `import foo from './utils'`},
		{Path: "src/utils.ts", Type: "blob", SHA: "sha2"},
	}
	result := Analyze(files)
	if len(result.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d: %v", len(result.Edges), result.Edges)
	}

	source := nodeByPath(t, result.Nodes, "src/app.ts")
	target := nodeByPath(t, result.Nodes, "src/utils.ts")

	e := result.Edges[0]
	// Dependency edges must reference node UUIDs, not paths.
	if e.Source == "src/app.ts" || e.Target == "src/utils.ts" {
		t.Errorf("dependency edge uses paths instead of UUIDs: %+v", e)
	}
	if e.Source != source.UUID {
		t.Errorf("Source: got %q, want %q", e.Source, source.UUID)
	}
	if e.Target != target.UUID {
		t.Errorf("Target: got %q, want %q", e.Target, target.UUID)
	}
}

func TestAnalyze_UnresolvableImportProducesNoEdge(t *testing.T) {
	files := []fetcher.FileData{
		{Path: "src/app.ts", Type: "blob", SHA: "sha1", RawFileData: `import foo from './missing'`},
	}
	result := Analyze(files)
	if len(result.Edges) != 0 {
		t.Errorf("expected 0 edges, got %d", len(result.Edges))
	}
}

func TestAnalyze_NonRelativeImportProducesNoEdge(t *testing.T) {
	files := []fetcher.FileData{
		{Path: "src/app.ts", Type: "blob", SHA: "sha1", RawFileData: `import React from 'react'`},
	}
	result := Analyze(files)
	if len(result.Edges) != 0 {
		t.Errorf("expected 0 edges for node_modules import, got %d", len(result.Edges))
	}
}

func TestAnalyze_NonBlobSkipsParser(t *testing.T) {
	// tree entries get a node but no edges
	files := []fetcher.FileData{
		{Path: "src", Type: "tree", SHA: "sha1"},
	}
	result := Analyze(files)
	if len(result.Nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(result.Nodes))
	}
	if len(result.Edges) != 0 {
		t.Errorf("expected 0 edges, got %d", len(result.Edges))
	}
}

func TestAnalyze_UnsupportedExtensionSkipsParser(t *testing.T) {
	// .go blob gets a node but parser is not called, so no edges
	files := []fetcher.FileData{
		{Path: "main.go", Type: "blob", SHA: "sha1", RawFileData: `import "./something"`},
	}
	result := Analyze(files)
	if len(result.Nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(result.Nodes))
	}
	if len(result.Edges) != 0 {
		t.Errorf("expected 0 edges for .go file, got %d", len(result.Edges))
	}
}

func TestAnalyze_EmptyInput(t *testing.T) {
	result := Analyze(nil)
	if len(result.Nodes) != 0 || len(result.Edges) != 0 {
		t.Errorf("expected empty result, got nodes=%d edges=%d", len(result.Nodes), len(result.Edges))
	}
}

func TestAnalyze_DependencyEdgeType(t *testing.T) {
	files := []fetcher.FileData{
		{Path: "src/app.ts", Type: "blob", SHA: "sha1", RawFileData: `import foo from './utils'`},
		{Path: "src/utils.ts", Type: "blob", SHA: "sha2"},
	}
	result := Analyze(files)

	source := nodeByPath(t, result.Nodes, "src/app.ts")
	target := nodeByPath(t, result.Nodes, "src/utils.ts")

	for _, e := range result.Edges {
		if e.Source == source.UUID && e.Target == target.UUID {
			if e.Type != graph.EdgeTypeDependency {
				t.Errorf("dependency edge type: got %q, want %q", e.Type, graph.EdgeTypeDependency)
			}
			return
		}
	}
	t.Fatal("dependency edge not found")
}

// nodeByPath returns the node with the given path, failing the test if absent.
func nodeByPath(t *testing.T, nodes []Node, p string) Node {
	t.Helper()
	for _, n := range nodes {
		if n.Path == p {
			return n
		}
	}
	t.Fatalf("no node found for path %q", p)
	return Node{}
}

func TestAnalyze_ContainsEdgeEmittedForFileInDir(t *testing.T) {
	files := []fetcher.FileData{
		{Path: "src", Type: "tree", SHA: "sha1"},
		{Path: "src/app.ts", Type: "blob", SHA: "sha2"},
	}
	result := Analyze(files)

	parent := nodeByPath(t, result.Nodes, "src")
	child := nodeByPath(t, result.Nodes, "src/app.ts")

	for _, e := range result.Edges {
		if e.Type != graph.EdgeTypeContains {
			continue
		}
		// Contains edges must reference node UUIDs, not paths.
		if e.Source == "src" || e.Target == "src/app.ts" {
			t.Errorf("contains edge uses paths instead of UUIDs: %+v", e)
		}
		if e.Source == parent.UUID && e.Target == child.UUID {
			return
		}
	}
	t.Fatal("contains edge parent.UUID→child.UUID not found")
}

func TestAnalyze_ContainsEdgeNotEmittedForRootLevelFile(t *testing.T) {
	files := []fetcher.FileData{
		{Path: "README.md", Type: "blob", SHA: "sha1"},
	}
	result := Analyze(files)
	for _, e := range result.Edges {
		if e.Type == graph.EdgeTypeContains {
			t.Errorf("unexpected contains edge for root-level file: %+v", e)
		}
	}
}

func TestAnalyze_ContainsEdgeNotEmittedWhenParentAbsent(t *testing.T) {
	// src/app.ts present but "src" tree node is not in the file list
	files := []fetcher.FileData{
		{Path: "src/app.ts", Type: "blob", SHA: "sha1"},
	}
	result := Analyze(files)
	for _, e := range result.Edges {
		if e.Type == graph.EdgeTypeContains {
			t.Errorf("unexpected contains edge when parent dir not in file list: %+v", e)
		}
	}
}

func TestAnalyze_ContainsEdgesForMultipleChildren(t *testing.T) {
	files := []fetcher.FileData{
		{Path: "src", Type: "tree", SHA: "sha1"},
		{Path: "src/a.ts", Type: "blob", SHA: "sha2"},
		{Path: "src/b.ts", Type: "blob", SHA: "sha3"},
	}
	result := Analyze(files)

	parent := nodeByPath(t, result.Nodes, "src")
	childA := nodeByPath(t, result.Nodes, "src/a.ts")
	childB := nodeByPath(t, result.Nodes, "src/b.ts")

	found := map[string]bool{}
	for _, e := range result.Edges {
		if e.Type == graph.EdgeTypeContains {
			if e.Source != parent.UUID {
				t.Errorf("contains edge source: got %q, want parent UUID %q", e.Source, parent.UUID)
			}
			found[e.Target] = true
		}
	}
	if !found[childA.UUID] {
		t.Error("missing contains edge for src/a.ts")
	}
	if !found[childB.UUID] {
		t.Error("missing contains edge for src/b.ts")
	}
}

func TestAnalyze_MultipleEdgesFromOneFile(t *testing.T) {
	files := []fetcher.FileData{
		{
			Path: "src/app.ts", Type: "blob", SHA: "sha1",
			RawFileData: "import a from './a'\nimport b from './b'",
		},
		{Path: "src/a.ts", Type: "blob", SHA: "sha2"},
		{Path: "src/b.ts", Type: "blob", SHA: "sha3"},
	}
	result := Analyze(files)
	if len(result.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d: %v", len(result.Edges), result.Edges)
	}
}

func TestAnalyze_DuplicateImportsCollapseToSingleEdge(t *testing.T) {
	// Two separate import statements resolving to the same target file
	// must collapse into a single dependency edge.
	files := []fetcher.FileData{
		{
			Path: "src/app.ts", Type: "blob", SHA: "sha1",
			RawFileData: "import { a } from './utils'\nimport { b } from './utils'",
		},
		{Path: "src/utils.ts", Type: "blob", SHA: "sha2"},
	}
	result := Analyze(files)

	source := nodeByPath(t, result.Nodes, "src/app.ts")
	target := nodeByPath(t, result.Nodes, "src/utils.ts")

	count := 0
	for _, e := range result.Edges {
		if e.Type == graph.EdgeTypeDependency && e.Source == source.UUID && e.Target == target.UUID {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 deduplicated dependency edge, got %d", count)
	}
}

func TestAnalyze_AllEdgesUseUUIDsNotPaths(t *testing.T) {
	files := []fetcher.FileData{
		{Path: "src", Type: "tree", SHA: "sha1"},
		{Path: "src/app.ts", Type: "blob", SHA: "sha2", RawFileData: `import foo from './utils'`},
		{Path: "src/utils.ts", Type: "blob", SHA: "sha3"},
	}
	result := Analyze(files)

	paths := make(map[string]bool, len(files))
	uuids := make(map[string]bool, len(result.Nodes))
	for _, f := range files {
		paths[f.Path] = true
	}
	for _, n := range result.Nodes {
		uuids[n.UUID] = true
	}

	if len(result.Edges) == 0 {
		t.Fatal("expected at least one edge for this fixture")
	}
	for _, e := range result.Edges {
		if paths[e.Source] || paths[e.Target] {
			t.Errorf("edge uses a raw path instead of a UUID: %+v", e)
		}
		if !uuids[e.Source] || !uuids[e.Target] {
			t.Errorf("edge Source/Target does not match any node UUID: %+v", e)
		}
	}
}

func TestAnalyze_NoDuplicateEdgesOverall(t *testing.T) {
	files := []fetcher.FileData{
		{Path: "src", Type: "tree", SHA: "sha1"},
		{
			Path: "src/app.ts", Type: "blob", SHA: "sha2",
			RawFileData: "import { a } from './utils'\nimport { b } from './utils'",
		},
		{Path: "src/utils.ts", Type: "blob", SHA: "sha3"},
	}
	result := Analyze(files)

	seen := make(map[[2]string]bool)
	for _, e := range result.Edges {
		key := [2]string{e.Source, e.Target}
		if seen[key] {
			t.Errorf("duplicate edge found: %+v", e)
		}
		seen[key] = true
	}
}
