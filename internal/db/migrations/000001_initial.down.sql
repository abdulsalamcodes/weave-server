BEGIN;

DROP TRIGGER IF EXISTS set_users_updated_at ON users;
DROP TRIGGER IF EXISTS set_wallets_updated_at ON wallets;
DROP TRIGGER IF EXISTS set_wallet_accounts_updated_at ON wallet_accounts;
DROP TRIGGER IF EXISTS set_bank_accounts_updated_at ON bank_accounts;

DROP FUNCTION IF EXISTS update_updated_at_column;

DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS bank_accounts;
DROP TABLE IF EXISTS wallet_deposits;
DROP TABLE IF EXISTS wallet_accounts;
DROP TABLE IF EXISTS wallet_transactions;
DROP TABLE IF EXISTS wallets;
DROP TABLE IF EXISTS pin_attempts;
DROP TABLE IF EXISTS user_sessions;
DROP TABLE IF EXISTS users;

COMMIT;
