import dagre from '@dagrejs/dagre'
import { Node, Edge } from '@xyflow/react'

const NODE_WIDTH = 180
const NODE_HEIGHT = 60

// edges may include structural edges (e.g. folder→file "contains" edges) purely
// to inform the layout hierarchy — this function only repositions nodes and
// never returns edges, so callers are free to pass edge types they don't render.
export function applyDagreLayout(nodes: Node[], edges: Edge[]): Node[] {
  const g = new dagre.graphlib.Graph()
  g.setDefaultEdgeLabel(() => ({}))
  g.setGraph({
    rankdir: 'TB',      // top to bottom
    ranksep: 80,        // vertical spacing between ranks
    nodesep: 40,        // horizontal spacing between nodes
    marginx: 40,
    marginy: 40,
  })

  nodes.forEach(node => {
    g.setNode(node.id, { width: NODE_WIDTH, height: NODE_HEIGHT })
  })

  edges.forEach(edge => {
    g.setEdge(edge.source, edge.target)
  })

  dagre.layout(g)

  return nodes.map(node => {
    const { x, y } = g.node(node.id)
    return {
      ...node,
      position: {
        x: x - NODE_WIDTH / 2,
        y: y - NODE_HEIGHT / 2,
      },
    }
  })
}
