ALTER TABLE user_affiliate_ledger
    ADD COLUMN reversed_amount DECIMAL(20,8) NOT NULL DEFAULT 0
    CHECK (reversed_amount >= 0 AND reversed_amount <= amount);

ALTER TABLE affiliate_first_recharges
    ADD COLUMN reversed_amount DECIMAL(20,8) NOT NULL DEFAULT 0
    CHECK (reversed_amount >= 0 AND reversed_amount <= bonus_amount);

-- A negative signed wallet balance is a recovery debt, never spendable quota.
-- Future available rebates and matured frozen rebates repay it automatically.
COMMENT ON COLUMN user_affiliates.aff_quota IS 'Signed available rebate balance: negative values are refund recovery debt';
COMMENT ON COLUMN user_affiliate_ledger.reversed_amount IS 'Cumulative amount revoked from this accrual by successful refunds';
