import { useEffect, useState } from "react"
import { useNavigate } from "react-router-dom"
import { getMe, API_BASE_URL } from "../services/api"

function LoginPage() {
    const navigate = useNavigate()
    const [checking, setChecking] = useState(true)

    useEffect(() => {
        getMe().then((user) => {
            if (user) {
                navigate("/repos", { replace: true })
            } else {
                setChecking(false)
            }
        })
    }, [navigate])

    if (checking) {
        return (
            <div className="w-full h-full flex items-center justify-center bg-[#1e2130]">
                <div className="w-10 h-10 border-4 border-slate-600 border-t-green-500 rounded-full animate-spin" />
            </div>
        )
    }

    return (
        <div className="w-full h-full flex items-center justify-center bg-[#1e2130]">
            <div className="flex flex-col items-center gap-8">
                <h1 className="text-4xl font-extrabold text-green-500">CodeAtlas</h1>
                <div className="flex flex-col gap-4 w-64">
                    <a
                        href={`${API_BASE_URL}/auth/github/login`}
                        className="text-center bg-gray-900 hover:bg-gray-800 text-white font-semibold py-2 px-4 rounded"
                    >
                        Login with GitHub
                    </a>
                    <a
                        href={`${API_BASE_URL}/auth/gitlab/login`}
                        className="text-center bg-orange-600 hover:bg-orange-700 text-white font-semibold py-2 px-4 rounded"
                    >
                        Login with GitLab
                    </a>
                </div>
            </div>
        </div>
    )
}

export default LoginPage
