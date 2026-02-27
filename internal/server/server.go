package server

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/http"
	"time"

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
	mux := http.NewServeMux()

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!DOCTYPE html>
<html lang="en" data-theme="light">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"><title>Shelterkin</title></head>
<body style="font-family: system-ui, sans-serif; display: flex; align-items: center; justify-content: center; min-height: 100vh; margin: 0;">
<div style="text-align: center;"><h1>Shelterkin</h1><p>Server is running.</p></div>
</body>
</html>`))
	})

	// middleware chain: outermost wraps first
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
