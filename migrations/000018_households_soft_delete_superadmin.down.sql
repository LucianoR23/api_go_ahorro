DROP INDEX IF EXISTS idx_households_deleted_at;
ALTER TABLE households DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE users DROP COLUMN IF EXISTS is_superadmin;
