# Weave-Server Audit Report

## Executive Summary

**Grade: C** (early-production with serious gaps)

The project is a fintech backend for Nigerian money transfers — well-structured architecturally but lacking the hardening, testing depth, and operational readiness required for production financial services.

**Top 3 Risks:**
1. **No CI/CD pipeline.** Every deployment is a manual process. No automated tests gate merges. A bad push could take the system down undetected.
2. **Webhook handlers trust unauthenticated payloads.** Okra/Mono webhooks accept any POST without signature verification — an attacker can forge bank-link events to inject arbitrary bank accounts.
3. **Transfer flow has no database transactions.** Wallet hold, payout creation, and wallet debit are individual DB calls. If the process crashes after payout but before debit, money is lost (or conversely, a debit without a payout).

**Top 3 Opportunities:**
1. Wire the existing `pkg/idempotency.Redis` store instead of the in-memory one in production; add `github.com/go-playground/validator` to handler validation middleware.
2. Extract a shared `httpclient` abstraction to eliminate the 4x duplication of timeout/client construction.
3. Add CI (GitHub Actions) with `go test -race`, `golangci-lint`, and `go vet`.

---

## Repo Map

**Purpose:** Multi-bank money transfer agent using chat-based AI interaction. Users register, link bank accounts or get a Paystack DVA, execute transfers via wallet-first debit plans.

**Stack:** Go 1.26.1 / Chi router / pgx PostgreSQL / go-redis / Paystack (payouts + DVA) / Okra + Mono (bank linking) / OpenAI (intent parsing)

**Maturity:** Late-prototype / early-production (~5 commits, no CI, no README, one SQL migration, 44 Go files, 6,716 lines)

```
weave-server/
├── cmd/server/main.go          # Entry point: config → DB + Redis → migrations → server start
├── internal/
│   ├── config/config.go        # Env-driven config (19 vars)
│   ├── db/                     # Postgres pool, Redis client, embedded migration runner
│   │   └── migrations/000001_initial.up.sql  # Full schema (10 tables + triggers)
│   ├── handler/                # HTTP handlers (auth, wallet, transfer, chat, bank, webhook)
│   ├── middleware/             # Chi middleware: auth JWT, rate-limiter, idempotency, logging, recovery, CORS, security headers, timeout
│   ├── model/                  # Domain types: User, Wallet, Transaction, Amount, BankAccount
│   ├── provider/               # External API clients: Paystack, Okra, Mono, OpenAI/LLM
│   ├── repository/             # Interface definitions + Postgres implementations
│   ├── server/server.go        # Router assembly + graceful shutdown
│   └── service/                # Business logic: auth, wallet, transfer, payout, sourcing engine
├── pkg/
│   ├── idempotency/store.go    # Redis + in-memory idempotency stores (NOT WIRED)
│   ├── logger/logger.go        # Structured JSON logger wrapper
│   └── validator/validator.go  # go-playground validator with custom rules
├── bin/weave-server            # PREBUILT BINARY COMMITTED (12.4 MB)
├── Makefile                    # build, test, lint, docker targets
├── Dockerfile                  # Multi-stage static build → Alpine
├── docker-compose.yml          # Postgres + Redis + API
└── .env.example                # 45-line env template
```

---

## Audit Report

### Architecture & Design

