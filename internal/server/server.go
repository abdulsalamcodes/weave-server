package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/abdulsalamcodes/weave-server/internal/config"
	"github.com/abdulsalamcodes/weave-server/internal/handler"
	"github.com/abdulsalamcodes/weave-server/internal/middleware"
	"github.com/abdulsalamcodes/weave-server/internal/provider/llm"
	"github.com/abdulsalamcodes/weave-server/internal/provider/mono"
	"github.com/abdulsalamcodes/weave-server/internal/provider/okra"
	"github.com/abdulsalamcodes/weave-server/internal/provider/paystack"
	"github.com/abdulsalamcodes/weave-server/internal/repository"
	"github.com/abdulsalamcodes/weave-server/internal/service"
	"github.com/abdulsalamcodes/weave-server/pkg/idempotency"
)

type Server struct {
	cfg    *config.Config
	db     *pgxpool.Pool
	rdb    *redis.Client
	logger *slog.Logger
	router chi.Router
	http   *http.Server
}

func New(cfg *config.Config, db *pgxpool.Pool, rdb *redis.Client, logger *slog.Logger) *Server {
	s := &Server{
		cfg:    cfg,
		db:     db,
		rdb:    rdb,
		logger: logger,
		router: chi.NewRouter(),
	}

	s.setupMiddleware()
	s.setupRoutes()

	return s
}

func (s *Server) setupMiddleware() {
	s.router.Use(chimw.RealIP)
	s.router.Use(middleware.RequireJSON)
	s.router.Use(middleware.MaxBody)
	s.router.Use(middleware.SecurityHeaders)
	s.router.Use(middleware.RequestID)
	s.router.Use(middleware.Recovery(s.logger))
	s.router.Use(middleware.Logging(s.logger))
	s.router.Use(middleware.CORS([]string{"*"}))

	rateLimiter := middleware.NewRateLimiter(
		s.cfg.Ratelimit.Requests,
		s.cfg.Ratelimit.Window,
	)
	s.router.Use(middleware.RateLimit(rateLimiter, s.logger))

	// Idempotency store — use Redis when available, fall back to in-memory
	var idempotencyStore middleware.IDKeyStore
	if s.rdb != nil {
		idempotencyStore = &redisIDKeyStore{store: idempotency.NewStore(s.rdb, 24*time.Hour)}
		s.logger.Info("using redis idempotency store")
	} else {
		idempotencyStore = middleware.NewInMemoryIDKeyStore(24 * time.Hour)
		s.logger.Warn("redis unavailable, using in-memory idempotency store")
	}
	s.router.Use(middleware.Idempotency(idempotencyStore, s.logger))

	// Auth middleware
	authCfg := middleware.AuthConfig{
		JWTSecret: s.cfg.JWT.Secret,
		BypassPaths: []string{
			"/health",
			"/api/v1/auth/register",
			"/api/v1/auth/login",
			"/api/v1/auth/token/refresh",
		},
	}
	s.router.Use(middleware.Auth(authCfg, s.logger))

	// Timeout
	s.router.Use(middleware.Timeout(30*time.Second, s.logger))
}

func (s *Server) setupRoutes() {
	// Repositories
	userRepo := repository.NewUserRepo(s.db)
	walletRepo := repository.NewWalletRepo(s.db)
	txnRepo := repository.NewTransactionRepo(s.db)
	bankRepo := repository.NewBankAccountRepo(s.db)

	// External clients
	var paystackClient *paystack.Client
	if s.cfg.Paystack.SecretKey != "" {
		paystackClient = paystack.NewClient(s.cfg.Paystack.SecretKey, s.cfg.Paystack.PublicKey)
		s.logger.Info("paystack client initialized")
	}

	var llmClient *llm.Client
	if s.cfg.LLM.APIKey != "" {
		llmClient = llm.NewClient(s.cfg.LLM.APIKey, s.cfg.LLM.Model)
		s.logger.Info("llm client initialized", "model", s.cfg.LLM.Model)
	}

	var okraClient *okra.Client
	if s.cfg.Okra.ClientID != "" && s.cfg.Okra.Secret != "" {
		okraClient = okra.NewClient(s.cfg.Okra.ClientID, s.cfg.Okra.Secret)
		s.logger.Info("okra client initialized")
	}

	var monoClient *mono.Client
	if s.cfg.Mono.SecretKey != "" {
		monoClient = mono.NewClient(s.cfg.Mono.SecretKey)
		s.logger.Info("mono client initialized")
	}

	// Services
	authService := service.NewAuthService(
		userRepo, walletRepo,
		s.cfg.Auth, s.cfg.JWT.Secret,
		s.cfg.JWT.AccessTTL, s.cfg.JWT.RefreshTTL,
		s.db,
		s.logger,
	)
	walletService := service.NewWalletService(walletRepo, userRepo, paystackClient, s.logger)
	sourcingEngine := service.NewSourcingEngine(walletService, bankRepo, s.logger)
	payoutService := service.NewPayoutService(paystackClient, s.logger)
	transferService := service.NewTransferService(txnRepo, walletRepo, walletService, sourcingEngine, payoutService, s.db, s.logger)

	// Handlers
	healthHandler := handler.NewHealthHandler()
	authHandler := handler.NewAuthHandler(authService, s.logger)
	walletHandler := handler.NewWalletHandler(walletService, s.logger)
	transferHandler := handler.NewTransferHandler(transferService, s.logger)
	chatHandler := handler.NewChatHandler(transferService, walletService, authService, llmClient, s.logger)
	bankHandler := handler.NewBankHandler(bankRepo, userRepo, okraClient, monoClient, s.logger)
	webhookHandler := handler.NewWebhookHandler(walletService, paystackClient, s.logger)

	// Routes
	s.router.Get("/health", healthHandler.HealthCheck)

	s.router.Route("/api/v1", func(r chi.Router) {
		authHandler.RegisterRoutes(r)
		walletHandler.RegisterRoutes(r)
		transferHandler.RegisterRoutes(r)
		chatHandler.RegisterRoutes(r)
		bankHandler.RegisterRoutes(r)
		webhookHandler.RegisterRoutes(r)
	})
}

func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	s.http = &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		s.logger.Info("server starting", "addr", addr, "env", s.cfg.Server.Environment)
		if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-quit
	s.logger.Info("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("server forced shutdown: %w", err)
	}

	s.logger.Info("server stopped gracefully")
	return nil
}

// redisIDKeyStore adapts pkg/idempotency.Store to middleware.IDKeyStore.
type redisIDKeyStore struct {
	store *idempotency.Store
}

func (a *redisIDKeyStore) Get(ctx context.Context, namespace, key string) (*middleware.IDKeyResponse, bool, error) {
	resp, found, err := a.store.Get(ctx, namespace, key)
	if err != nil || !found {
		return nil, false, err
	}
	return &middleware.IDKeyResponse{Status: resp.Status, Body: resp.Body}, true, nil
}

func (a *redisIDKeyStore) Set(ctx context.Context, namespace, key string, resp *middleware.IDKeyResponse) error {
	return a.store.Set(ctx, namespace, key, &idempotency.Response{Status: resp.Status, Body: resp.Body})
}
