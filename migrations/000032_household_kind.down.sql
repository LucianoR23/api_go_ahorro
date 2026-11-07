DROP TRIGGER IF EXISTS households_enforce_spaces ON households;
DROP FUNCTION IF EXISTS households_enforce_spaces();
DROP TRIGGER IF EXISTS household_members_enforce_spaces ON household_members;
DROP FUNCTION IF EXISTS household_members_enforce_spaces();
DROP INDEX IF EXISTS households_one_personal_per_user;
ALTER TABLE households DROP COLUMN IF EXISTS kind;
