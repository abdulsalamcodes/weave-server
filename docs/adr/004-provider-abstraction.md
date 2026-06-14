# ADR 004: Provider Abstraction Strategy

**Status:** Accepted  
**Date:** 2026-06-14

## Context

Weave integrates with multiple external providers in two categories:
- **Bank aggregation**: Mono and Okra (interchangeable for linking + balance + debit)
- **PSP**: Paystack (primary), possible future alternatives

Each has different APIs, auth methods, and webhook signature schemes.

## Decision

Each provider gets its own package under `internal/provider/` with its own client types and request/response structs. No shared provider interface.

### Rationale

- Mono and Okra have fundamentally different API shapes (REST vs different endpoints, different auth headers)
- Forcing a common interface would leak abstraction or require `interface{}` everywhere
- The **SourcingEngine** already abstracts the funding logic; provider choice is a detail
- Webhook handling is per-provider in the handler layer
- Provider-specific structs are self-documenting

## Consequences

- Adding a new provider means creating a new package and wiring it in `server.go`
- No generic `Provider` interface — each service that needs a provider gets it directly
- Provider initialization is nil-safe: if `MONO_SECRET_KEY` is empty, no mono client is created
- Handler webhook routes are registered per-provider (`/banks/webhook/okra`, `/banks/webhook/mono`)
- Provider clients have nil checks everywhere before use
