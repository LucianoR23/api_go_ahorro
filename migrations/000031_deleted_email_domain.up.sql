-- Plan 1.1: deleted accounts get a brand-free placeholder e-mail on the
-- reserved .invalid TLD (RFC 2606). Rows deleted before keep the same shape
-- under the new domain.
UPDATE users
SET email = replace(email, '@ahorra.deleted', '@deleted.invalid')
WHERE email LIKE 'deleted+%@ahorra.deleted';
