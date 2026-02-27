package server

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/shelterkin/shelterkin/internal/auth"
	"github.com/shelterkin/shelterkin/internal/config"
	"github.com/shelterkin/shelterkin/internal/crypto"
	"github.com/shelterkin/shelterkin/internal/middleware"
)

type Server struct {
	cfg        *config.Config
	db         *sql.DB
	enc        *crypto.Encryptor
	hmac       *crypto.HMACHasher
	httpServer *http.Server
	router     *http.ServeMux
}

func New(cfg *config.Config, db *sql.DB, enc *crypto.Encryptor, hmac *crypto.HMACHasher, staticFS fs.FS) *Server {
	secure := strings.HasPrefix(cfg.BaseURL, "https")

	authService := auth.NewService(db, enc, hmac)
	authHandler := auth.NewHandler(authService, cfg.SessionSecret, secure, middleware.GetCSRFToken)

	// app routes go through LoadSession + CSRF
	appMux := http.NewServeMux()
	appMux.HandleFunc("GET /login", authHandler.HandleLoginPage)
	appMux.HandleFunc("POST /login", authHandler.HandleLogin)
	appMux.HandleFunc("GET /register", authHandler.HandleRegisterPage)
	appMux.HandleFunc("POST /register", authHandler.HandleRegister)
	appMux.HandleFunc("POST /logout", authHandler.HandleLogout)
	appMux.Handle("GET /{$}", middleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!DOCTYPE html>
<html lang="en" data-theme="light">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"><title>Shelterkin</title></head>
<body style="font-family: system-ui, sans-serif; display: flex; align-items: center; justify-content: center; min-height: 100vh; margin: 0;">
<div style="text-align: center;"><h1>Shelterkin</h1><p>Server is running.</p></div>
</body>
</html>`))
	})))

	var appHandler http.Handler = appMux
	appHandler = middleware.CSRF(cfg.CSRFKey, secure)(appHandler)
	appHandler = middleware.LoadSession(authService, cfg.SessionSecret, secure)(appHandler)

	// top-level mux: /health and /static bypass LoadSession + CSRF
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.Handle("/", appHandler)

	// shared middleware: Recover → RequestID → SecurityHeaders → Logging → mux
	var handler http.Handler = mux
	handler = middleware.Logging(handler)
	handler = middleware.SecurityHeaders(handler)
	handler = middleware.RequestID(handler)
	handler = middleware.Recover(handler)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return &Server{
		cfg:        cfg,
		db:         db,
		enc:        enc,
		hmac:       hmac,
		httpServer: httpServer,
		router:     mux,
	}
}

func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
