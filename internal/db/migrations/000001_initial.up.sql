-- Weave Initial Schema
-- idempotent: each table uses IF NOT EXISTS

BEGIN;

-- ============================================================================
-- Users
-- ============================================================================
CREATE TABLE IF NOT EXISTS users (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone               VARCHAR(20) UNIQUE NOT NULL,
    email               VARCHAR(255) UNIQUE,
    full_name           VARCHAR(255) NOT NULL,
    bvn_hash            VARCHAR(255),       -- SHA-256 hashed, never plaintext
    nin_hash            VARCHAR(255),       -- SHA-256 hashed, never plaintext
    kyc_level           INT NOT NULL DEFAULT 1,
    pin_hash            TEXT NOT NULL,       -- bcrypt
    biometric_enabled   BOOLEAN NOT NULL DEFAULT FALSE,
    biometric_public_key TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_users_phone ON users(phone);

-- ============================================================================
-- User Sessions (refresh tokens)
-- ============================================================================
CREATE TABLE IF NOT EXISTS user_sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token   TEXT NOT NULL UNIQUE,   -- hashed
    device_info     TEXT,
    ip_address      INET,
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_user ON user_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_sessions_refresh ON user_sessions(refresh_token);

-- ============================================================================
-- PIN Attempts (rate limiting for PIN entry)
-- ============================================================================
CREATE TABLE IF NOT EXISTS pin_attempts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    success     BOOLEAN NOT NULL,
    ip_address  INET,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pin_attempts_user ON pin_attempts(user_id, created_at);

-- ============================================================================
-- Wallets
-- ============================================================================
CREATE TABLE IF NOT EXISTS wallets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID REFERENCES users(id) ON DELETE CASCADE,
    type            VARCHAR(20) NOT NULL DEFAULT 'user', -- user, settlement, fee
    balance         BIGINT NOT NULL DEFAULT 0,           -- kobo
    ledger_balance  BIGINT NOT NULL DEFAULT 0,           -- kobo (balance - holds)
    currency        VARCHAR(3) NOT NULL DEFAULT 'NGN',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_user_wallet UNIQUE (user_id),
    CONSTRAINT check_balance_non_negative CHECK (balance >= 0),
    CONSTRAINT check_ledger_balance_non_negative CHECK (ledger_balance >= 0),
    CONSTRAINT check_ledger_lte_balance CHECK (ledger_balance <= balance)
);

-- ============================================================================
-- Wallet Transactions (ledger entries)
-- ============================================================================
CREATE TABLE IF NOT EXISTS wallet_transactions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id   UUID NOT NULL REFERENCES wallets(id),
    type        VARCHAR(10) NOT NULL,    -- credit, debit, hold, release
    amount      BIGINT NOT NULL,         -- kobo
    reference   VARCHAR(100) UNIQUE NOT NULL,
    description TEXT,
    status      VARCHAR(20) NOT NULL DEFAULT 'COMPLETED',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_wallet_tx_wallet ON wallet_transactions(wallet_id);
CREATE INDEX IF NOT EXISTS idx_wallet_tx_ref ON wallet_transactions(reference);

-- ============================================================================
-- Wallet Accounts (issued DVAs via Paystack/Flutterwave)
-- ============================================================================
CREATE TABLE IF NOT EXISTS wallet_accounts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider        VARCHAR(20) NOT NULL,           -- paystack, flutterwave, mono
    provider_ref    VARCHAR(255) NOT NULL,
    account_number  VARCHAR(20) NOT NULL UNIQUE,
    account_name    VARCHAR(255) NOT NULL,
    bank_name       VARCHAR(100) NOT NULL,
    bank_code       VARCHAR(10),
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    is_default      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_wallet_accounts_user ON wallet_accounts(user_id);
CREATE INDEX IF NOT EXISTS idx_wallet_accounts_number ON wallet_accounts(account_number);

