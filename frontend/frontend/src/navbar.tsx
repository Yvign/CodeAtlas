import { Link, useNavigate } from "react-router-dom"
import { useShallow } from "zustand/react/shallow"
import { useStore } from "./store"
import { logout } from "./services/api"

function NavBar() {
    const navigate = useNavigate()
    const { user, clearUser } = useStore(useShallow((state) => ({
        user: state.user,
        clearUser: state.clearUser,
    })))

    const handleLogout = async () => {
        try {
            await logout()
        } catch (err) {
            console.error('Failed to logout', err)
        }
        clearUser()
        navigate('/login')
    }

    return (
        <div className="bg-[#1e2130] border-b border-indigo-500/40 h-14 text-white flex justify-between items-center px-6 shrink-0">
            <Link to="/repos" className="text-green-600 font-extrabold">CodeAtlas</Link>
            {user ? (
                <div className="flex items-center gap-4">
                    <span className="text-slate-300">{user.username}</span>
                    <button
                        onClick={handleLogout}
                        className="bg-slate-700 hover:bg-slate-600 text-white px-3 py-1.5 rounded-lg text-sm font-semibold transition-colors"
                    >
                        Logout
                    </button>
                </div>
            ) : (
                <Link to="/login" className="text-slate-300 hover:text-white">Login</Link>
            )}
        </div>
    )
}

export default NavBar
