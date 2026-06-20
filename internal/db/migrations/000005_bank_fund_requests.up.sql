CREATE TABLE bank_fund_requests (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bank_account_id UUID        NOT NULL REFERENCES bank_accounts(id),
    reference       TEXT        NOT NULL UNIQUE,
    amount          BIGINT      NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending','completed','failed')),
    provider_ref    TEXT,
    failure_reason  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ
);

CREATE INDEX idx_bfr_user ON bank_fund_requests (user_id, created_at DESC);
CREATE INDEX idx_bfr_ref  ON bank_fund_requests (reference);
