# Weave Server

Chat-based multi-bank money transfer agent for Nigeria. Backend API written in Go.

## Stack

- **Language:** Go 1.26
- **Database:** PostgreSQL 16
- **Cache:** Redis 7
- **Bank Aggregation:** Okra, Mono
- **PSP/Payouts:** Paystack
- **Container:** Docker + Compose

## Quick Start

```bash
cp .env.example .env
docker compose up -d
make migrate-up
make run
```

Server starts at `http://localhost:8080`.

## Available Commands

| Command | Description |
|---------|-------------|
| `make build` | Build binary to `bin/weave-server` |
| `make run` | Run locally |
| `make run-hot` | Run with hot reload (air) |
| `make test` | Run all tests with race detector |
| `make lint` | Run golangci-lint |
| `make vet` | Run `go vet ./...` |
| `make migrate-up` | Apply DB migrations |
| `make migrate-down` | Rollback DB migrations |
| `make docker-build` | Build Docker image |
| `make docker-run` | Start services via docker compose |
| `make clean` | Remove build artifacts |

## Project Structure

```
├── cmd/server/           # Entry point
├── internal/
│   ├── config/           # Env-based config
│   ├── db/               # PostgreSQL connection + migrations
│   ├── handler/          # HTTP handlers
│   ├── middleware/        # Chi middleware (auth, rate-limit, CORS, etc.)
│   ├── model/            # Domain models + validation
│   ├── provider/
│   │   ├── llm/          # OpenAI function-calling
│   │   ├── mono/         # Mono bank provider
│   │   ├── okra/         # Okra bank provider
│   │   └── paystack/     # Paystack PSP integration
│   ├── repository/       # Data access layer (pgx)
│   └── service/          # Business logic
├── migrations/           # Embedded SQL migrations
├── pkg/                  # Shared utilities
└── .github/workflows/    # CI pipeline
```

## API Endpoints

### Auth
- `POST /api/v1/auth/register` — Register with phone + PIN
- `POST /api/v1/auth/login` — Login with phone + PIN
- `POST /api/v1/auth/verify-pin` — Verify PIN for sensitive actions
- `POST /api/v1/auth/refresh` — Refresh access token
- `PUT /api/v1/auth/pin` — Change PIN

### Wallet
- `GET /api/v1/wallet` — Get wallet balance
- `POST /api/v1/wallet/account` — Create DVA (virtual account)

### Transfers
- `POST /api/v1/transfers` — Initiate transfer (idempotency-key header)
- `GET /api/v1/transfers/{id}` — Get transfer by ID
- `GET /api/v1/transfers/ref/{ourRef}` — Get transfer by reference

### Bank Linking
- `POST /api/v1/banks/link/okra` — Get Okra connect URL
- `POST /api/v1/banks/link/mono` — Get Mono connect URL
- `GET /api/v1/banks` — List linked banks
- `PUT /api/v1/banks/{id}/priority` — Update bank priority
- `DELETE /api/v1/banks/{id}` — Unlink bank

### Webhooks
- `POST /webhooks/paystack` — Paystack transaction events
- `POST /webhooks/okra` — Okra account events
- `POST /webhooks/mono` — Mono account events

### Chat
- `POST /api/v1/chat` — Natural language money transfer

## Testing

```bash
# All tests with race detector
make test

# Specific package
go test -race -count=1 ./internal/service

# Without race
go test ./...
```

The CI pipeline (`.github/workflows/ci.yml`) runs `go vet`, tests with race detector, build, and `go mod tidy` check on every push/PR to `main`.

## Deployment

```bash
# Build image
docker build -t weave-server .

# Run with compose
docker compose up -d
```

Set required environment variables in production (see `.env.example` for the full list).
