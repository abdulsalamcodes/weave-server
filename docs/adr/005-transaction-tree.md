# ADR 005: Transaction Tree Model

**Status:** Accepted  
**Date:** 2026-06-14

## Context

A single money transfer can involve multiple funding sources (wallet + bank account) and a payout. The system needs to track each leg independently for audit, reconciliation, and failure handling.

## Decision

Transfers use a **parent-child tree model**:
- **Parent transaction**: the transfer request (type `debit_leg` at the top level)
- **Child transactions**: individual legs — debit from wallet, debit from bank, payout to recipient

### Rationale

- Each leg has independent status tracking (one leg can fail without affecting others)
- Clear audit trail for reconciliation
- Payout leg references the parent transfer for grouping
- Supports future features: partial refunds, per-leg fees, multi-source transfers

## Consequences

- `parent_id` nullable UUID field on the `transactions` table
- Parent is created first, then children are linked via `parent_id`
- `TransactionFilter` can filter by type to get just transfers, just deposits, etc.
- Payout failure rolls back the entire transfer (all legs reversed)
- Child transactions can be retrieved with `GetByParentID`
