-- Plan 1.2: "Mi plata" + one Hogar.
--
-- Every user has exactly one personal space ("Mi plata", kind = 'personal')
-- that only they belong to, and at most one active shared space ("Hogar",
-- kind = 'shared'). The API keeps working with N households; these rules
-- are the product limit, enforced here so no code path or race can break
-- them. The service checks them first to give a friendly message.

ALTER TABLE households
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'shared'
        CONSTRAINT households_kind_check CHECK (kind IN ('personal', 'shared'));

-- One personal space per user, deleted or not (it is never re-created).
CREATE UNIQUE INDEX households_one_personal_per_user
    ON households (created_by) WHERE kind = 'personal';


-- household_members rules:
--   * a personal space only admits its creator;
--   * a user belongs to at most one active (not soft-deleted) shared space.
-- The user row is locked first so two concurrent joins for the same user
-- run one after the other and the second sees the first.
CREATE FUNCTION household_members_enforce_spaces() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    hh households%ROWTYPE;
BEGIN
    SELECT * INTO hh FROM households WHERE id = NEW.household_id;

    IF hh.kind = 'personal' THEN
        IF NEW.user_id <> hh.created_by THEN
            RAISE EXCEPTION 'a personal space only admits its owner'
                USING ERRCODE = '23514', CONSTRAINT = 'household_members_personal_single_member';
        END IF;
        RETURN NEW;
    END IF;

    IF hh.deleted_at IS NOT NULL THEN
        RETURN NEW;
    END IF;

    PERFORM 1 FROM users WHERE id = NEW.user_id FOR NO KEY UPDATE;
    IF EXISTS (
        SELECT 1
        FROM household_members hm
        JOIN households h ON h.id = hm.household_id
        WHERE hm.user_id = NEW.user_id
          AND hm.household_id <> NEW.household_id
          AND h.kind = 'shared'
          AND h.deleted_at IS NULL
    ) THEN
        RAISE EXCEPTION 'user % already belongs to a shared household', NEW.user_id
            USING ERRCODE = '23505', CONSTRAINT = 'household_members_one_shared_per_user';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER household_members_enforce_spaces
    BEFORE INSERT OR UPDATE OF household_id, user_id ON household_members
    FOR EACH ROW EXECUTE FUNCTION household_members_enforce_spaces();


-- households rules:
--   * kind never changes;
--   * restoring a soft-deleted shared space fails if one of its members
--     joined another shared space meanwhile.
CREATE FUNCTION households_enforce_spaces() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.kind <> OLD.kind THEN
        RAISE EXCEPTION 'households.kind cannot change'
            USING ERRCODE = '23514', CONSTRAINT = 'households_kind_immutable';
    END IF;

    IF NEW.kind = 'shared' AND OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS NULL THEN
        PERFORM 1 FROM users u
        JOIN household_members hm ON hm.user_id = u.id
        WHERE hm.household_id = NEW.id
        ORDER BY u.id
        FOR NO KEY UPDATE OF u;

        IF EXISTS (
            SELECT 1
            FROM household_members mine
            JOIN household_members other ON other.user_id = mine.user_id
                                        AND other.household_id <> mine.household_id
            JOIN households h ON h.id = other.household_id
            WHERE mine.household_id = NEW.id
              AND h.kind = 'shared'
              AND h.deleted_at IS NULL
        ) THEN
            RAISE EXCEPTION 'a member of household % already belongs to another shared household', NEW.id
                USING ERRCODE = '23505', CONSTRAINT = 'household_members_one_shared_per_user';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER households_enforce_spaces
    BEFORE UPDATE OF kind, deleted_at ON households
    FOR EACH ROW EXECUTE FUNCTION households_enforce_spaces();
