# CodeAtlas 🗺️

An interactive dependency-graph visualization tool that analyzes remote GitHub and GitLab
repositories — without cloning them — and renders their import structure as a navigable,
editable map in the browser.

---

## 📌 Project Overview

**CodeAtlas** bridges the gap between complex source code and architectural comprehension.
By fetching a repository's file tree directly through the GitHub/GitLab APIs and parsing
every JavaScript/TypeScript file's import and export statements via tree-sitter, CodeAtlas
builds a typed dependency graph and renders it as an interactive, persistent visual map —
one that remembers your annotations and layout across re-analyses.

---

## 🕹️ How It Works: User Guide

CodeAtlas turns a remote repository into a live, explorable map in five steps — sign in,
pick a repo, generate, navigate, and inspect. Here's what each one actually does and what
you'll see.

```
┌───────────┐    ┌───────────────┐    ┌──────────────┐    ┌─────────────┐    ┌────────────┐
│ 1.Sign In │ ─► │ 2. Select a   │ ─► │ 3. Generate  │ ─► │ 4. Navigate │ ─► │ 5. Inspect │
│           │    │    Repository │    │ Map & Layout │    │  & Filter   │    │Dependencies│
└───────────┘    └───────────────┘    └──────────────┘    └─────────────┘    └────────────┘
```

### Step 1 — Sign In

Sign in with **GitHub** or **GitLab** to grant CodeAtlas read access to your repositories —
no cloning, no local setup on your end.

![Sign in](screenshots/SS1.png)
![Authorize](screenshots/SS2.jpeg)
### Step 2 — Select a Repository

Once signed in, pick a repository and branch from the list. There's no "paste a local
folder path" option — CodeAtlas always works against the real, current state of the repo
directly from GitHub/GitLab.

![Select a repository](screenshots/SS3.png)

### Step 3 — Automatic Graph Generation

Once you kick off analysis, CodeAtlas does the heavy lifting for you: it scans every source
file in the repository, figures out how they import and depend on one another, and lays the
whole thing out automatically as a clean, readable map — no manual arranging required.

If you've analyzed this repository before, your previous work isn't lost. CodeAtlas
recognizes files it's seen before and carries forward your existing layout and any
descriptions you've added, only adjusting what's genuinely new or changed.

![Generated dependency graph](screenshots/SS4.png)

### Step 4 — Navigate & Filter the Canvas

Once the map renders, it's yours to explore:

* **Pan & zoom** — click-drag to pan across large codebases, scroll to zoom in on a cluster
  or out for the full picture.
* **Reposition nodes** — drag any file/module node to a spot that makes more sense to you;
  your layout is saved automatically, so it's there next time you open this graph.
* **Collapse or expand groups** — toggle a directory cluster between a single high-level
  node and its full set of individual files, so you can zoom out to see architecture or
  zoom in to see specifics without losing context.


### Step 5 — Inspect Dependencies & Metadata

Select any node to see how it fits into the rest of the codebase:

* Its **connections highlight**, showing exactly what it depends on and what depends on
  it — useful for spotting an unexpectedly central file or an unwanted circular dependency
  at a glance.
* The **side panel** shows that file's path and any description you've written for it. You
  can add or edit that description directly, and it's remembered the next time the graph
  is regenerated.
* When you're happy with the map, **commit it back to the repository** so your annotations
  and layout become part of the project's history, not just something sitting in CodeAtlas.

![Inspecting a node's dependencies](screenshots/SS5.png)

---

## 🛠️ Tech Stack

**Backend:** Go, [Chi](https://github.com/go-chi/chi) router, tree-sitter (via
`gotreesitter`), JWT (`golang-jwt/jwt`), PostgreSQL (`lib/pq`), `golang-migrate` for schema
migrations, GitHub OAuth + GitLab OAuth

**Frontend:** React, TypeScript, Vite, React Flow (`@xyflow/react`), `dagre` (hierarchical
graph layout), Tailwind CSS

**Persistence:** PostgreSQL — three migrated tables (`users`, `oauth_tokens`,
`graph_registry`). If `CODEATLAS_DB_URL` isn't set, the server falls back to in-memory mock
repositories automatically, so local development works without a database.

**Not yet implemented:** CLI tooling (`cmd/analyze` currently exists only as an empty stub),
VS Code extension, offline/local-repository analysis.

---

## 🚀 Getting Started Locally

### Prerequisites
* **Go** 1.25+
* **Node.js** 18+ and **npm**
* A **PostgreSQL** database (optional for local dev — see below)
* GitHub and/or GitLab **OAuth App** credentials (client ID + secret), for sign-in

### 1. Configure environment variables

The backend reads its configuration directly from the environment (`.env` is loaded
automatically via `godotenv` if present in `backend/`). Required for OAuth and sessions:

```bash
CODEATLAS_JWT_SECRET=<random-secret>
CODEATLAS_ENCRYPTION_KEY=<random-key>          # encrypts stored OAuth tokens at rest
CODEATLAS_GITHUB_CLIENT_ID=<...>
CODEATLAS_GITHUB_CLIENT_SECRET=<...>
CODEATLAS_GITLAB_CLIENT_ID=<...>
CODEATLAS_GITLAB_CLIENT_SECRET=<...>
CODEATLAS_APP_BASE_URL=http://localhost:8080   # used to build OAuth redirect URIs
CODEATLAS_FRONTEND_URL=http://localhost:5173   # defaults to this if unset; used for CORS
CODEATLAS_PORT=8080                            # defaults to 8080 if unset

# Optional — omit to run against in-memory mock repositories instead of Postgres
CODEATLAS_DB_URL=postgres://user:pass@localhost:5432/codeatlas?sslmode=disable
```

### 2. Run the Backend Server

```bash
cd backend
go mod download
go run ./cmd/server
```

If `CODEATLAS_DB_URL` is set, the server connects to Postgres and automatically applies any
pending migrations (via `golang-migrate`) on startup — no separate migration step needed.
To run migrations manually instead:

```bash
make migrate
```

### 3. Run the Frontend

```bash
cd frontend
npm install
npm run dev
```

The dev server starts on `http://localhost:5173` and talks to the backend at
`http://localhost:8080` by default (override with `VITE_API_BASE_URL`).
