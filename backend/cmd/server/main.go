package main

import (
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"

	"CodeAtlas/internal/api"
	"CodeAtlas/internal/db"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

func main() {
	godotenv.Load()

	var (
		userRepo  db.UserRepository
		tokenRepo db.TokenRepository
		graphRepo db.GraphRepository
	)

	if dbURL := os.Getenv("CODEATLAS_DB_URL"); dbURL != "" {
		database, err := db.New(dbURL)
		if err != nil {
			log.Fatalf("connect to database: %v", err)
		}
		userRepo = db.NewPostgresUserRepository(database)
		tokenRepo = db.NewPostgresTokenRepository(database)
		graphRepo = db.NewPostgresGraphRepository(database)
		log.Println("database connected")
	} else {
		log.Println("CODEATLAS_DB_URL not set, using in-memory repositories")
		userRepo = db.NewMockUserRepository()
		tokenRepo = db.NewMockTokenRepository()
		graphRepo = db.NewMockGraphRepository()
	}

	authH := &api.AuthHandler{Users: userRepo, Tokens: tokenRepo}
	reposH := &api.ReposHandler{Tokens: tokenRepo}
	graphsH := &api.GraphsHandler{Graphs: graphRepo, Tokens: tokenRepo}

	frontendURL := os.Getenv("CODEATLAS_FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:5173"
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{frontendURL},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/auth/github/login", authH.HandleGithubLogin)
	r.Get("/auth/github/callback", authH.HandleGithubCallback)
	r.Get("/auth/gitlab/login", authH.HandleGitlabLogin)
	r.Get("/auth/gitlab/callback", authH.HandleGitlabCallback)
	r.Post("/auth/logout", authH.HandleLogout)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Group(func(r chi.Router) {
		r.Use(api.JWTMiddleware)

		r.Get("/auth/me", authH.HandleMe)
		r.Get("/repos", reposH.HandleListRepos)
		r.Get("/repos/{provider}/{owner}/{repo}/branches", reposH.HandleListBranches)
		r.Get("/graphs", graphsH.HandleListGraphs)
		r.Post("/graphs", graphsH.HandleCreateGraph)
		r.Get("/graphs/{id}", graphsH.HandleGetGraph)
		r.Delete("/graphs/{id}", graphsH.HandleDeleteGraph)
		r.Patch("/graphs/{id}/nodes", graphsH.HandleUpdateNodeDescriptions)
		r.Post("/graphs/{id}/commit", graphsH.HandleCommitGraph)
		r.Patch("/graphs/{id}/layout", graphsH.HandleUpdateLayout)
	})

	port := os.Getenv("CODEATLAS_PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("server listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}
