# ADR 001: Wallet-First Funding Model

**Status:** Accepted  
**Date:** 2026-06-14

## Context

Weave needs to fund outbound transfers. Two possible models:
- **Wallet-first**: users deposit money into a Weave wallet, transfers deduct from it
- **Direct-debit-first**: transfers pull directly from linked bank accounts via Okra/Mono

## Decision

Wallet-first. Transfers source from the wallet balance first, then fall back to linked bank accounts.

### Rationale

- **Trust building**: Users are hesitant to let a new app pull directly from their bank. A wallet creates a familiar prepaid mental model.
- **Settlement simplicity**: Wallet balance is local (Postgres); no external API dependency during transfer execution for the primary funding leg.
- **PSP integration**: Paystack provides virtual accounts for easy wallet top-ups; the same PSP handles payouts.
- **Bank linking as power-user feature**: After trust is established, users can link accounts for automatic top-up or direct sourcing.

## Consequences

- `SourcingEngine` always checks wallet first, bank accounts second
- Users must deposit before they can send (friction but safer)
- Bank account `last_balance` is cached; real-time refresh is a manual action
