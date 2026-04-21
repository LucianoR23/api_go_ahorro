DROP INDEX IF EXISTS idx_email_verifications_user_active;
DROP TABLE IF EXISTS email_verifications;
ALTER TABLE users DROP COLUMN IF EXISTS email_verified_at;
