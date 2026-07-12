import { useEffect, useState, FormEvent } from "react"
import { useNavigate } from "react-router-dom"
import { useStore } from "../store"
import { createGraph, listGraphs, deleteGraph, GraphEntry } from "../services/api"

type ParsedRepo = {
    provider: "github" | "gitlab"
    owner: string
    repo: string
}

function parseRepoUrl(url: string): ParsedRepo | null {
    let parsed: URL
    try {
        parsed = new URL(url.trim())
    } catch {
        return null
    }

    const host = parsed.hostname.replace(/^www\./, "")
    let provider: "github" | "gitlab" | null = null
    if (host === "github.com") provider = "github"
    else if (host === "gitlab.com") provider = "gitlab"
    if (!provider) return null

    const [owner, repoRaw] = parsed.pathname.split("/").filter(Boolean)
    if (!owner || !repoRaw) return null

    return { provider, owner, repo: repoRaw.replace(/\.git$/, "") }
}

const STATUS_STYLES: Record<string, string> = {
    processing: "bg-yellow-500/15 text-yellow-400 border border-yellow-500/40",
    ready: "bg-[#00ff88]/15 text-[#00ff88] border border-[#00ff88]/40",
    failed: "bg-red-500/15 text-red-400 border border-red-500/40",
}

