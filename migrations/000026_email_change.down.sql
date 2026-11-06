ALTER TABLE email_verifications DROP COLUMN IF EXISTS new_email;
ALTER TABLE users DROP COLUMN IF EXISTS pending_email;
