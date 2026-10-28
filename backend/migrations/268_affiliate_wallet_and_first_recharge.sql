-- Every existing and future account has a code, independently of campaign visibility.
INSERT INTO user_affiliates (user_id, aff_code)
SELECT id, 'YZ' || UPPER(SUBSTRING(MD5('yingzo-affiliate:' || id::text), 1, 24)) FROM users
ON CONFLICT DO NOTHING;

CREATE OR REPLACE FUNCTION initialize_user_affiliate() RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO user_affiliates (user_id, aff_code)
    VALUES (NEW.id, 'YZ' || UPPER(SUBSTRING(MD5('yingzo-affiliate:' || NEW.id::text), 1, 24)))
    ON CONFLICT (user_id) DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER users_initialize_affiliate AFTER INSERT ON users
FOR EACH ROW EXECUTE FUNCTION initialize_user_affiliate();

CREATE TABLE affiliate_withdrawals (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    amount DECIMAL(20,8) NOT NULL CHECK (amount > 0),
    alipay_account VARCHAR(128) NOT NULL,
    alipay_name VARCHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'rejected')),
    admin_note VARCHAR(500) NOT NULL DEFAULT '',
    processed_by BIGINT REFERENCES users(id),
    processed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_affiliate_withdrawals_user ON affiliate_withdrawals(user_id, id DESC);
CREATE INDEX idx_affiliate_withdrawals_status ON affiliate_withdrawals(status, id DESC);

-- One claim per user; bonus credit and claim commit in the same transaction.
CREATE TABLE affiliate_first_recharges (
    user_id BIGINT PRIMARY KEY REFERENCES users(id),
    order_id BIGINT NOT NULL UNIQUE REFERENCES payment_orders(id),
    bonus_amount DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (bonus_amount >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
