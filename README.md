# CodeAtlas

> Visualize the import dependency graph of any GitHub or GitLab repository — without cloning it.

CodeAtlas analyzes remote repositories via the GitHub and GitLab APIs, parses JavaScript and TypeScript source files using tree-sitter AST queries, and renders an interactive dependency graph in the browser.

---

## What it does

Given a repository URL and branch, CodeAtlas:

1. Fetches the full file tree from GitHub or GitLab
2. Parses every `.ts`, `.tsx`, `.js`, and `.jsx` file for import and export statements
3. Resolves relative imports to their actual file targets using a three-pass resolution strategy
4. Builds a dependency graph with two edge types — `dependency` (import relationships) and `contains` (directory hierarchy)
5. Merges the result with any previously saved graph, preserving node positions and user annotations
6. Commits the graph as `.codeatlas/graph.json` back to the repository
7. Renders the graph as an interactive React Flow visualization in the browser

---

## Architecture

CodeAtlas is structured around three layers:

**Fetch** — The provider layer abstracts GitHub and GitLab APIs into a uniform interface, walking the repository tree and retrieving file contents in parallel batches.

**Analyze** — The core library parses each file's AST for import statements, resolves relative paths to their targets, and builds a typed dependency graph with directory hierarchy edges.

**Persist** — The graph is merged with any previously saved version to preserve user annotations and canvas positions, then committed back to the repository as `.codeatlas/graph.json`.

The backend is a pure Go library with no runtime dependency on any local Git tooling. All repository access goes through the provider APIs.

---

## Key Design Decisions

**Provider abstraction** — A `Provider` interface abstracts GitHub and GitLab API differences (tree structure, pagination, blob format, rate limit headers) so the core pipeline is provider-agnostic.

**Parallel batch fetching** — Blob contents are fetched concurrently in batches of 20, achieving up to 7x faster graph generation compared to sequential fetching on repos of 500+ files.

**Two-pass GitHub tree walk** — GitHub truncates recursive tree responses at 100,000 entries. When truncation is detected, CodeAtlas falls back to a shallow fetch and re-enqueues each subtree individually via BFS.

**Three-pass import resolution** — Relative imports are resolved by: (1) direct path match, (2) extension append (`.ts → .tsx → .js → .jsx`), (3) index file fallback. Non-relative (npm) imports are silently dropped.

**UUID-preserving merge** — On re-analysis, existing node UUIDs are matched by SHA (exact identity) → path (location) → name (filename only, if unambiguous). Matched nodes carry forward user descriptions, notes, and canvas positions.

**Conflict-safe commits** — On a commit conflict, CodeAtlas fetches the winning version, merges user metadata from the pending graph onto the latest structure, and retries up to three times.

---

## Tech Stack

**Backend:** Go, Chi, JWT, tree-sitter, GitHub API, GitLab API

**Frontend:** React, TypeScript, React Flow, dagre

**Planned:** PostgreSQL persistence, CLI tooling, VSCode extension, local/offline repo support
