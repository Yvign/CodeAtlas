import { useEffect, useState } from "react"
import { Navigate, Outlet } from "react-router-dom"
import { getMe } from "../services/api"
import { useStore } from "../store"

function ProtectedRoute() {
    const setUser = useStore((state) => state.setUser)
    const [status, setStatus] = useState<"loading" | "authenticated" | "unauthenticated">("loading")

    useEffect(() => {
        getMe().then((user) => {
            if (user) {
                setUser(user)
                setStatus("authenticated")
            } else {
                setStatus("unauthenticated")
            }
        })
    }, [setUser])

    if (status === "loading") {
        return (
            <div className="w-full h-full flex items-center justify-center">
                <div className="w-10 h-10 border-4 border-gray-300 border-t-green-600 rounded-full animate-spin" />
            </div>
        )
    }

    if (status === "unauthenticated") {
        return <Navigate to="/login" replace />
    }

    return <Outlet />
}

export default ProtectedRoute
