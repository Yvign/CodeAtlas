import { Node, Edge } from "@xyflow/react";
import { NodeData } from "../components/Nodes";

/**
 * =========================================================================
 * BACKEND IMPLEMENTATION GUIDE FOR graphapi.go
 * =========================================================================
 * 
 * Your Go backend should expose an endpoint (e.g., GET /api/graph) that 
 * returns a JSON response matching the `GraphDataResponse` interface below.
 * 
 * Example JSON Response:
 * {
 *   "nodes": [
 *     {
 *       "id": "root_folder",
 *       "type": "folder",
 *       "position": { "x": 250, "y": 0 },
 *       "data": { "label": "src", "Nodedesc": "Main source directory" }
 *     },
 *     {
 *       "id": "app_file",
 *       "type": "file",
 *       "position": { "x": 100, "y": 150 },
 *       "data": { "label": "App.tsx", "Nodedesc": "React entry point" }
 *     }
 *   ],
 *   "edges": [
 *     {
 *       "id": "e_root_app",
 *       "source": "root_folder",
 *       "target": "app_file",
 *       "type": "myEdge"
 *     }
 *   ]
 * }
 * =========================================================================
 */

export interface GraphDataResponse {
    nodes: Node<NodeData>[];
    edges: Edge[];
}

export interface MeResponse {
    userID: string;
    provider: string;
    username: string;
}

export interface CreateGraphResponse {
    graphId: string;
    status: string;
}

export interface GraphEntry {
    id: string;
    provider: string;
    owner: string;
    repoName: string;
    branch: string;
    status: string;
}

interface GraphSummaryDTO {
    id: string;
    provider: string;
    owner: string;
    repo: string;
    branch: string;
    status: string;
    errorMsg?: string;
    createdAt: string;
}

export interface NodeRecord {
    uuid: string;
    type: string;
    name: string;
    sha: string;
    path: string;
    description: string;
    pos: { x: number; y: number };
}

export interface EdgeRecord {
    source: string;
    target: string;
    type: string;
}

export interface GraphRecord {
    version: string;
    generatedAt: string;
    repoOwner: string;
    repoName: string;
    branch: string;
    nodes: NodeRecord[];
    edges: EdgeRecord[];
}

export type GraphResponse =
    | { status: 'processing' }
    | { status: 'failed'; error: string }
    | { status: 'ready'; graph: GraphRecord };

export const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080';

// Calls GET /auth/me. Returns the authenticated user, or null on 401.
export async function getMe(): Promise<MeResponse | null> {
    const response = await fetch(`${API_BASE_URL}/auth/me`, {
        credentials: 'include',
    });

    if (response.status === 401) {
        return null;
    }

    return response.json();
}

// Calls POST /graphs to kick off graph generation for a repo.
export async function createGraph(provider: string, owner: string, repo: string, branch: string, commit: boolean): Promise<CreateGraphResponse> {
    const response = await fetch(`${API_BASE_URL}/graphs`, {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ provider, owner, repo, branch, commit }),
    });

    if (!response.ok) {
        throw new Error('Failed to create graph');
    }

    return response.json();
}

// Calls GET /graphs to list the current user's previously processed graphs.
export async function listGraphs(): Promise<GraphEntry[]> {
    const response = await fetch(`${API_BASE_URL}/graphs`, {
        credentials: 'include',
    });

    if (!response.ok) {
        throw new Error('Failed to list graphs');
    }

    const data: { graphs: GraphSummaryDTO[] } = await response.json();
    return data.graphs.map((g) => ({
        id: g.id,
        provider: g.provider,
        owner: g.owner,
        repoName: g.repo,
        branch: g.branch,
        status: g.status,
    }));
}

// Calls GET /graphs/:id to fetch a graph's current status/content.
export async function getGraph(id: string): Promise<GraphResponse> {
    const response = await fetch(`${API_BASE_URL}/graphs/${id}`, {
        credentials: 'include',
    });

    if (!response.ok) {
        throw new Error('Failed to fetch graph');
    }

    return response.json();
}

