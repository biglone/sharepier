package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"sharepier-api/internal/auth"
	"sharepier-api/internal/config"
	"sharepier-api/internal/db"
	"sharepier-api/internal/files"
	"sharepier-api/internal/httpx"
	"sharepier-api/internal/storage"
)

func main() {
	ctx := context.Background()
	cfg := config.Load()
	level := new(slog.LevelVar)
	level.Set(config.ParseLogLevel(cfg.LogLevel))

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	store, err := storage.NewStore(ctx, storage.Config{
		Backend:            cfg.StorageBackend,
		Root:               cfg.StorageRoot,
		S3Endpoint:         cfg.S3Endpoint,
		S3Region:           cfg.S3Region,
		S3Bucket:           cfg.S3Bucket,
		S3AccessKeyID:      cfg.S3AccessKeyID,
		S3SecretAccessKey:  cfg.S3SecretAccessKey,
		S3UseSSL:           cfg.S3UseSSL,
		S3UsePathStyle:     cfg.S3UsePathStyle,
		S3Prefix:           cfg.S3Prefix,
		S3AutoCreateBucket: cfg.S3AutoCreateBucket,
	})
	if err != nil {
		logger.Error("failed to initialize storage", slog.String("error", err.Error()))
		os.Exit(1)
	}

	database, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect database", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	if err := db.RunMigrations(ctx, database, cfg.MigrationsDir); err != nil {
		logger.Error("failed to run migrations", slog.String("error", err.Error()))
		os.Exit(1)
	}

	created, err := db.EnsureAdminUser(ctx, database, cfg.AdminBootstrapUsername, cfg.AdminBootstrapPassword)
	if err != nil {
		logger.Error("failed to bootstrap admin user", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if created {
		logger.Warn("bootstrapped default admin user", slog.String("username", cfg.AdminBootstrapUsername))
	}

	authService := auth.NewService(database, cfg, logger)
	fileService := files.NewService(
		database,
		store,
		cfg.PublicBaseURL,
		cfg.ResumableChunkSize,
		cfg.UploadSessionTTL,
		cfg.ShareAccessSecret,
		cfg.ShareAccessTTL,
	)
	router := httpx.NewRouter(cfg, logger, store, authService, fileService)
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info(
		"sharepier api listening",
		slog.String("addr", addr),
		slog.String("storage_backend", store.Backend()),
		slog.String("storage_location", store.Location()),
		slog.String("storage_root", store.Root()),
	)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server stopped", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
