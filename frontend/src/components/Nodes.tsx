import { Handle, Position, NodeProps, Node} from "@xyflow/react";

export type NodeData = {
    label: string;
    Nodedesc?: string;
    Notes?: string[];
}

export type FolderNode = Node<NodeData, 'folder'>
export type FileNode = Node<NodeData, 'file'>

export type AppNode = FolderNode | FileNode

function FolderNodeComponent({ data, selected }: NodeProps<FolderNode>) {
    return (
        <div className={`
            min-w-[140px] px-5 py-3 rounded-2xl border-2 
            backdrop-blur-md bg-white/60 shadow-lg 
            transition-all duration-300 ease-in-out
            ${selected ? 'border-sky-500 scale-105 shadow-sky-500/20' : 'border-sky-200 hover:border-sky-400 hover:shadow-xl'}
        `}>
            <Handle type="target" position={Position.Top} className="w-3 h-3 bg-sky-500!" />
            <div className="flex items-center space-x-3">
                <span className="text-xl">📁</span>
                <strong className="text-slate-800 font-semibold tracking-wide">{data.label}</strong>
            </div>
            <Handle type="source" position={Position.Bottom} className="w-3 h-3 bg-sky-500!" />
        </div>
    )
}

function FileNodeComponent({ data, selected }: NodeProps<FileNode>) {
    return (
        <div className={`
            min-w-[140px] px-5 py-3 rounded-2xl border-2 
            backdrop-blur-md bg-white/60 shadow-lg 
            transition-all duration-300 ease-in-out
            ${selected ? 'border-amber-500 scale-105 shadow-amber-500/20' : 'border-amber-200 hover:border-amber-400 hover:shadow-xl'}
        `}>
            <Handle type="target" position={Position.Top} className="w-3 h-3 bg-amber-500!" />
            <div className="flex items-center space-x-3">
                <span className="text-xl">📄</span>
                <strong className="text-slate-800 font-semibold tracking-wide">{data.label}</strong>
            </div>
            {/* Usually files don't have children, but we'll include a source handle just in case */}
            <Handle type="source" position={Position.Bottom} className="w-3 h-3 bg-amber-500!" />
        </div>
    )
}

export const nodeTypes = {
    folder: FolderNodeComponent,
    file: FileNodeComponent
}