import { useEffect, useCallback } from "react"
import { ReactFlow, Background, Controls, OnSelectionChangeParams } from "@xyflow/react"
import { useShallow } from 'zustand/react/shallow'
import { nodeTypes, AppNode } from "./components/Nodes"
import { edgeTypes } from "./components/Edges"
import SideBar from "./components/sideBar"
import { useStore } from "./store"

function GraphPage() {
    // We use shallow rendering to strictly prevent unnecessary React re-renders, maxing out performance.
    const { 
        nodes, edges, onNodesChange, onEdgesChange, loadGraphData,
        selectedNode, setSelectedNode,
        togglesidebar, setSidebarOpen,
        saveNodeNotes, closeSidebarAndUnselect
    } = useStore(useShallow((state) => ({
        nodes: state.nodes,
        edges: state.edges,
        onNodesChange: state.onNodesChange,
        onEdgesChange: state.onEdgesChange,
        loadGraphData: state.loadGraphData,
        selectedNode: state.selectedNode,
        setSelectedNode: state.setSelectedNode,
        togglesidebar: state.togglesidebar,
        setSidebarOpen: state.setSidebarOpen,
        saveNodeNotes: state.saveNodeNotes,
        closeSidebarAndUnselect: state.closeSidebarAndUnselect
    })))

    // Fetch initial data simulating backend API
    useEffect(() => {
        loadGraphData()
    }, [loadGraphData])

    const onSelectionChange = useCallback(({ nodes }: OnSelectionChangeParams) => {
        const selected = nodes.find(n => n.selected)
        if (selected) {
            setSelectedNode(selected as AppNode)
            setSidebarOpen(true)
        } else {
            setSelectedNode(null)
        }
    }, [setSelectedNode, setSidebarOpen])

    const onNodeClick = useCallback((_: React.MouseEvent, node: AppNode) => {
        // If the user clicks the node that is ALREADY selected, toggle it off!
        if (selectedNode?.id === node.id) {
            closeSidebarAndUnselect()
        }
    }, [selectedNode, closeSidebarAndUnselect])

    return (
        <div className="w-full h-full bg-slate-50 flex flex-row font-sans">
            <ReactFlow 
                nodes={nodes} 
                edges={edges}
                nodeTypes={nodeTypes} 
                edgeTypes={edgeTypes}
                onNodesChange={onNodesChange}
                onEdgesChange={onEdgesChange}
                onSelectionChange={onSelectionChange}
                onNodeClick={onNodeClick}
                nodesConnectable={false} // Disable adding new edges (read-only)
                elementsSelectable={true}
                fitView
            >
                <Controls />
                <Background color="#cbd5e1" gap={16}/>
            </ReactFlow>
            {togglesidebar && <SideBar togglefunc={closeSidebarAndUnselect} selectedNode={selectedNode} onSave={saveNodeNotes} />}
        </div>
    )
}

export default GraphPage