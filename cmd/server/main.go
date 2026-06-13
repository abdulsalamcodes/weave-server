package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/joho/godotenv"

	"github.com/abdulsalamcodes/weave-server/internal/config"
	"github.com/abdulsalamcodes/weave-server/internal/db"
	"github.com/abdulsalamcodes/weave-server/internal/server"
	"github.com/abdulsalamcodes/weave-server/pkg/logger"
)

func main() {
	// Load .env in development
	godotenv.Load()

	log := logger.New(os.Getenv("LOG_LEVEL"))
	if log == nil {
		log = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	cfg, err := config.Load()
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Database
	pool, err := db.NewPool(ctx, cfg.Database.URL)
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Run migrations
	if err := db.RunMigrations(ctx, pool); err != nil {
		log.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	// Redis
	rdb, err := db.NewRedis(ctx, cfg.Redis.URL)
	if err != nil {
		log.Warn("redis unavailable, running without cache", "error", err)
		rdb = nil
	}
	if rdb != nil {
		defer rdb.Close()
	}

	// Start server
	srv := server.New(cfg, pool, rdb, log)
	if err := srv.Start(); err != nil {
		log.Error("server error", "error", err)
		os.Exit(1)
	}

	log.Info("server stopped")
	fmt.Println("bye")
}