| # | Finding | File:Line | Why it matters | Severity |
|---|---------|-----------|----------------|----------|
| A1 | **No database transactions in transfer flow.** `Hold` → `Create debit leg` → `payout` → `Debit` are four separate DB calls. A crash after payout but before debit creates a money leak. | `service/transfer_service.go:119-205` | **Financial loss.** The hold is released on failure but the payout may have already been sent. | **Critical** |
| A2 | **Two parallel idempotency store implementations; production uses the in-memory one.** `pkg/idempotency` has a Redis-backed store, but `server.go` wires `middleware.NewInMemoryIDKeyStore` (no Redis, no persistence, no TTL expiry). | `server/server.go:68` vs `pkg/idempotency/store.go:23` | **Idempotency guarantees lost on restart.** A restart clears all cached idempotency keys, allowing duplicate transfers. Config drift: the code exists but isn't used. | **High** |
| A3 | **`api/` dir is empty.** No OpenAPI spec, no Protobuf, no client SDK. | `api/` | **Undocumented API surface.** Consumers must reverse-engineer endpoints from handler code. | **Medium** |
| A4 | **SourcingEngine is tightly coupled to WalletService (concrete type).** `BuildDebitPlan` takes `*WalletService` but only uses `GetBalance`. This forced the test to construct a full `WalletService` with nil paystack just to test debit planning. | `service/wallet_service.go:270-275` | **Testability friction; prevents isolated unit testing.** The sourcing engine can't be tested without constructing the entire wallet service stack. | **Medium** |
| A5 | **`pkg/idempotency/` is dead code.** The Redis `Store` and `InMemoryStore` types are never imported or referenced by any file outside the package. | `pkg/idempotency/store.go` | **Maintenance drag.** 90 lines of code to maintain that do nothing. Creates confusion about which idempotency implementation is active. | **Medium** |
| A6 | **Wallet creation and user creation are not in a DB transaction.** If wallet creation fails after user creation, we have orphan users. | `service/auth_service.go:80-89` | **Data integrity risk.** In production, a DB hiccup during registration creates orphan user rows with no wallet. | **Medium** |

### Code Quality

| # | Finding | File:Line | Why it matters | Severity |
|---|---------|-----------|----------------|----------|
| C1 | **`UpdateStatus` always sets `completed_at` even for non-terminal statuses.** Setting `completed_at` on a PENDING → PROCESSING update will overwrite a previously set value and falsely indicate completion time. | `repository/transaction_repo.go:147` | **Audit trail corruption.** Any non-final status update stamps `completed_at`, making it impossible to trust this field. | **High** |
| C2 | **Duplicate scan code across 4 query methods in TransactionRepo.** `GetByID`, `GetByOurRef`, `GetByIdempotencyKey`, `GetByParentID` each repeat the same 15-field scan. | `repository/transaction_repo.go:60-141,166-189` | **Maintenance hazard.** Adding a column requires editing 4 identical scan blocks. Past bugs in scan arg order suggest fragility. | **Medium** |
| C3 | **4 copies of `http.Client{Timeout: 30*time.Second}` in provider packages.** Paystack, Okra, Mono, and LLM each construct identical clients. | `provider/paystack/client.go:30`, `okra/client.go:25`, `mono/client.go:23`, `llm/client.go:28` | **Config drift risk.** Changing timeout requires 4 edits. No centralized HTTP configuration or retry logic. | **Low** |
| C4 | **`fmt.Println("bye")` in production entry point.** | `cmd/server/main.go:66` | **Minor, but signals premature/polish lack.** Dead code in the production binary's startup path. | **Low** |
| C5 | **`slog.NewTextHandler(nil, ...)` in test helpers.** A nil writer causes panics on log output. | `service/auth_service_test.go:12`, `middleware/middleware_test.go:19` | **Test fragility.** If a test triggers a log statement (e.g., error log on duplicate key), it panics instead of failing gracefully. | **Low** |
| C6 | **`pkg/validator/init()` uses global state.** `var validate` is set in `init()`, making it impossible to inject a different validator in tests. | `pkg/validator/validator.go:12-18` | **Untestable validation logic.** The custom validators cannot be unit-tested in isolation. | **Low** |

### Security