// Calls DELETE /graphs/:id to remove a graph entry.
export async function deleteGraph(id: string): Promise<void> {
    const response = await fetch(`${API_BASE_URL}/graphs/${id}`, {
        method: 'DELETE',
        credentials: 'include',
    });

    if (!response.ok) {
        throw new Error('Failed to delete graph');
    }
}

// ApiError carries the backend's `error.code` (e.g. "graph_not_committed") so
// callers can branch on specific failure reasons instead of just "it failed".
export class ApiError extends Error {
    code?: string;
    constructor(message: string, code?: string) {
        super(message);
        this.code = code;
    }
}

async function errorCodeFromResponse(response: Response): Promise<string | undefined> {
    try {
        const data = await response.json();
        return data?.error?.code;
    } catch {
        return undefined;
    }
}

// Calls PATCH /graphs/:graphId/nodes to persist a batch of pending description
// edits (possibly across many nodes) in a single commit.
export async function saveDescriptions(graphId: string, updates: { nodeId: string; description: string }[]): Promise<void> {
    const response = await fetch(`${API_BASE_URL}/graphs/${graphId}/nodes`, {
        method: 'PATCH',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(updates),
    });

    if (!response.ok) {
        throw new ApiError('Failed to save descriptions', await errorCodeFromResponse(response));
    }
}

// Calls PATCH /graphs/:graphId/layout to persist node positions.
export async function saveLayout(graphId: string, positions: { nodeId: string; x: number; y: number }[]): Promise<void> {
    const response = await fetch(`${API_BASE_URL}/graphs/${graphId}/layout`, {
        method: 'PATCH',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(positions),
    });

    if (!response.ok) {
        throw new ApiError('Failed to save layout', await errorCodeFromResponse(response));
    }
}

// Calls POST /graphs/:graphId/commit to commit an in-memory (not yet
// committed) graph to the repository as .codeatlas/graph.json.
export async function commitGraph(graphId: string): Promise<void> {
    const response = await fetch(`${API_BASE_URL}/graphs/${graphId}/commit`, {
        method: 'POST',
        credentials: 'include',
    });

    if (!response.ok) {
        throw new ApiError('Failed to commit graph', await errorCodeFromResponse(response));
    }
}

// Calls POST /auth/logout to clear the session cookie.
export async function logout(): Promise<void> {
    const response = await fetch(`${API_BASE_URL}/auth/logout`, {
        method: 'POST',
        credentials: 'include',
    });

    if (!response.ok) {
        throw new Error('Failed to logout');
    }
}

// Mock function to simulate fetching from your Go backend
export async function fetchGraphData(): Promise<GraphDataResponse> {
    // Simulating network delay
    await new Promise(resolve => setTimeout(resolve, 500));

    return {
        nodes: [
            {
                id: '1',
                type: 'folder',
                position: { x: 250, y: 50 },
                data: { label: 'backend', Nodedesc: 'Go backend source code' }
            },
            {
                id: '2',
                type: 'file',
                position: { x: 100, y: 200 },
                data: { label: 'graphapi.go', Nodedesc: 'Serves graph JSON to frontend' }
            },
            {
                id: '3',
                type: 'folder',
                position: { x: 400, y: 50 },
                data: { label: 'frontend', Nodedesc: 'React frontend source code' }
            },
            {
                id: '4',
                type: 'file',
                position: { x: 300, y: 200 },
                data: { label: 'App.tsx', Nodedesc: 'React application entry point' }
            },
            {
                id: '5',
                type: 'file',
                position: { x: 500, y: 200 },
                data: { label: 'index.html', Nodedesc: 'HTML Document' }
            }
        ],
        edges: [
            { id: 'e1-2', source: '1', target: '2', type: 'myEdge' },
            { id: 'e3-4', source: '3', target: '4', type: 'myEdge' },
            { id: 'e3-5', source: '3', target: '5', type: 'myEdge' }
        ]
    };
}
