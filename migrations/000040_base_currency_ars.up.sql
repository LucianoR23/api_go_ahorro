-- Plan 5.5: a space always sums in pesos. Movements keep their own currency;
-- only the space's base is locked. The column stays so another country can be
-- opened later by dropping this check (a space would still pick its currency
-- once and never change it).
-- NOT VALID: checks new and updated rows without failing on an old test space
-- in another currency.
ALTER TABLE households
    ADD CONSTRAINT households_base_currency_ars CHECK (base_currency = 'ARS') NOT VALID;
