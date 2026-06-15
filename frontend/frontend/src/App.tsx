import GraphPage from "./graphpage.tsx"
import {ReactFlowProvider} from "@xyflow/react"
function App() {

  return (
    <>
      <div style={{ display: "flex", width: "100vw", height: "100vh" }}>
          <ReactFlowProvider>
            <GraphPage />
          </ReactFlowProvider>
      </div>
    </>
  )
}

export default App
