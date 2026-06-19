ALTER TABLE bank_accounts
    ADD CONSTRAINT uq_bank_accounts_user_account UNIQUE (user_id, account_number);
