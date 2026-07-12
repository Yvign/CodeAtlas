import { Navigate, Route, Routes } from "react-router-dom"
import { ReactFlowProvider } from "@xyflow/react"
import ProtectedRoute from "./components/ProtectedRoute"
import LoginPage from "./pages/LoginPage"
import ReposPage from "./pages/ReposPage"
import GraphPage from "./pages/GraphPage"
import NavBar from "./navbar"

function App() {

  return (
    <div style={{ display: "flex", flexDirection: "column", width: "100vw", height: "100vh" }}>
      <NavBar />
      <div style={{ flex: 1, minHeight: 0, display: "flex" }}>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route element={<ProtectedRoute />}>
            <Route path="/repos" element={<ReposPage />} />
            <Route
              path="/graph/:id"
              element={
                <ReactFlowProvider>
                  <GraphPage />
                </ReactFlowProvider>
              }
            />
          </Route>
          <Route path="/" element={<Navigate to="/repos" replace />} />
        </Routes>
      </div>
    </div>
  )
}

export default App
