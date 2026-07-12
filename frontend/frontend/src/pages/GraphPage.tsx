import { useEffect, useCallback, useState, useRef } from "react"
import { useParams, useNavigate } from "react-router-dom"
import { ReactFlow, Background, Controls, Edge, MarkerType, OnSelectionChangeParams } from "@xyflow/react"
import { useShallow } from 'zustand/react/shallow'
import { nodeTypes, AppNode } from "../components/Nodes"
import { edgeTypes } from "../components/Edges"
import SideBar from "../components/sideBar"
import { useStore } from "../store"
import { getGraph, saveLayout, saveDescriptions, commitGraph, createGraph, listGraphs, ApiError, NodeRecord, EdgeRecord } from "../services/api"
import { applyDagreLayout } from "../utils/layout"

function toAppNode(node: NodeRecord, position: { x: number; y: number }): AppNode {
    return {
        id: node.uuid,
        type: node.type === 'directory' ? 'folder' : 'file',
        position,
        data: {
            label: node.name,
            Nodedesc: node.description,
        },
    } as AppNode
}

// Contains edges (folder → file/folder hierarchy) are always rendered.
function toContainsFlowEdge(edge: EdgeRecord): Edge {
    return {
        id: `${edge.source}-${edge.target}`,
        source: edge.source,
        target: edge.target,
        type: 'containsEdge',
        markerEnd: { type: MarkerType.ArrowClosed, color: '#4b5563' },
    }
}

// Dependency edges start hidden — they're only revealed for the edges
// connected to the currently selected file node (see setVisibleDependencyEdges).
function toDependencyFlowEdge(edge: EdgeRecord): Edge {
    return {
        id: `${edge.source}-${edge.target}`,
        source: edge.source,
        target: edge.target,
        type: 'myEdge',
        hidden: true,
    }
}

type LoadState =
    | { status: 'processing' }
    | { status: 'failed'; error: string }
    | { status: 'ready' }

