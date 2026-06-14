# Weave Server — Architecture

## Overview

Weave is a chat-based multi-bank money transfer agent. Users interact via natural language (LLM-parsed intents) to send money. Funds are sourced from the user's wallet balance and/or linked bank accounts (Okra/Mono), then paid out via Paystack.

## Component Map

```
┌──────────┐     ┌──────────────┐     ┌─────────────┐
│  Client   │────▶  HTTP Router  │────▶  Middleware  │
│ (Flutter) │     │   (chi)      │     │  Stack      │
└──────────┘     └──────┬───────┘     └──────┬──────┘
                        │                     │
                        ▼                     ▼
                 ┌──────────────────────────────┐
                 │         Handlers              │
                 │  (handler package)            │
                 │  validate, respond, delegate  │
                 └──────────┬───────────────────┘
                            │
                            ▼
                 ┌──────────────────────────────┐
                 │         Services              │
                 │  (service package)            │
                 │  business logic + orchestrate │
                 └──────────┬───────────────────┘
                            │
              ┌─────────────┼─────────────┐
              ▼             ▼             ▼
     ┌────────────┐ ┌────────────┐ ┌────────────┐
     │ Repository │ │  Provider  │ │  Sourcing  │
     │ (pgx/DB)   │ │  Clients   │ │  Engine    │
     └────────────┘ │(Mono,Okra, │ └────────────┘
                    │ Paystack)  │
                    └────────────┘
```

## Layer Responsibilities

### Handlers (`internal/handler/`)
- Parse and validate HTTP requests
- Call service methods
- Serialize responses (always JSON)
- Route registration via `RegisterRoutes(r chi.Router)`

### Services (`internal/service/`)
- Business logic and orchestration
- Transactional boundaries (pgx transactions)
- Error types (domain errors like `ErrInsufficientFunds`)

### Repositories (`internal/repository/`)
- Data access via pgx queries
- Interface-based → concrete implementation (swap for testing)
- Transaction propagation via `context.Context` (`WithTx`/`getQuerier`)

### Providers (`internal/provider/`)
- External API clients (Mono, Okra, Paystack)
- Each provider has its own package with request/response types
- Webhook signature verification built in

## Key Flows

### Transfer (Send Money)

```
POST /api/v1/transfers
  │
  ├─ TransferService.InitiateTransfer()
  │   ├─ Check idempotency (Idempotency-Key header)
  │   ├─ SourcingEngine.BuildDebitPlan()
  │   │   ├─ Check wallet balance
  │   │   └─ Check linked bank accounts (by priority)
  │   ├─ Begin pgx transaction
  │   ├─ Process debit legs (wallet hold / bank debit)
  │   ├─ Execute payout via Paystack
  │   ├──└─ On failure: release holds, mark failed
  │   ├─ Complete wallet debit (release hold → deduct)
  │   └─ Commit transaction
  └─ Return TransferResult
```

### Chat (NLP Intent)

```
POST /api/v1/chat/message
  │
  ├─ ChatHandler.HandleMessage()
  │   ├─ LLM.ParseIntent("send 5000 to 0123456789")
  │   │   └─ Returns IntentSendMoney + extracted entities
  │   └─ Routes to handleSendMoney / handleCheckBalance etc.
  └─ Returns human-readable response + structured intent
```

### Bank Linking

```
POST /api/v1/banks/link  (provider: "okra" | "mono")
  ├─ Returns connect_url from provider
  │    └─ User completes OAuth flow in provider widget
  │         └─ Provider sends webhook →
POST /api/v1/banks/webhook/{provider}
  ├─ Verify HMAC signature
  ├─ Save bank account to DB
  └─ Return success
```

## Data Model

```
User ──1:1── Wallet
 │
 ├──1:N── BankAccount (linked accounts for sourcing)
 │
 └──1:N── Transaction
           ├── parent (the transfer request)
           └── children (debit legs + payout leg)
```

- **Amount**: stored as `int64` kobo (1 NGN = 100 kobo). All math in kobo.
- **Wallet**: `balance` = total, `ledger_balance` = available (balance minus holds).
- **Transaction**: tree structure — parent is the transfer request, children are debit legs (wallet/bank) and payout leg.
- **BankAccount**: linked via Okra or Mono OAuth flow. `last_balance` cached from provider; can be refreshed.

## Error Handling

- All errors return `{"error":{"code":"...","message":"..."}}`.
- Standard codes defined in `handler/response.go`.
- Domain errors (e.g., `ErrInsufficientFunds`) map to HTTP status + error code in handlers.
- Recovery middleware catches panics and returns JSON instead of plain text.

## Security

- PIN hashed with bcrypt (cost 12), never stored in plaintext
- JWT access tokens (short-lived 15m) + refresh tokens (7d)
- PIN lockout after N failed attempts (IP-tracked)
- Idempotency-Key prevents duplicate transfers
- Webhook signatures verified (HMAC-SHA256 for Okra, HMAC-SHA512 for Mono/Paystack)
- Max body size (1MB) + Content-Type enforcement
- Trace context propagation via W3C TraceContext headers

## Configuration

All config via environment variables. See `.env.example`. No config files.
