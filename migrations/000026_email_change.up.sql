-- Plan 0.5: changing the email requires verifying the new address. The new
-- address waits in users.pending_email; the verification token carries it in
-- email_verifications.new_email (NULL = verify the current email, as before).
-- The account keeps its old email until the link sent to the new one is used.
ALTER TABLE users ADD COLUMN pending_email TEXT;
ALTER TABLE email_verifications ADD COLUMN new_email TEXT;