function GraphPage() {
    const { id } = useParams<{ id: string }>()
    const navigate = useNavigate()
    const [loadState, setLoadState] = useState<LoadState>({ status: 'processing' })

    // Repo details for "Try again" on failure. The failed-graph response
    // itself carries no repo info, so we look it up from the user's graph
    // list (best-effort — if that also fails, Try again is just disabled).
    const [retryDetails, setRetryDetails] = useState<{ provider: string; owner: string; repo: string; branch: string } | null>(null)
    const [isRetrying, setIsRetrying] = useState(false)
    const [retryError, setRetryError] = useState<string | null>(null)

    // Layout save/discard state. Positions are only persisted when the user
    // explicitly presses Save; lastSavedPositionsRef is a snapshot to revert
    // to on Discard and never needs to trigger a re-render itself.
    const [isLayoutDirty, setIsLayoutDirty] = useState(false)
    const [isSavingLayout, setIsSavingLayout] = useState(false)
    const [layoutError, setLayoutError] = useState<string | null>(null)
    const [layoutNeedsCommit, setLayoutNeedsCommit] = useState(false)
    const [isCommittingLayout, setIsCommittingLayout] = useState(false)
    const [showLayoutSaved, setShowLayoutSaved] = useState(false)
    const lastSavedPositionsRef = useRef<Record<string, { x: number; y: number }>>({})
    const layoutSavedTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)

    // Flashes the "Layout saved" confirmation for a couple seconds, then hides it.
    const flashLayoutSaved = useCallback(() => {
        if (layoutSavedTimeoutRef.current) clearTimeout(layoutSavedTimeoutRef.current)
        setShowLayoutSaved(true)
        layoutSavedTimeoutRef.current = setTimeout(() => setShowLayoutSaved(false), 2000)
    }, [])

    // Clear any pending "Layout saved" timeout on unmount.
    useEffect(() => {
        return () => {
            if (layoutSavedTimeoutRef.current) clearTimeout(layoutSavedTimeoutRef.current)
        }
    }, [])

    // Description save/discard state. Edits themselves live in the store's
    // pendingDescriptions (so the sidebar can edit any node without losing
    // other nodes' in-progress edits); this is just the save-flow UI state,
    // independent from the layout save flow above (separate commits).
    const [isSavingDescriptions, setIsSavingDescriptions] = useState(false)
    const [descriptionsError, setDescriptionsError] = useState<string | null>(null)
    const [descriptionsNeedCommit, setDescriptionsNeedCommit] = useState(false)
    const [isCommittingDescriptions, setIsCommittingDescriptions] = useState(false)
    const [showDescriptionsSaved, setShowDescriptionsSaved] = useState(false)
    const descriptionsSavedTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)

    // Flashes the "Descriptions saved" confirmation for a couple seconds, then hides it.
    const flashDescriptionsSaved = useCallback(() => {
        if (descriptionsSavedTimeoutRef.current) clearTimeout(descriptionsSavedTimeoutRef.current)
        setShowDescriptionsSaved(true)
        descriptionsSavedTimeoutRef.current = setTimeout(() => setShowDescriptionsSaved(false), 2000)
    }, [])

    // Clear any pending "Descriptions saved" timeout on unmount.
    useEffect(() => {
        return () => {
            if (descriptionsSavedTimeoutRef.current) clearTimeout(descriptionsSavedTimeoutRef.current)
        }
    }, [])

    // We use shallow rendering to strictly prevent unnecessary React re-renders, maxing out performance.
    const {
        nodes, edges, onNodesChange, onEdgesChange,
        setNodes, setEdges, setGraphRecord,
        selectedNode, setSelectedNode, setVisibleDependencyEdges,
        togglesidebar, setSidebarOpen,
        pendingDescriptions, applySavedDescriptions, clearPendingDescriptions,
        closeSidebarAndUnselect
    } = useStore(useShallow((state) => ({
        nodes: state.nodes,
        edges: state.edges,
        onNodesChange: state.onNodesChange,
        onEdgesChange: state.onEdgesChange,
        setNodes: state.setNodes,
        setEdges: state.setEdges,
        setGraphRecord: state.setGraphRecord,
        selectedNode: state.selectedNode,
        setSelectedNode: state.setSelectedNode,
        setVisibleDependencyEdges: state.setVisibleDependencyEdges,
        togglesidebar: state.togglesidebar,
        setSidebarOpen: state.setSidebarOpen,
        pendingDescriptions: state.pendingDescriptions,
        applySavedDescriptions: state.applySavedDescriptions,
        clearPendingDescriptions: state.clearPendingDescriptions,
        closeSidebarAndUnselect: state.closeSidebarAndUnselect
    })))

    const descriptionsDirty = Object.keys(pendingDescriptions).length > 0

    // Fetch the graph from the backend, polling while it's still processing.
    useEffect(() => {
        if (!id) return

        let cancelled = false
        let intervalId: ReturnType<typeof setInterval> | undefined

        const stopPolling = () => {
            if (intervalId) {
                clearInterval(intervalId)
                intervalId = undefined
            }
        }

        // Best-effort lookup of this graph's repo details for "Try again" — the
        // failed-graph response itself carries no provider/owner/repo/branch.
        const fetchRetryDetails = async () => {
            try {
                const graphs = await listGraphs()
                if (cancelled) return
                const entry = graphs.find((g) => g.id === id)
                if (entry) {
                    setRetryDetails({ provider: entry.provider, owner: entry.owner, repo: entry.repoName, branch: entry.branch })
                }
            } catch {
                // Try again will just stay disabled if this fails too.
            }
        }

        const load = async () => {
            try {
                const result = await getGraph(id)
                if (cancelled) return

                if (result.status === 'processing') {
                    setLoadState({ status: 'processing' })
                    return
                }

                stopPolling()

                if (result.status === 'failed') {
                    setLoadState({ status: 'failed', error: result.error })
                    fetchRetryDetails()
                    return
                }

                let flowNodes = result.graph.nodes.map((node) =>
                    toAppNode(node, { x: node.pos.x, y: node.pos.y })
                )

                // Must match graph.EdgeTypeDependency / graph.EdgeTypeContains in
                // backend/internal/core/graph/graph.go exactly ("dependency" / "contains").
                const dependencyEdgeRecords = result.graph.edges.filter((edge) => edge.type === 'dependency')
                const containsEdgeRecords = result.graph.edges.filter((edge) => edge.type === 'contains')

                // Contains edges render by default; dependency edges start hidden
                // and only reveal for the edges touching the selected file node.
                const renderEdges: Edge[] = [
                    ...containsEdgeRecords.map(toContainsFlowEdge),
                    ...dependencyEdgeRecords.map(toDependencyFlowEdge),
                ]

                const allUnpositioned = result.graph.nodes.every(
                    (node) => node.pos.x === 0 && node.pos.y === 0
                )
                if (allUnpositioned) {
                    // Dagre uses every edge (contains + dependency) so the full
                    // graph structure informs node positioning, even though
                    // dependency edges start hidden on the canvas.
                    flowNodes = applyDagreLayout(flowNodes, renderEdges) as AppNode[]
                }

                setGraphRecord(result.graph)
                setNodes(flowNodes)
                setEdges(renderEdges)
                lastSavedPositionsRef.current = Object.fromEntries(
                    flowNodes.map((n) => [n.id, n.position])
                )
                setIsLayoutDirty(false)
                setLayoutError(null)
                setLayoutNeedsCommit(false)
                setShowLayoutSaved(false)
                clearPendingDescriptions()
                setDescriptionsError(null)
                setDescriptionsNeedCommit(false)
                setShowDescriptionsSaved(false)
                setRetryDetails(null)
                setRetryError(null)
                setLoadState({ status: 'ready' })
            } catch {
                if (cancelled) return
                stopPolling()
                setLoadState({ status: 'failed', error: 'Could not reach the server — check your connection' })
                fetchRetryDetails()
            }
        }

        load()
        intervalId = setInterval(load, 3000)

        return () => {
            cancelled = true
            stopPolling()
        }
    }, [id, setNodes, setEdges, setGraphRecord, clearPendingDescriptions])

    // Re-submits the same repo (commit defaults to false — the original
    // commit setting isn't returned by the backend) and jumps to the new graph.
    const handleRetry = useCallback(async () => {
        if (!retryDetails) return
        setIsRetrying(true)
        setRetryError(null)
        try {
            const result = await createGraph(retryDetails.provider, retryDetails.owner, retryDetails.repo, retryDetails.branch, false)
            navigate(`/graph/${result.graphId}`)
        } catch {
            setRetryError('Failed to start a new graph — try again')
        } finally {
            setIsRetrying(false)
        }
    }, [retryDetails, navigate])

    const onSelectionChange = useCallback(({ nodes }: OnSelectionChangeParams) => {
        const selected = nodes.find(n => n.selected)
        if (selected) {
            setSelectedNode(selected as AppNode)
            setSidebarOpen(true)
            // Only file nodes have dependency edges; selecting a folder hides them all.
            setVisibleDependencyEdges(selected.type === 'file' ? selected.id : null)
        } else {
            setSelectedNode(null)
            setVisibleDependencyEdges(null)
        }
    }, [setSelectedNode, setSidebarOpen, setVisibleDependencyEdges])

    const onNodeClick = useCallback((_: React.MouseEvent, node: AppNode) => {
        // If the user clicks the node that is ALREADY selected, toggle it off!
        if (selectedNode?.id === node.id) {
            closeSidebarAndUnselect()
        }
    }, [selectedNode, closeSidebarAndUnselect])

    // Dropping a node just marks the layout dirty — positions are only
    // persisted to the backend (a real git commit) when the user presses Save.
    const onNodeDragStop = useCallback(() => {
        setIsLayoutDirty(true)
        setShowLayoutSaved(false)
    }, [])

    const handleSaveLayout = useCallback(async () => {
        if (!id) return
        setIsSavingLayout(true)
        setLayoutError(null)
        try {
            const positions = nodes.map((n) => ({ nodeId: n.id, x: n.position.x, y: n.position.y }))
            await saveLayout(id, positions)
            lastSavedPositionsRef.current = Object.fromEntries(
                positions.map((p) => [p.nodeId, { x: p.x, y: p.y }])
            )
            setIsLayoutDirty(false)
            flashLayoutSaved()
        } catch (err) {
            if (err instanceof ApiError && err.code === 'graph_not_committed') {
                setLayoutNeedsCommit(true)
            } else {
                console.error('Failed to save layout', err)
                setLayoutError('Failed to save layout — try again')
            }
        } finally {
            setIsSavingLayout(false)
        }
    }, [id, nodes, flashLayoutSaved])

    const handleDiscardLayout = useCallback(() => {
        setNodes(nodes.map((n) => ({
            ...n,
            position: lastSavedPositionsRef.current[n.id] ?? n.position,
        })))
        setIsLayoutDirty(false)
        setLayoutError(null)
        setLayoutNeedsCommit(false)
        setShowLayoutSaved(false)
    }, [nodes, setNodes])

    // Dismissing the prompt just abandons this save attempt — dragged
    // positions stay as unsaved local changes, they are not reverted.
    const handleDismissLayoutCommitPrompt = useCallback(() => {
        setLayoutNeedsCommit(false)
        setLayoutError(null)
    }, [])

    const handleCommitAndSaveLayout = useCallback(async () => {
        if (!id) return
        setIsCommittingLayout(true)
        setLayoutError(null)
        try {
            await commitGraph(id)
            const positions = nodes.map((n) => ({ nodeId: n.id, x: n.position.x, y: n.position.y }))
            await saveLayout(id, positions)
            lastSavedPositionsRef.current = Object.fromEntries(
                positions.map((p) => [p.nodeId, { x: p.x, y: p.y }])
            )
            setIsLayoutDirty(false)
            setLayoutNeedsCommit(false)
            flashLayoutSaved()
        } catch (err) {
            console.error('Failed to commit layout', err)
            setLayoutError('Failed to commit — try again')
        } finally {
            setIsCommittingLayout(false)
        }
    }, [id, nodes, flashLayoutSaved])

    const handleSaveDescriptions = useCallback(async () => {
        if (!id) return
        setIsSavingDescriptions(true)
        setDescriptionsError(null)
        try {
            const updates = Object.entries(pendingDescriptions).map(([nodeId, description]) => ({ nodeId, description }))
            await saveDescriptions(id, updates)
            applySavedDescriptions(updates)
            flashDescriptionsSaved()
        } catch (err) {
            if (err instanceof ApiError && err.code === 'graph_not_committed') {
                setDescriptionsNeedCommit(true)
            } else {
                console.error('Failed to save descriptions', err)
                setDescriptionsError('Failed to save descriptions — try again')
            }
        } finally {
            setIsSavingDescriptions(false)
        }
    }, [id, pendingDescriptions, applySavedDescriptions, flashDescriptionsSaved])

    const handleDiscardDescriptions = useCallback(() => {
        clearPendingDescriptions()
        setDescriptionsError(null)
        setDescriptionsNeedCommit(false)
        setShowDescriptionsSaved(false)
    }, [clearPendingDescriptions])

    // Dismissing the prompt just abandons this save attempt — pending
    // description edits stay as unsaved local changes, they are not reverted.
    const handleDismissDescriptionsCommitPrompt = useCallback(() => {
        setDescriptionsNeedCommit(false)
        setDescriptionsError(null)
    }, [])

    const handleCommitAndSaveDescriptions = useCallback(async () => {
        if (!id) return
        setIsCommittingDescriptions(true)
        setDescriptionsError(null)
        try {
            await commitGraph(id)
            const updates = Object.entries(pendingDescriptions).map(([nodeId, description]) => ({ nodeId, description }))
            await saveDescriptions(id, updates)
            applySavedDescriptions(updates)
            setDescriptionsNeedCommit(false)
            flashDescriptionsSaved()
        } catch (err) {
            console.error('Failed to commit descriptions', err)
            setDescriptionsError('Failed to commit — try again')
        } finally {
            setIsCommittingDescriptions(false)
        }
    }, [id, pendingDescriptions, applySavedDescriptions, flashDescriptionsSaved])

    if (loadState.status === 'processing') {
        return (
            <div className="w-full h-full flex flex-col items-center justify-center gap-4 bg-slate-50">
                <div className="w-10 h-10 border-4 border-slate-300 border-t-sky-500 rounded-full animate-spin" />
                <p className="text-slate-600 font-medium">Generating your graph...</p>
            </div>
        )
    }

    if (loadState.status === 'failed') {
        return (
            <div className="w-full h-full flex flex-col items-center justify-center gap-4 bg-slate-50 px-6">
                <div className="w-16 h-16 rounded-full bg-red-100 flex items-center justify-center">
                    <span className="text-3xl text-red-600">⚠</span>
                </div>
                <h2 className="text-xl font-bold text-slate-800">Graph generation failed</h2>
                <p className="text-red-600 font-medium text-center max-w-md">{loadState.error}</p>
                {retryError && <p className="text-sm text-red-500">{retryError}</p>}
                <div className="flex gap-3">
                    <button
                        onClick={handleRetry}
                        disabled={!retryDetails || isRetrying}
                        className="bg-sky-600 hover:bg-sky-500 disabled:opacity-50 text-white px-4 py-2 rounded-lg font-semibold transition-colors"
                    >
                        {isRetrying ? 'Retrying…' : 'Try again'}
                    </button>
                    <button
                        onClick={() => navigate('/repos')}
                        className="bg-slate-800 hover:bg-slate-700 text-white px-4 py-2 rounded-lg font-semibold transition-colors"
                    >
                        Go back
                    </button>
                </div>
            </div>
        )
    }

    return (
        <div className="w-full h-full bg-slate-50 flex flex-row font-sans relative">
            <ReactFlow
                nodes={nodes}
                edges={edges}
                nodeTypes={nodeTypes}
                edgeTypes={edgeTypes}
                onNodesChange={onNodesChange}
                onEdgesChange={onEdgesChange}
                onSelectionChange={onSelectionChange}
                onNodeClick={onNodeClick}
                onNodeDragStop={onNodeDragStop}
                nodesConnectable={false} // Disable adding new edges (read-only)
                elementsSelectable={true}
                fitView
            >
                <Controls />
                <Background color="#cbd5e1" gap={16}/>
            </ReactFlow>
            {/* Description and layout edits save independently (separate commits) —
                each gets its own toolbar, stacked here so they never overlap. */}
            <div className="absolute top-4 right-4 z-40 flex flex-col items-end gap-3">
                {descriptionsNeedCommit && (
                    <div className="bg-white shadow-lg rounded-lg border border-slate-200 p-3 flex flex-col gap-2 max-w-xs">
                        {descriptionsError && <span className="text-sm text-red-600">{descriptionsError}</span>}
                        <span className="text-sm text-amber-600">This graph hasn't been committed to your repository yet.</span>
                        <div className="flex items-center gap-3">
                            <button
                                onClick={handleDismissDescriptionsCommitPrompt}
                                disabled={isCommittingDescriptions}
                                className="bg-slate-100 hover:bg-slate-200 disabled:opacity-60 text-slate-700 px-3 py-1.5 rounded-lg text-sm font-semibold transition-colors"
                            >
                                Discard save
                            </button>
                            <button
                                onClick={handleCommitAndSaveDescriptions}
                                disabled={isCommittingDescriptions}
                                className="bg-sky-600 hover:bg-sky-500 disabled:opacity-60 text-white px-3 py-1.5 rounded-lg text-sm font-semibold transition-colors"
                            >
                                {isCommittingDescriptions ? 'Committing…' : 'Commit & Save'}
                            </button>
                        </div>
                    </div>
                )}
                {descriptionsDirty && !descriptionsNeedCommit && (
                    <div className="bg-white shadow-lg rounded-lg border border-slate-200 p-3 flex items-center gap-3">
                        {descriptionsError && <span className="text-sm text-red-600">{descriptionsError}</span>}
                        <button
                            onClick={handleDiscardDescriptions}
                            disabled={isSavingDescriptions}
                            className="bg-slate-100 hover:bg-slate-200 disabled:opacity-60 text-slate-700 px-3 py-1.5 rounded-lg text-sm font-semibold transition-colors"
                        >
                            Discard
                        </button>
                        <button
                            onClick={handleSaveDescriptions}
                            disabled={isSavingDescriptions}
                            className="bg-sky-600 hover:bg-sky-500 disabled:opacity-60 text-white px-3 py-1.5 rounded-lg text-sm font-semibold transition-colors"
                        >
                            {isSavingDescriptions ? 'Saving…' : 'Save descriptions'}
                        </button>
                    </div>
                )}
                {showDescriptionsSaved && !descriptionsDirty && !descriptionsNeedCommit && (
                    <div className="bg-white shadow-lg rounded-lg border border-slate-200 px-3 py-1.5">
                        <span className="text-sm text-emerald-600 font-medium">✓ Descriptions saved</span>
                    </div>
                )}

                {layoutNeedsCommit && (
                    <div className="bg-white shadow-lg rounded-lg border border-slate-200 p-3 flex flex-col gap-2 max-w-xs">
                        {layoutError && <span className="text-sm text-red-600">{layoutError}</span>}
                        <span className="text-sm text-amber-600">This graph hasn't been committed to your repository yet.</span>
                        <div className="flex items-center gap-3">
                            <button
                                onClick={handleDismissLayoutCommitPrompt}
                                disabled={isCommittingLayout}
                                className="bg-slate-100 hover:bg-slate-200 disabled:opacity-60 text-slate-700 px-3 py-1.5 rounded-lg text-sm font-semibold transition-colors"
                            >
                                Discard save
                            </button>
                            <button
                                onClick={handleCommitAndSaveLayout}
                                disabled={isCommittingLayout}
                                className="bg-sky-600 hover:bg-sky-500 disabled:opacity-60 text-white px-3 py-1.5 rounded-lg text-sm font-semibold transition-colors"
                            >
                                {isCommittingLayout ? 'Committing…' : 'Commit & Save'}
                            </button>
                        </div>
                    </div>
                )}
                {isLayoutDirty && !layoutNeedsCommit && (
                    <div className="bg-white shadow-lg rounded-lg border border-slate-200 p-3 flex items-center gap-3">
                        {layoutError && <span className="text-sm text-red-600">{layoutError}</span>}
                        <button
                            onClick={handleDiscardLayout}
                            disabled={isSavingLayout}
                            className="bg-slate-100 hover:bg-slate-200 disabled:opacity-60 text-slate-700 px-3 py-1.5 rounded-lg text-sm font-semibold transition-colors"
                        >
                            Discard
                        </button>
                        <button
                            onClick={handleSaveLayout}
                            disabled={isSavingLayout}
                            className="bg-sky-600 hover:bg-sky-500 disabled:opacity-60 text-white px-3 py-1.5 rounded-lg text-sm font-semibold transition-colors"
                        >
                            {isSavingLayout ? 'Saving…' : 'Save layout'}
                        </button>
                    </div>
                )}
                {showLayoutSaved && !isLayoutDirty && !layoutNeedsCommit && (
                    <div className="bg-white shadow-lg rounded-lg border border-slate-200 px-3 py-1.5">
                        <span className="text-sm text-emerald-600 font-medium">✓ Layout saved</span>
                    </div>
                )}
            </div>
            {togglesidebar && <SideBar togglefunc={closeSidebarAndUnselect} selectedNode={selectedNode} />}
        </div>
    )
}

export default GraphPage
