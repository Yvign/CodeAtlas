import { useState, useEffect } from "react"
import { AppNode } from "./Nodes"

function SideBar({ togglefunc, selectedNode, onSave }: { togglefunc: () => void, selectedNode: AppNode | null, onSave: (nodeId: string, notes: string[]) => void }) {
    const [localNotes, setLocalNotes] = useState<string[]>([])
    const [inputValue, setInputValue] = useState("")
    const [hasUnsavedChanges, setHasUnsavedChanges] = useState(false)

    // Sync local state when a new node is selected
    useEffect(() => {
        if (selectedNode) {
            setLocalNotes(selectedNode.data.Notes || [])
            setInputValue("")
            setHasUnsavedChanges(false)
        }
    }, [selectedNode])

    const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (e.key === 'Enter' && inputValue.trim() !== '') {
            setLocalNotes([...localNotes, inputValue.trim()])
            setInputValue("")
            setHasUnsavedChanges(true)
        }
    }

    const handleSave = () => {
        if (selectedNode) {
            onSave(selectedNode.id, localNotes)
            setHasUnsavedChanges(false)
        }
    }

    const handleCancel = () => {
        if (selectedNode) {
            setLocalNotes(selectedNode.data.Notes || [])
            setInputValue("")
            setHasUnsavedChanges(false)
        }
    }

    return (
        <div className="w-80 h-full bg-slate-800 text-slate-200 flex flex-col shadow-2xl z-50 border-l border-slate-700 transition-all duration-300">
            {/* Header */}
            <div className="flex justify-between items-center p-5 border-b border-slate-700 bg-slate-900/50">
                <h2 className="text-xl font-bold tracking-wide">
                    {selectedNode ? selectedNode.data.label : 'Details'}
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
                {selectedNode ? (
                    <>
                        <div className="bg-slate-900/50 p-4 rounded-xl border border-slate-700">
                            <p className="text-sm text-slate-400 uppercase tracking-widest mb-1 font-semibold">Type</p>
                            <p className="text-lg font-medium capitalize">{selectedNode.type}</p>
                        </div>
                        
                        <div className="bg-slate-900/50 p-4 rounded-xl border border-slate-700">
                            <p className="text-sm text-slate-400 uppercase tracking-widest mb-1 font-semibold">Description</p>
                            <p className="text-md leading-relaxed">
                                {selectedNode.data.Nodedesc || 'No description provided.'}
                            </p>
                        </div>

                        {/* Notes Section */}
                        <div className="mt-8 pt-6 border-t border-slate-700 flex flex-col h-full">
                            <p className="text-sm text-slate-400 uppercase tracking-widest mb-3 font-semibold">Notes</p>
                            <input 
                                name="description" 
                                type="text" 
                                value={inputValue}
                                onChange={(e) => setInputValue(e.target.value)}
                                onKeyDown={handleKeyDown}
                                placeholder="Type a note and hit Enter..."
                                className="w-full bg-slate-900 border border-slate-700 rounded-lg px-4 py-2 text-sm focus:outline-none focus:border-sky-500 transition-colors"
                            />
                            <ul className="mt-4 text-sm text-slate-300 space-y-2">
                                {localNotes.map((note, index) => (
                                    <li key={index} className="flex items-start bg-slate-700/30 p-2 rounded-lg">
                                        <span className="mr-2 text-sky-500">•</span>
                                        <p>{note}</p>
                                    </li>
                                ))}
                            </ul>

                            {/* Save / Cancel buttons appear only when there are unsaved changes */}
                            {hasUnsavedChanges && (
                                <div className="mt-6 pt-4 border-t border-slate-700 flex space-x-3">
                                    <button 
                                        onClick={handleSave}
                                        className="flex-1 bg-sky-600 hover:bg-sky-500 text-white py-2 rounded-lg text-sm font-semibold transition-colors shadow-lg shadow-sky-900/20"
                                    >
                                        Save
                                    </button>
                                    <button 
                                        onClick={handleCancel}
                                        className="flex-1 bg-slate-700 hover:bg-slate-600 text-slate-200 py-2 rounded-lg text-sm font-semibold transition-colors"
                                    >
                                        Cancel
                                    </button>
                                </div>
                            )}
                        </div>
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