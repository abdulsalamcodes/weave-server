# ADR 003: Repository Interface Pattern

**Status:** Accepted  
**Date:** 2026-06-14

## Context

The service layer needs to access data without coupling to Postgres or pgx specifics. It also needs to be testable without a real database.

## Decision

Each repository defines an **interface** in `internal/repository/interfaces.go` and provides a **concrete pgx implementation**. Services depend only on the interface.

### Rationale

- Services are testable with mock implementations (`internal/service/mock_test.go`, handler tests)
- Interface is the contract; concrete type can be swapped (e.g., for migration tooling)
- pgx-specific types (`pgx.Rows`, `pgx.Tx`) stay in the repository package
- Transaction propagation via `context.Context` — `repository.WithTx(ctx, tx)` stores the tx, `getQuerier(ctx)` retrieves it

## Consequences

- Every repository method must be declared in the interface before it can be used by a service
- Adding a method to an interface requires updating all mock implementations
- Mock-generating boilerplate is written by hand (no mockgen/mockery) — acceptable for current scale
- Transactional methods need `WithTx`/`getQuerier` helpers to propagate the pgx tx via context