| # | Finding | File:Line | Why it matters | Severity |
|---|---------|-----------|----------------|----------|
| S1 | **Okra and Mono webhook handlers do not verify signatures.** Any POST to `/banks/webhook/okra` or `/banks/webhook/mono` is accepted and creates a verified bank account. | `handler/bank.go:134-217` | **Account takeover.** An attacker can forge a webhook to link any bank account to any user. This is a critical vulnerability in a financial application. | **Critical** |
| S2 | **CORS configured with `["*"]`.** Allows any origin to make authenticated requests from arbitrary web pages. | `server/server.go:59` | **CSRF risk.** While the API uses JWT Bearer tokens (not cookies), browser-based clients could be vulnerable to token exfiltration or abuse if credentials are accessible. | **Medium** |
| S3 | **Paystack webhook handler reads body twice.** First for signature verification (reads to exhaustion), then resets with `NopCloser`. The second decode via `json.NewDecoder` works correctly, but this pattern is fragile. | `handler/wallet.go:120-136` | **Reliability risk.** If `VerifyWebhook` changes or a middleware reads the body, the handler silently breaks. | **Medium** |
| S4 | **No request body size limits on any endpoint.** Large payloads can exhaust server memory. | All handlers | **DoS risk.** An attacker can send multi-MB POST bodies to any endpoint. | **Medium** |
| S5 | **`provider_token` is stored unencrypted in the in-memory test mock.** While the production DB stores encrypted text (per schema comment), the mock stores plaintext. | `service/mock_test.go:210` | **Low (test code).** But no encryption is verified at the repository layer — nothing enforces that the production code actually encrypts. | **Low** |
| S6 | **PIN lockout bypass on `VerifyPIN`: no IP-based lockout.** Lockout is purely per-user across all IPs. An attacker can perform distributed guessing across IPs to evade IP bans. | `service/auth_service.go:134-157` | **Brute-force risk.** 3 attempts per 15 min per user across all IPs — easy to script across proxies. | **Medium** |

### Testing

| # | Finding | File:Line | Why it matters | Severity |
|---|---------|-----------|----------------|----------|
| T1 | **No integration tests.** All tests are unit tests with mocks. The actual SQL queries, DB constraints, and provider API calls are untested. | Entire repo | **Production failures guaranteed.** The first real DB call or Paystack API interaction will surface issues the unit tests can't catch. | **Critical** |
| T2 | **Zero tests for handlers.** All 7 handler files have no tests. HTTP routing, request validation, error responses, and webhook processing are untested. | `handler/*.go` | **Entire HTTP surface is blind.** A misrouted endpoint, wrong status code, or broken validation goes undetected. | **High** |
| T3 | **Zero tests for provider clients.** Paystack, Okra, Mono, and LLM clients have no tests (unit or integration). | `provider/*/*.go` | **External integrations untested.** Any API contract change by a provider breaks the system silently. | **High** |
| T4 | **Zero tests for repository implementations.** Actual SQL queries and DB interactions are untested. | `repository/*.go` | **SQL errors surface only at runtime.** Syntax errors, constraint violations, column mismatches are never caught. | **High** |
| T5 | **Test coverage is narrow — no edge cases tested for PIN change, token refresh, KYC update.** Auth service covers register/login/lockout/verify but not ChangePIN, RefreshToken. | `service/auth_service_test.go` | **Incomplete coverage of critical auth paths.** Token refresh and PIN change bugs go undetected. | **Medium** |

### Performance

| # | Finding | File:Line | Why it matters | Severity |
|---|---------|-----------|----------------|----------|
| P1 | **No pagination on list endpoints.** `GET /banks` and `GET /transfers` return all records unbounded. | `handler/bank.go:220`, `handler/transfer.go:89` | **OOM risk with real data.** A user with thousands of transactions consumes unbounded server memory on each request. | **Medium** |
| P2 | **`GetByParentID` returns full rows immediately.** Could be heavy for multi-leg transfers. | `repository/transaction_repo.go:110` | **Minor.** Most transfers have 2-3 legs. But no limit mechanism exists. | **Low** |
| P3 | **Rate limiter uses token bucket but doesn't clean up stale buckets.** | `middleware/auth.go:134-159` | **Memory leak.** Each unique IP/user creates a bucket entry that lives forever. Under sustained traffic, this grows unbounded. | **Low** |
| P4 | **`releaseHold` errors are silently discarded in the transfer failure path.** The release could fail, leaving funds stuck on hold. | `service/transfer_service.go:181` | **Wallet balance drift.** Failed transfers can permanently reduce available balance. | **Medium** |

### Dependencies

| # | Finding | File:Line | Why it matters | Severity |
|---|---------|-----------|----------------|----------|
| D1 | **No CI/CD pipeline.** No `.github/workflows/` or CI config. | — | **No automated tests, linting, or security scanning.** Every deployment is manual and blind. | **Critical** |
| D2 | **Prebuilt binary (12.4 MB) committed to repo.** | `bin/weave-server` | **Repository bloat + security risk.** Binary diffs accumulate in git history. Impossible to audit what's actually in the binary. | **Medium** |
| D3 | **`go.sum` is present but no automated check to keep it healthy.** | `go.sum` | **Supply chain risk.** Without CI, there's no automated `go mod verify` or vulnerability scanning. | **Low** |

