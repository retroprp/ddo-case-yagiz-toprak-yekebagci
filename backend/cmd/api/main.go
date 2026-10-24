package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bakimplan/internal/auth"
	"bakimplan/internal/database"
	"bakimplan/internal/handlers"
	appmw "bakimplan/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://cmms_user:cmms_secret_pass@localhost:5432/bakimplan_db?sslmode=disable"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := database.ConnectAndMigrate(ctx, dbURL)
	if err != nil {
		slog.Error("failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// 5 Rol için Demo Kullanıcılarını Otomatik Oluştur
	if err := handlers.SeedDefaultUsers(ctx, pool); err != nil {
		slog.Error("failed to seed default users", "error", err)
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(15 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "Idempotency-Key"},
		AllowCredentials: true,
	}))

	// Healthcheck
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		pingCtx, pingCancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer pingCancel()

		if err := pool.Ping(pingCtx); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "unhealthy", "database": "disconnected"})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "healthy", "database": "connected", "service": "bakimplan-api"})
	})

	authHandler := handlers.NewAuthHandler(pool)

	r.Route("/api/v1", func(api chi.Router) {
		api.Use(appmw.Idempotency(pool))

		// Açık Uçlar
		api.Post("/auth/login", authHandler.Login)

		// Korumalı Uçlar (JWT Zorunlu)
		api.Group(func(protected chi.Router) {
			protected.Use(auth.Authenticate)

			protected.Get("/auth/me", authHandler.Me)

			// Yalnızca Yönetici Denetim Kayıtlarını (Audit Logs) Görebilir
			protected.With(auth.RequireRoles("yonetici")).Get("/audit-logs", authHandler.ListAuditLogs)
		})
	})

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("starting BakımPlan API server", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server crashed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	slog.Info("shutting down gracefully...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}
