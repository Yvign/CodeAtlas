import { EdgeProps, getBezierPath } from '@xyflow/react';

function MyCustomEdge({
  sourceX, sourceY, targetX, targetY,
  sourcePosition, targetPosition
}: EdgeProps) {
  const [edgePath] = getBezierPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition });

  return (
    <path
      d={edgePath}
      stroke="#4f46e5"
      strokeWidth={2}
      fill="none"
      strokeDasharray="5,5"
    />
  );
}

const edgeTypes = { myEdge: MyCustomEdge };
export {edgeTypes}