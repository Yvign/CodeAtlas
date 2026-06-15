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
