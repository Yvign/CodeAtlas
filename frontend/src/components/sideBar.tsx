import { useShallow } from 'zustand/react/shallow'
import { AppNode } from "./Nodes"
import { useStore } from "../store"

function SideBar({ togglefunc, selectedNode }: { togglefunc: () => void, selectedNode: AppNode | null }) {
    const { edges, nodes, visibleDependencyEdges, graphRecord, pendingDescriptions, setPendingDescription, revertPendingDescription } = useStore(useShallow((state) => ({
        edges: state.edges,
        nodes: state.nodes,
        visibleDependencyEdges: state.visibleDependencyEdges,
        graphRecord: state.graphRecord,
        pendingDescriptions: state.pendingDescriptions,
        setPendingDescription: state.setPendingDescription,
        revertPendingDescription: state.revertPendingDescription,
    })))

    const nodeRecord = selectedNode
        ? graphRecord?.nodes.find((n) => n.uuid === selectedNode.id) ?? null
        : null

    const importRelationships = selectedNode && selectedNode.type === 'file'
        ? edges
            .filter((e) => visibleDependencyEdges.has(e.id))
            .map((e) => {
                const otherId = e.source === selectedNode.id ? e.target : e.source
                return nodes.find((n) => n.id === otherId)?.data.label
            })
            .filter((label): label is string => Boolean(label))
        : []

    // The textarea always reflects global pending state (if this node has an
    // unsaved edit) falling back to the last-persisted description — never
    // local component state, so switching nodes doesn't lose an in-progress edit.
    const isDirty = selectedNode !== null && selectedNode.id in pendingDescriptions
    const currentDescription = selectedNode
        ? (pendingDescriptions[selectedNode.id] ?? nodeRecord?.description ?? "")
        : ""

    const handleChange = (value: string) => {
        if (!selectedNode) return
        setPendingDescription(selectedNode.id, value)
    }

    const handleRevert = () => {
        if (!selectedNode) return
        revertPendingDescription(selectedNode.id)
    }

    return (
        <div className="w-80 h-full bg-slate-800 text-slate-200 flex flex-col shadow-2xl z-50 border-l border-slate-700 transition-all duration-300">
            {/* Header */}
            <div className="flex justify-between items-center p-5 border-b border-slate-700 bg-slate-900/50">
                <h2 className="text-xl font-bold tracking-wide">
                    {nodeRecord ? nodeRecord.name : 'Details'}
                </h2>
                <button
                    onClick={togglefunc}
                    className="w-8 h-8 flex items-center justify-center rounded-full hover:bg-slate-700 text-slate-400 hover:text-white transition-colors"
                >
                    ✕
                </button>
            </div>

            {/* Body */}
            <div className="p-5 flex-1 overflow-y-auto space-y-4">
                {nodeRecord ? (
                    <>
                        <div className="bg-slate-900/50 p-4 rounded-xl border border-slate-700">
                            <p className="text-sm text-slate-400 uppercase tracking-widest mb-1 font-semibold">Name</p>
                            <p className="text-lg font-medium">{nodeRecord.name}</p>
                        </div>

                        <div className="bg-slate-900/50 p-4 rounded-xl border border-slate-700">
                            <p className="text-sm text-slate-400 uppercase tracking-widest mb-1 font-semibold">Path</p>
                            <p className="text-md break-all">{nodeRecord.path || '—'}</p>
                        </div>

                        <div className="bg-slate-900/50 p-4 rounded-xl border border-slate-700">
                            <p className="text-sm text-slate-400 uppercase tracking-widest mb-1 font-semibold">Type</p>
                            <p className="text-lg font-medium capitalize">{nodeRecord.type}</p>
                        </div>

                        {importRelationships.length > 0 && (
                            <div className="bg-slate-900/50 p-4 rounded-xl border border-slate-700">
                                <p className="text-sm text-slate-400 uppercase tracking-widest mb-2 font-semibold">Import relationships</p>
                                <ul className="space-y-1 text-sm text-slate-300">
                                    {importRelationships.map((name, index) => (
                                        <li key={index} className="flex items-center gap-2">
                                            <span className="text-indigo-400">↔</span>
                                            {name}
                                        </li>
                                    ))}
                                </ul>
                            </div>
                        )}

                        <div className="bg-slate-900/50 p-4 rounded-xl border border-slate-700">
                            <div className="flex items-center justify-between mb-2">
                                <p className="text-sm text-slate-400 uppercase tracking-widest font-semibold">Description</p>
                                {isDirty && (
                                    <span className="text-xs text-amber-400 font-medium">Unsaved</span>
                                )}
                            </div>
                            <textarea
                                value={currentDescription}
                                onChange={(e) => handleChange(e.target.value)}
                                placeholder="No description provided."
                                rows={6}
                                className="w-full bg-slate-900 border border-slate-700 rounded-lg px-4 py-2 text-sm focus:outline-none focus:border-sky-500 transition-colors resize-none"
                            />
                        </div>

                        {/* Saving happens globally (see the canvas toolbar) — this only
                            reverts this node's pending edit back to its last-saved value. */}
                        {isDirty && (
                            <div className="pt-4 border-t border-slate-700">
                                <button
                                    onClick={handleRevert}
                                    className="w-full bg-slate-700 hover:bg-slate-600 text-slate-200 py-2 rounded-lg text-sm font-semibold transition-colors"
                                >
                                    Revert
                                </button>
                            </div>
                        )}
                    </>
                ) : (
                    <div className="h-full flex flex-col items-center justify-center text-slate-500 space-y-3">
                        <span className="text-4xl text-slate-600">🖱️</span>
                        <p className="text-center font-medium">Select a node to view its details</p>
                    </div>
                )}
            </div>
        </div>
    )
}

export default SideBar