### DevEx & Operations

| # | Finding | File:Line | Why it matters | Severity |
|---|---------|-----------|----------------|----------|
| O1 | **No graceful handling of Redis being unavailable.** `rdb = nil` is set but the `pkg/idempotency.Redis` store is never wired anyway. The server runs without caching entirely. | `cmd/server/main.go:52` | **Operational surprise.** Admin expects Redis to be used but it's completely bypassed. | **Medium** |
| O2 | **`Recovery` middleware sends `text/plain` for panics.** Uses `http.Error` which sets `Content-Type: text/plain; charset=utf-8`, breaking JSON contract. | `middleware/recovery.go:19` | **Client confusion.** API clients expecting JSON receive plain text on panics. | **Low** |
| O3 | **No structured error response in recovery path.** Returns `{"error":"internal_server_error"}` as plain text — but the Content-Type header says `text/plain`. | `middleware/recovery.go:19` | **API contract violation.** All endpoints should return JSON. | **Low** |
| O4 | **Hot-reload config (`air`) is not committed.** `make run-hot` uses air defaults. | `Makefile` | **Developer friction.** Hot-reload behavior is not reproducible across team members. | **Low** |

### Documentation

| # | Finding | File:Line | Why it matters | Severity |
|---|---------|-----------|----------------|----------|
| M1 | **No README.md.** No description, setup instructions, architecture overview, or deployment guide. | — | **Onboarding blocked.** New devs must reverse-engineer the entire project from source. | **High** |
| M2 | **No CONTRIBUTING.md or ADRs.** | — | **No architectural decision history.** Future maintainers don't know why choices were made. | **Medium** |
| M3 | **`.env.example` is present and well-maintained.** Good documentation of all 19 env vars with sensible defaults. | `.env.example` | **Strength.** This is the best-documented file in the repo. | N/A (strength) |

### Strengths (What to Preserve)

1. **Clean architectural layering** — handler → service → repository separation with interface contracts is textbook Go clean architecture. The compile-time interface checks (`var _ repository.UserRepository = (*UserRepo)(nil)`) are excellent practice.
2. **Graceful degradation** — all provider clients are optional; missing API keys produce warnings, not crashes. The `paystackClient == nil` checks throughout prevent nil-ptr panics.
3. **Idempotency-first design** — transfer initiation checks idempotency keys before execution. This is correct and essential for financial operations.
4. **PIN lockout** — bcrypt + attempt tracking with configurable thresholds is the right approach.
5. **Request-scoped logging** — request ID propagation, structured slog attributes, log-level routing (error on 5xx, warn on 4xx).
6. **Security headers** — `X-Content-Type-Options`, `X-Frame-Options`, HSTS, CSP, `Cache-Control: no-store` are correctly set.
7. **Graceful shutdown** — SIGINT/SIGTERM handling with 30s drain timeout.
8. **Embedded migrations** — SQL files embedded in the binary (zero external migration tool dependency), transactional application.
9. **Race detector in test config** — `go test -race` in the Makefile is a sign of quality awareness.
10. **Tests exist with mocks** — 33 passing tests with a comprehensive mock suite covering the core service layer.

---

## Task Plan

### Milestone 0 — Safety Net (do first)

| # | Task | Files | Acceptance | Effort | Risk |
|---|------|-------|------------|--------|------|
| M0.1 | **Add GitHub Actions CI** — `go test -race ./...`, `go vet ./...`, `golangci-lint` on push/PR | `.github/workflows/ci.yml` | CI passes; PRs blocked on failures | S | Low |
| M0.2 | **Add README** — setup, run, test, deploy instructions | `README.md` | New dev can run the project in <5 min | M | Low |
| M0.3 | **Integration test for transfer flow** — httptest-based test that exercises a complete transfer route | `internal/handler/transfer_test.go` | Test asserts 201, correct balance change, transaction records created | M | Low |
| M0.4 | **Add request body size limit middleware** — reject payloads > 1MB | `internal/middleware/security.go` | No endpoint accepts >1MB body | S | Low |

