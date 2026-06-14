# Weave Server — Agent Guide

## Quick Start

```sh
make run              # start server
make test             # test with -race
make build            # production binary
make vet              # go vet
make tidy             # mod tidy + verify
```

## Module

`github.com/abdulsalamcodes/weave-server` — Go 1.26.1

## Project Layout

```
cmd/server/main.go    # entry point
internal/
  config/             # env-based config loading
  db/                 # pgx pool + redis + migrations
  handler/            # chi HTTP handlers (routes, request/response types)
  middleware/         # chi middleware (auth, tracing, idempotency, etc.)
  model/              # domain types (Amount, Transaction, Wallet, etc.)
  provider/           # external API clients (mono/, okra/, paystack/)
  repository/         # data access (pgx queries)
  server/             # wiring (handler -> service -> repo)
  service/            # business logic
pkg/
  httpclient/         # shared HTTP client
  idempotency/        # Redis-backed idempotency store
  logger/             # structured JSON logger
  telemetry/          # OpenTelemetry tracing
  validator/          # custom validators
```

## Conventions

- **Amount**: `model.Amount` is `int64` in kobo (1 NGN = 100 kobo). Use `model.NewAmount(ngn)` to convert.
- **DB**: pgx/v5 with `pgxpool.Pool`. Transactions via `repository.WithTx(ctx, tx)`.
- **Router**: chi/v5. All business routes under `/api/v1`.
- **Auth**: JWT bearer tokens. Middleware injects `UserID` into context.
- **Errors**: All API errors return `{"error":{"code":"...","message":"..."}}`. Error codes in `handler/response.go`.
- **Idempotency**: HTTP `Idempotency-Key` header. Redis-backed, missing Redis falls back to in-memory.
- **Logging**: `slog` structured logger with request ID propagation.
- **Testing**: Standard `testing` package. Mocks in `internal/service/mock_test.go`. Integration tests use `httptest`.
- **Middleware order**: RealIP → Tracing → RequireJSON → MaxBody → SecurityHeaders → RequestID → Recovery → Logging → CORS → RateLimit → Idempotency → Auth → Timeout

## Test Rules

- Always run `go build ./...` then `go test -race -count=1 -short ./internal/...` before committing.
- Add `ListByUserID`/`CountByUserID` to mock repos when testing handlers that list transactions.
- Mocks live in `internal/service/mock_test.go` (service layer) and `internal/handler/transfer_test.go` (handler layer).

## Provider Clients

- Mono: `internal/provider/mono/client.go` (HMAC-SHA512 webhooks)
- Okra: `internal/provider/okra/client.go` (HMAC-SHA256 webhooks)
- Paystack: `internal/provider/paystack/client.go` (HMAC-SHA512 webhooks)

## Key Contracts

- Services depend on repository interfaces (`internal/repository/interfaces.go`), not concrete types.
- `SourcingEngine` depends on `WalletBalanceProvider` interface (decoupled from `WalletService`).
- `TransferService` needs `bankRepo` and provider clients to execute bank account debits.

## When Adding A New Endpoint

1. Add model types if new domain objects needed.
2. Add repo interface + implementation.
3. Add service method.
4. Add handler with route registration.
5. Wire in `internal/server/server.go`.
6. Update tests: mock repo + handler/handler test.
7. Update `docs/openapi.yaml`.