function ReposPage() {
    const user = useStore((state) => state.user)
    const navigate = useNavigate()

    const [repoUrl, setRepoUrl] = useState("")
    const [branch, setBranch] = useState("main")
    const [commitToRepo, setCommitToRepo] = useState(false)
    const [submitError, setSubmitError] = useState<string | null>(null)
    const [submitting, setSubmitting] = useState(false)

    const [graphs, setGraphs] = useState<GraphEntry[]>([])
    const [loadingGraphs, setLoadingGraphs] = useState(true)
    const [listError, setListError] = useState<string | null>(null)

    const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null)
    const [deletingId, setDeletingId] = useState<string | null>(null)
    const [deleteError, setDeleteError] = useState<string | null>(null)

    useEffect(() => {
        listGraphs()
            .then(setGraphs)
            .catch(() => setListError("Failed to load your graphs"))
            .finally(() => setLoadingGraphs(false))
    }, [])

    const handleDeleteClick = (id: string) => {
        setConfirmDeleteId(id)
        setDeleteError(null)
    }

    const handleCancelDelete = () => {
        setConfirmDeleteId(null)
        setDeleteError(null)
    }

    const handleConfirmDelete = async (id: string) => {
        setDeletingId(id)
        setDeleteError(null)
        try {
            await deleteGraph(id)
            setGraphs((prev) => prev.filter((g) => g.id !== id))
            setConfirmDeleteId(null)
        } catch {
            setDeleteError("Failed to delete — try again")
        } finally {
            setDeletingId(null)
        }
    }

    const handleSubmit = async (e: FormEvent) => {
        e.preventDefault()
        setSubmitError(null)

        const parsed = parseRepoUrl(repoUrl)
        if (!parsed) {
            setSubmitError("Could not parse repo URL — use https://github.com/owner/repo format")
            return
        }

        setSubmitting(true)
        try {
            const result = await createGraph(parsed.provider, parsed.owner, parsed.repo, branch.trim() || "main", commitToRepo)
            navigate(`/graph/${result.graphId}`)
        } catch {
            setSubmitError("Failed to generate graph — please try again")
        } finally {
            setSubmitting(false)
        }
    }

    return (
        <div className="w-full h-full flex flex-col bg-[#0d1117] text-slate-200">
            <div className="flex-1 overflow-y-auto">
                <div className="max-w-2xl mx-auto px-6 py-10 space-y-10">
                    {user && (
                        <p className="text-sm text-slate-400">
                            Signed in as <span className="text-[#00ff88] font-medium">{user.username}</span>
                        </p>
                    )}

                    <section className="bg-[#161b22] border border-slate-800 rounded-2xl p-6 space-y-4">
                        <h2 className="text-lg font-bold text-white tracking-wide">Submit a repo</h2>
                        <form onSubmit={handleSubmit} className="space-y-4">
                            <div>
                                <label className="block text-xs uppercase tracking-widest text-slate-400 mb-1">
                                    Repo URL
                                </label>
                                <input
                                    type="text"
                                    value={repoUrl}
                                    onChange={(e) => setRepoUrl(e.target.value)}
                                    placeholder="https://github.com/owner/repo"
                                    className="w-full bg-[#0d1117] border border-slate-700 rounded-lg px-4 py-2 text-sm focus:outline-none focus:border-[#00ff88] transition-colors"
                                />
                            </div>
                            <div>
                                <label className="block text-xs uppercase tracking-widest text-slate-400 mb-1">
                                    Branch
                                </label>
                                <input
                                    type="text"
                                    value={branch}
                                    onChange={(e) => setBranch(e.target.value)}
                                    placeholder="main"
                                    className="w-full bg-[#0d1117] border border-slate-700 rounded-lg px-4 py-2 text-sm focus:outline-none focus:border-[#00ff88] transition-colors"
                                />
                            </div>
                            <div>
                                <label className="flex items-center gap-2 text-sm text-slate-300 cursor-pointer">
                                    <input
                                        type="checkbox"
                                        checked={commitToRepo}
                                        onChange={(e) => setCommitToRepo(e.target.checked)}
                                        className="h-4 w-4 rounded border-slate-700 bg-[#0d1117] accent-[#00ff88]"
                                    />
                                    Commit graph to repository
                                </label>
                                {commitToRepo && (
                                    <p className="mt-2 text-xs text-yellow-400 bg-yellow-500/10 border border-yellow-500/30 rounded-lg px-3 py-2">
                                        This will commit .codeatlas/graph.json to your repository
                                    </p>
                                )}
                            </div>
                            {submitError && <p className="text-sm text-red-400">{submitError}</p>}
                            <button
                                type="submit"
                                disabled={submitting}
                                className="w-full bg-[#00ff88] hover:bg-[#00e67a] disabled:opacity-50 text-[#0d1117] font-semibold py-2 rounded-lg transition-colors"
                            >
                                Generate Graph
                            </button>
                        </form>
                    </section>

                    <section className="space-y-4">
                        <h2 className="text-lg font-bold text-white tracking-wide">Your graphs</h2>
                        {loadingGraphs ? (
                            <p className="text-sm text-slate-500">Loading...</p>
                        ) : listError ? (
                            <p className="text-sm text-red-400">{listError}</p>
                        ) : graphs.length === 0 ? (
                            <p className="text-sm text-slate-500">No graphs yet — submit a repo above</p>
                        ) : (
                            <div className="space-y-3">
                                {graphs.map((g) => (
                                    <div
                                        key={g.id}
                                        className="bg-[#161b22] border border-slate-800 hover:border-[#00ff88]/50 rounded-xl px-5 py-4 transition-colors"
                                    >
                                        <div className="flex items-center justify-between gap-3">
                                            <button
                                                onClick={() => navigate(`/graph/${g.id}`)}
                                                className="flex-1 flex items-center justify-between text-left"
                                            >
                                                <div>
                                                    <p className="font-semibold text-white">
                                                        {g.owner}/{g.repoName}
                                                    </p>
                                                    <p className="text-xs text-slate-500">{g.branch}</p>
                                                </div>
                                                <span
                                                    className={`text-xs font-semibold px-3 py-1 rounded-full capitalize ${
                                                        STATUS_STYLES[g.status] ?? "bg-slate-700 text-slate-300 border border-slate-600"
                                                    }`}
                                                >
                                                    {g.status}
                                                </span>
                                            </button>
                                            <button
                                                onClick={() => handleDeleteClick(g.id)}
                                                aria-label="Delete graph"
                                                title="Delete graph"
                                                className="shrink-0 text-slate-500 hover:text-red-400 transition-colors p-1"
                                            >
                                                🗑️
                                            </button>
                                        </div>

                                        {confirmDeleteId === g.id && (
                                            <div className="mt-3 pt-3 border-t border-slate-800 space-y-2">
                                                <p className="text-sm text-slate-400">
                                                    Delete this graph? This will not affect your repository.
                                                </p>
                                                {deleteError && <p className="text-sm text-red-400">{deleteError}</p>}
                                                <div className="flex gap-3">
                                                    <button
                                                        onClick={() => handleConfirmDelete(g.id)}
                                                        disabled={deletingId === g.id}
                                                        className="bg-red-600 hover:bg-red-500 disabled:opacity-60 text-white px-3 py-1.5 rounded-lg text-sm font-semibold transition-colors"
                                                    >
                                                        {deletingId === g.id ? 'Deleting…' : 'Confirm'}
                                                    </button>
                                                    <button
                                                        onClick={handleCancelDelete}
                                                        disabled={deletingId === g.id}
                                                        className="bg-slate-700 hover:bg-slate-600 disabled:opacity-60 text-slate-200 px-3 py-1.5 rounded-lg text-sm font-semibold transition-colors"
                                                    >
                                                        Cancel
                                                    </button>
                                                </div>
                                            </div>
                                        )}
                                    </div>
                                ))}
                            </div>
                        )}
                    </section>
                </div>
            </div>
        </div>
    )
}

export default ReposPage
