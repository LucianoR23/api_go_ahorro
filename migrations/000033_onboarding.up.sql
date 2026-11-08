-- Plan 1.3: new onboarding.
--
-- A card is network x issuer (Visa Galicia is not Visa Santander). The bank
-- already lives in payment_methods.bank_id; this adds the network. Nullable:
-- cards created before 1.3, or by someone who skips it, have none.
ALTER TABLE credit_cards
    ADD COLUMN network TEXT
        CONSTRAINT credit_cards_network_check
        CHECK (network IN ('visa', 'mastercard', 'amex', 'cabal', 'naranja', 'maestro', 'other'));

-- When the user finished or skipped onboarding. NULL sends them to it.
-- No backfill: every existing account is a test account.
ALTER TABLE users ADD COLUMN onboarded_at TIMESTAMPTZ;
