import { create } from 'zustand';
import { Edge, NodeChange, EdgeChange, applyNodeChanges, applyEdgeChanges, OnNodesChange, OnEdgesChange } from '@xyflow/react';
import { AppNode } from './components/Nodes';
import { fetchGraphData, GraphRecord } from './services/api';

type User = {
  userID: string;
  provider: string;
  username: string;
};

type GraphState = {
  nodes: AppNode[];
  edges: Edge[];
  selectedNode: AppNode | null;
  togglesidebar: boolean;
  user: User | null;
  graphRecord: GraphRecord | null;
  visibleDependencyEdges: Set<string>;
  // Edited-but-not-yet-saved node descriptions, keyed by node uuid. Kept
  // globally (not per-sidebar-instance local state) so switching between
  // nodes doesn't lose an in-progress edit — it's only cleared on save,
  // per-node revert, or loading a different graph.
  pendingDescriptions: Record<string, string>;

  // Auth actions
  setUser: (user: User) => void;
  clearUser: () => void;
  setGraphRecord: (record: GraphRecord) => void;
  setVisibleDependencyEdges: (nodeId: string | null) => void;

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
  setPendingDescription: (nodeId: string, description: string) => void;
  revertPendingDescription: (nodeId: string) => void;
  clearPendingDescriptions: () => void;
  applySavedDescriptions: (updates: { nodeId: string; description: string }[]) => void;
  closeSidebarAndUnselect: () => void;
};

export const useStore = create<GraphState>((set, get) => ({
  nodes: [],
  edges: [],
  selectedNode: null,
  togglesidebar: false,
  user: null,
  graphRecord: null,
  visibleDependencyEdges: new Set(),
  pendingDescriptions: {},

  setUser: (user) => set({ user }),
  clearUser: () => set({ user: null }),
  setGraphRecord: (record) => set({ graphRecord: record }),

  setVisibleDependencyEdges: (nodeId) => {
    const currentEdges = get().edges;
    const visibleIds = nodeId
      ? new Set(
          currentEdges
            .filter((e) => e.type === 'myEdge' && (e.source === nodeId || e.target === nodeId))
            .map((e) => e.id)
        )
      : new Set<string>();

    set({
      visibleDependencyEdges: visibleIds,
      edges: currentEdges.map((e) =>
        e.type === 'myEdge' ? { ...e, hidden: !visibleIds.has(e.id) } : e
      ),
    });
  },

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

  setPendingDescription: (nodeId, description) => {
    const persisted = get().graphRecord?.nodes.find((n) => n.uuid === nodeId)?.description || "";
    const current = get().pendingDescriptions;
    if (description === persisted) {
      if (!(nodeId in current)) return;
      const next = { ...current };
      delete next[nodeId];
      set({ pendingDescriptions: next });
    } else {
      set({ pendingDescriptions: { ...current, [nodeId]: description } });
    }
  },

  revertPendingDescription: (nodeId) => {
    const current = get().pendingDescriptions;
    if (!(nodeId in current)) return;
    const next = { ...current };
    delete next[nodeId];
    set({ pendingDescriptions: next });
  },

  clearPendingDescriptions: () => set({ pendingDescriptions: {} }),

  applySavedDescriptions: (updates) => {
    const record = get().graphRecord;
    if (!record) return;
    const byId = new Map(updates.map((u) => [u.nodeId, u.description]));
    const nextPending = { ...get().pendingDescriptions };
    updates.forEach((u) => delete nextPending[u.nodeId]);
    set({
      graphRecord: {
        ...record,
        nodes: record.nodes.map((n) =>
          byId.has(n.uuid) ? { ...n, description: byId.get(n.uuid)! } : n
        ),
      },
      pendingDescriptions: nextPending,
    });
  },

  closeSidebarAndUnselect: () => {
    get().setVisibleDependencyEdges(null);
    set({
      togglesidebar: false,
      selectedNode: null,
      nodes: get().nodes.map((node) => ({ ...node, selected: false }))
    });
  }
}));