-- ============================================================================
-- Wallet Deposits (incoming webhook records)
-- ============================================================================
CREATE TABLE IF NOT EXISTS wallet_deposits (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_account_id UUID REFERENCES wallet_accounts(id),
    user_id           UUID NOT NULL REFERENCES users(id),
    amount            BIGINT NOT NULL,            -- kobo
    fee               BIGINT NOT NULL DEFAULT 0,  -- kobo
    provider          VARCHAR(20) NOT NULL,
    provider_ref      VARCHAR(255) UNIQUE NOT NULL,
    sender_account    VARCHAR(20),
    sender_bank       VARCHAR(100),
    status            VARCHAR(20) NOT NULL DEFAULT 'PENDING', -- PENDING, COMPLETED, FAILED
    reconciled        BOOLEAN NOT NULL DEFAULT FALSE,
    webhook_raw       JSONB,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_wallet_deposits_user ON wallet_deposits(user_id);
CREATE INDEX IF NOT EXISTS idx_wallet_deposits_provider_ref ON wallet_deposits(provider_ref);

-- ============================================================================
-- Bank Accounts (linked via Okra/Mono)
-- ============================================================================
CREATE TABLE IF NOT EXISTS bank_accounts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider            VARCHAR(20) NOT NULL,           -- okra, mono
    provider_token      TEXT NOT NULL,                   -- AES-256-GCM encrypted
    account_number      VARCHAR(20) NOT NULL,
    account_name        VARCHAR(255) NOT NULL,
    bank_code           VARCHAR(10) NOT NULL,
    bank_name           VARCHAR(100) NOT NULL,
    priority            INT NOT NULL DEFAULT 5,         -- 1 = highest
    min_balance         BIGINT NOT NULL DEFAULT 0,      -- kobo
    last_balance        BIGINT,                          -- kobo
    last_balance_fetched_at TIMESTAMPTZ,
    is_active           BOOLEAN NOT NULL DEFAULT TRUE,
    is_verified         BOOLEAN NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_bank_accounts_user ON bank_accounts(user_id);

-- ============================================================================
-- Transactions (multi-leg transfer tracking)
-- ============================================================================
CREATE TABLE IF NOT EXISTS transactions (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id               UUID NOT NULL REFERENCES users(id),
    parent_id             UUID REFERENCES transactions(id),
    type                  VARCHAR(20) NOT NULL,          -- debit_leg, payout_leg, deposit, fee, refund
    amount                BIGINT NOT NULL,               -- kobo
    fee                   BIGINT NOT NULL DEFAULT 0,     -- kobo
    currency              VARCHAR(3) NOT NULL DEFAULT 'NGN',
    status                VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    source_account_id     UUID REFERENCES bank_accounts(id),
    source_provider       VARCHAR(20),                   -- okra, mono, wallet
    recipient_account     VARCHAR(20),
    recipient_bank_code   VARCHAR(10),
    recipient_name        VARCHAR(255),
    provider_ref          VARCHAR(255),                  -- external reference
    our_ref               VARCHAR(100) UNIQUE NOT NULL,  -- Weave reference
    idempotency_key       VARCHAR(255),
    failure_reason        TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at          TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_transactions_user ON transactions(user_id);
CREATE INDEX IF NOT EXISTS idx_transactions_status ON transactions(status);
CREATE INDEX IF NOT EXISTS idx_transactions_parent ON transactions(parent_id);
CREATE INDEX IF NOT EXISTS idx_transactions_idempotency ON transactions(idempotency_key);
CREATE INDEX IF NOT EXISTS idx_transactions_our_ref ON transactions(our_ref);
CREATE INDEX IF NOT EXISTS idx_transactions_provider_ref ON transactions(provider_ref);

-- ============================================================================
-- Audit Log (immutable append-only)
-- ============================================================================
CREATE TABLE IF NOT EXISTS audit_logs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID REFERENCES users(id),
    action      VARCHAR(100) NOT NULL,
    metadata    JSONB,
    ip_address  INET,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_user ON audit_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON audit_logs(action);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON audit_logs(created_at);

-- ============================================================================
-- Idempotency Keys (for idempotent API calls)
-- ============================================================================
CREATE TABLE IF NOT EXISTS idempotency_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key             VARCHAR(255) NOT NULL UNIQUE,
    response_status INT NOT NULL,
    response_body   JSONB,
    response_headers JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '24 hours')
);

CREATE INDEX IF NOT EXISTS idx_idempotency_keys_key ON idempotency_keys(key);

-- ============================================================================
-- Function: updated_at trigger
-- ============================================================================
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Apply to tables with updated_at
DO $$
DECLARE
    tbl TEXT;
BEGIN
    FOR tbl IN
        SELECT unnest(ARRAY['users', 'wallets', 'wallet_accounts', 'bank_accounts'])
    LOOP
        EXECUTE format(
            'CREATE TRIGGER set_%I_updated_at BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()',
            tbl, tbl
        );
    END LOOP;
END;
$$;

COMMIT;
