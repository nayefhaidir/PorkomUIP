package bootstrap

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"uip-meeting-backend/internal/platform/sqlstore"
	httpapi "uip-meeting-backend/internal/transport/http"
)

func Run() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := sqlstore.Open(ctx, sqlstore.ParseDBURL(os.Getenv("DATABASE_URL")))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer store.Close()

	if len(os.Args) > 1 && os.Args[1] == "seed" {
		if err := sqlstore.SeedDatabase(ctx, store); err != nil {
			log.Fatalf("seed database: %v", err)
		}
		log.Printf("Seed completed")
		return
	}

	uploadDir := os.Getenv("UPLOAD_DIR")
	if uploadDir == "" {
		uploadDir = "uploads"
	}
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		log.Fatalf("create upload directory: %v", err)
	}

	server := &http.Server{
		Addr:              env("ADDR", ":8080"),
		Handler:           httpapi.NewHandler(store),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go httpapi.RunBackgroundJobs(ctx, store)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()

	log.Printf("Go API listening on %s using %s", server.Addr, store.Driver())
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
