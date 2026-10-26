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
	eqHandler := handlers.NewEquipmentHandler(pool)
	invHandler := handlers.NewInventoryHandler(pool)
	maintHandler := handlers.NewMaintenanceHandler(pool)
	metricsHandler := handlers.NewMetricsHandler(pool)
	lifecycleHandler := handlers.NewWorkOrderLifecycleHandler(pool)
	metricsAdminHandler := handlers.NewMetricsAdminHandler(pool)

	extractUserID := func(r *http.Request) *int {
		if claims, ok := auth.GetUserFromContext(r.Context()); ok && claims != nil {
			uid := claims.UserID
			return &uid
		}
		return nil
	}

	r.Route("/api/v1", func(api chi.Router) {
		api.Post("/auth/login", authHandler.Login)

		api.Group(func(protected chi.Router) {
			// Önce JWT doğrulanır, ardından kullanıcıya izole Idempotency ve Otomatik Audit Log çalışır!
			protected.Use(auth.Authenticate)
			protected.Use(appmw.RequireIdempotencyKey())
			protected.Use(appmw.Idempotency(pool, extractUserID))
			protected.Use(appmw.AuditTrail(pool, extractUserID))

			protected.Get("/auth/me", authHandler.Me)

			// F-1 & T-2: Ekipman Hiyerarşisi
			protected.Get("/equipments/tree", eqHandler.GetTree)
			protected.With(auth.RequireRoles("planlama", "yonetici")).Post("/equipments", eqHandler.Create)
			protected.With(auth.RequireRoles("planlama", "yonetici")).Patch("/equipments/{id}/move", eqHandler.Move)
			protected.With(auth.RequireRoles("teknisyen", "planlama", "uretim", "yonetici")).Post("/equipments/{id}/meter-readings", maintHandler.RecordMeterReading)

			// F-2: Arıza Bildirimi
			protected.Get("/failure-reports", maintHandler.ListFailureReports)
			protected.With(auth.RequireRoles("uretim", "teknisyen", "planlama", "yonetici")).Post("/failure-reports", maintHandler.CreateFailureReport)

			// F-3: İş Emirleri
			protected.Get("/work-orders", maintHandler.ListWorkOrders)
			protected.With(auth.RequireRoles("planlama", "yonetici")).Post("/work-orders", maintHandler.CreateWorkOrder)
			protected.With(auth.RequireRoles("teknisyen", "planlama", "yonetici")).Post("/work-orders/{id}/complete", maintHandler.CompleteWorkOrder)

			// F-3: Yaşam döngüsü (acik -> atandi -> devam_ediyor / iptal) ve geçmiş
			protected.With(auth.RequireRoles("teknisyen", "planlama", "yonetici")).Patch("/work-orders/{id}/status", lifecycleHandler.UpdateStatus)
			protected.Get("/work-orders/{id}/events", lifecycleHandler.ListEvents)

			// F-4 & T-1: Önleyici Bakım Planları
			protected.Get("/preventive-plans", maintHandler.ListPMPlans)
			protected.With(auth.RequireRoles("planlama", "yonetici")).Post("/preventive-plans", maintHandler.CreatePMPlan)
			protected.With(auth.RequireRoles("planlama", "yonetici")).Post("/preventive-plans/trigger-due", maintHandler.TriggerDueTimePMs)

			// F-5 & T-3: Yedek Parça Stoğu ve Rezervasyon
			protected.Get("/parts", invHandler.ListParts)
			protected.Get("/warehouses", invHandler.ListWarehouses)
			protected.With(auth.RequireRoles("depo", "yonetici")).Post("/stock-movements", invHandler.CreateStockMovement)
			protected.With(auth.RequireRoles("depo", "teknisyen", "planlama", "yonetici")).Post("/work-orders/{id}/reserve-part", invHandler.ReservePartForWorkOrder)

			// F-6: Teknisyen İş Yükü ve Atama
			protected.With(auth.RequireRoles("teknisyen", "planlama", "yonetici")).Get("/technicians/workload", maintHandler.GetTechnicianWorkload)
			protected.With(auth.RequireRoles("planlama", "yonetici")).Post("/work-orders/{id}/assign", lifecycleHandler.AssignTechnician)
			protected.With(auth.RequireRoles("planlama", "yonetici")).Delete("/work-orders/{id}/assign", lifecycleHandler.Unassign)

			// F-7 & T-4: Güvenilirlik Göstergeleri (MTBF, MTTR, Availability)
			protected.Get("/metrics/reliability", metricsHandler.GetReliabilityMetrics)
			protected.With(auth.RequireRoles("teknisyen", "planlama", "uretim", "yonetici")).Post("/downtime-events", metricsHandler.CreateDowntimeEvent)
			protected.With(auth.RequireRoles("planlama", "yonetici")).Patch("/downtime-events/{id}", metricsHandler.UpdateDowntimeEvent)

			// Denetim Kaydı
			protected.With(auth.RequireRoles("yonetici")).Get("/audit-logs", authHandler.ListAuditLogs)

			// Rapor altyapısı: materyalize view yenileme ve tazelik durumu.
			// REFRESH ... CONCURRENTLY transaction içinde çalışmaz, bu yüzden
			// yalnızca açık çağrıyla tetiklenir (otomatik değil).
			protected.With(auth.RequireRoles("yonetici")).Get("/admin/refresh-metrics", metricsAdminHandler.RefreshState)
			protected.With(auth.RequireRoles("yonetici")).Post("/admin/refresh-metrics", metricsAdminHandler.RefreshMetrics)
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