### Milestone 1 — Critical Fixes

| # | Task | Files | Acceptance | Effort | Risk |
|---|------|-------|------------|--------|------|
| M1.1 | **Add Okra webhook signature verification** — verify HMAC-SHA256 signature via Okra secret | `internal/handler/bank.go` | Forged webhooks are rejected with 401 | M | Low |
| M1.2 | **Add Mono webhook signature verification** — verify HMAC signature via Mono secret | `internal/handler/bank.go` | Forged webhooks are rejected with 401 | M | Low |
| M1.3 | **Wrap transfer flow in pgx transaction** — `Begin`/`Commit`/`Rollback` around multi-step transfer | `internal/service/transfer_service.go` | No partial transfers; rollback on failure releases all holds | L | Medium |
| M1.4 | **Fix `UpdateStatus` to conditionally set `completed_at`** — only for Completed/Failed | `internal/repository/transaction_repo.go` | `completed_at` is NULL for non-terminal statuses | S | Low |
| M1.5 | **Wire Redis idempotency store** — use `pkg/idempotency.NewStore` instead of in-memory | `internal/server/server.go` | Idempotency keys survive server restart | S | Low |

### Milestone 2 — High-Leverage Improvements

| # | Task | Files | Acceptance | Effort | Risk |
|---|------|-------|------------|--------|------|
| M2.1 | **Extract shared HTTP client package** — single `pkg/httpclient` with timeout + retry | `pkg/httpclient/client.go`, all `provider/*/client.go` | All 4 providers use shared client | M | Low |
| M2.2 | **Deduplicate TransactionRepo scan** — extract `scanTransaction` helper | `internal/repository/transaction_repo.go` | All 4 query methods call same scan function | S | Low |
| M2.3 | **Add handler integration tests** — httptest.Server tests for all handler endpoints | `internal/handler/*_test.go` | All endpoints tested at HTTP level | XL | Low |
| M2.4 | **Add pagination to list endpoints** — limit/offset on GET /banks, GET /transfers | `handler/bank.go`, `handler/transfer.go`, `repository/*` | Endpoints accept `?limit=50&offset=0` | M | Low |
| M2.5 | **Remove `bin/weave-server` from git** — .gitignore + delete tracked binary | `.gitignore` | `bin/` is gitignored, binary deleted from history | S | Low |

### Milestone 3 — Quality & Polish

| # | Task | Files | Acceptance | Effort | Risk |
|---|------|-------|------------|--------|------|
| M3.1 | **Fix recovery middleware JSON** — return `{"error":...}` with correct Content-Type | `internal/middleware/recovery.go` | Panics return JSON | S | Low |
| M3.2 | **Add PIN change and refresh token tests** | `internal/service/auth_service_test.go` | Auth service 100% method coverage | S | Low |
| M3.3 | **Remove `fmt.Println("bye")`** from main.go | `cmd/server/main.go` | Clean startup/shutdown | S | Low |
| M3.4 | **Fix test logger to use `io.Discard`** instead of nil writer | `service/auth_service_test.go`, `middleware/middleware_test.go` | Tests don't panic on log writes | S | Low |
| M3.5 | **Add Content-Type enforcement middleware** — reject non-JSON POST/PUT | `internal/middleware/security.go` | Non-JSON requests get 415 | S | Low |
| M3.6 | **Remove dead `pkg/idempotency` or wire it** | `pkg/idempotency/`, `internal/server/server.go` | No dead code | S | Low |

### Quick Wins (S effort, high impact)
- M1.5: Wire Redis idempotency store (1 line change)
- M1.4: Fix `completed_at` conditional (3 lines)
- M2.2: Deduplicate scan logic (30 mins)
- M2.5: Remove binary from git (5 mins)
- M3.1: Recovery middleware JSON fix (2 lines)
- M3.3: Remove `fmt.Println` (1 line)
- M3.4: Fix test logger nil→Discard (2 lines each)
- M0.1: GitHub Actions CI (30 mins skeleton)
