# ADR 002: Kobo-Based Amount Representation

**Status:** Accepted  
**Date:** 2026-06-14

## Context

Financial applications must handle currency amounts precisely. Options:
- **Floats** — `float64` (imprecise, rounding errors)
- **Decimal library** — `shopspring/decimal` etc. (additional dependency, complexity)
- **Integer kobo** — store as smallest unit (int64)

## Decision

All amounts are `model.Amount` (type `int64`), representing kobo (1 NGN = 100 kobo).

### Rationale

- No floating-point rounding errors
- No external dependency needed
- Simple math (addition, subtraction are int64 ops)
- Efficient storage and indexing in Postgres (`BIGINT`)
- Pattern used by Stripe, Paystack, and other payments platforms

## Consequences

- Input/output conversion at API boundaries:
  - `POST /transfers` receives NGN as float, converted via `model.NewAmount(ngn int64)`
  - `GET /wallet` returns kobo as int; client divides by 100
- All internal math is in kobo
- `Amount.NGN()` returns `float64` for display purposes only
- Must guard against overflow (max safe kobo = ~9.2e18, or ~9.2e16 NGN — sufficient for this use case)
