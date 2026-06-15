import { create } from 'zustand';
import { Edge, NodeChange, EdgeChange, applyNodeChanges, applyEdgeChanges, OnNodesChange, OnEdgesChange } from '@xyflow/react';
import { AppNode } from './components/Nodes';
import { fetchGraphData } from './services/api';

type GraphState = {
  nodes: AppNode[];
  edges: Edge[];
  selectedNode: AppNode | null;
  togglesidebar: boolean;
  
  // ReactFlow native handlers
  onNodesChange: OnNodesChange<AppNode>;
  onEdgesChange: OnEdgesChange<Edge>;
  setNodes: (nodes: AppNode[]) => void;
  setEdges: (edges: Edge[]) => void;
  
  // Data loading
  loadGraphData: () => Promise<void>;
  
  // UI Actions
  setSelectedNode: (node: AppNode | null) => void;
  setSidebarOpen: (open: boolean) => void;
  saveNodeNotes: (nodeId: string, notes: string[]) => void;
  closeSidebarAndUnselect: () => void;
};

export const useStore = create<GraphState>((set, get) => ({
  nodes: [],
  edges: [],
  selectedNode: null,
  togglesidebar: false,

  onNodesChange: (changes: NodeChange<AppNode>[]) => {
    set({
      nodes: applyNodeChanges(changes, get().nodes) as AppNode[],
    });
  },

  onEdgesChange: (changes: EdgeChange<Edge>[]) => {
    set({
      edges: applyEdgeChanges(changes, get().edges),
    });
  },

  setNodes: (nodes) => set({ nodes }),
  setEdges: (edges) => set({ edges }),

  loadGraphData: async () => {
    const data = await fetchGraphData();
    set({ nodes: data.nodes as AppNode[], edges: data.edges });
  },

  setSelectedNode: (node) => set({ selectedNode: node }),
  setSidebarOpen: (open) => set({ togglesidebar: open }),

  saveNodeNotes: (nodeId: string, notes: string[]) => {
    set({
      nodes: get().nodes.map((node) => {
        if (node.id === nodeId) {
          const updatedNode = { ...node, data: { ...node.data, Notes: notes } };
          // Keep sidebar in sync if this is the active node changing
          if (get().selectedNode?.id === nodeId) {
              set({ selectedNode: updatedNode as AppNode });
          }
          return updatedNode as AppNode;
        }
        return node;
      })
    });
  },

  closeSidebarAndUnselect: () => {
    set({
      togglesidebar: false,
      selectedNode: null,
      nodes: get().nodes.map((node) => ({ ...node, selected: false }))
    });
  }
}));
