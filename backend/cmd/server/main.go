package main

import (
	"github.com/joho/godotenv"
	"log"
	"net/http"
	"os"

	"CodeAtlas/internal/api"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	godotenv.Load()
	port := os.Getenv("CODEATLAS_PORT")
	if port == "" {
		port = "8080"
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/auth/github/login", api.HandleGithubLogin)
	r.Get("/auth/github/callback", api.HandleGithubCallback)
	r.Get("/auth/gitlab/login", api.HandleGitlabLogin)
	r.Get("/auth/gitlab/callback", api.HandleGitlabCallback)
	r.Post("/auth/logout", api.StubHandler)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Group(func(r chi.Router) {
		r.Use(api.JWTMiddleware)

		r.Get("/repos", api.HandleListRepos)
		r.Get("/repos/{provider}/{owner}/{repo}/branches", api.HandleListBranches)
		r.Get("/graphs", api.HandleListGraphs)
		r.Post("/graphs", api.HandleCreateGraph)
		r.Get("/graphs/{id}", api.HandleGetGraph)
		r.Delete("/graphs/{id}", api.HandleDeleteGraph)
		r.Patch("/graphs/{id}/nodes/{nodeId}", api.HandleUpdateNodeDescription)
		r.Post("/graphs/{id}/nodes/{nodeId}/notes", api.HandleAddNote)
		r.Patch("/graphs/{id}/notes/{noteId}", api.HandleUpdateNote)
		r.Delete("/graphs/{id}/notes/{noteId}", api.HandleDeleteNote)
		r.Patch("/graphs/{id}/layout", api.HandleUpdateLayout)
	})

	log.Printf("server listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}
