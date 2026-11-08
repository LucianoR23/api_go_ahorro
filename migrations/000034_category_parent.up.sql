-- Plan 1.4: Argentine categories.
--
-- 1. Subcategories, one level deep: Servicios > Luz. parent_id points to a
--    category of the same household (composite FK). Deleting a parent keeps
--    its children as top-level categories, so no expense loses its category.
-- 2. Icons become Lucide keys ("shopping-cart") instead of emojis. Stored
--    emojis are translated with the map the app used until now; unknown
--    values fall back to "tag".

ALTER TABLE categories
    ADD CONSTRAINT categories_household_id_id_key UNIQUE (household_id, id);

ALTER TABLE categories
    ADD COLUMN parent_id UUID,
    ADD CONSTRAINT categories_parent_fk
        FOREIGN KEY (household_id, parent_id) REFERENCES categories (household_id, id)
        ON DELETE SET NULL (parent_id),
    ADD CONSTRAINT categories_parent_not_self CHECK (parent_id <> id);

-- (parent_id, household_id): serves the FK and lookups by parent alone.
CREATE INDEX idx_categories_parent ON categories (parent_id, household_id);

-- One level only: a parent is top-level, and a category with children cannot
-- become a child. The service checks this first to answer with a field
-- error; the trigger is the backstop.
CREATE FUNCTION categories_one_level() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.parent_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF EXISTS (SELECT 1 FROM categories WHERE id = NEW.parent_id AND parent_id IS NOT NULL) THEN
        RAISE EXCEPTION 'a subcategory cannot have subcategories'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'categories_one_level';
    END IF;
    IF EXISTS (SELECT 1 FROM categories WHERE parent_id = NEW.id) THEN
        RAISE EXCEPTION 'a category with subcategories cannot be a subcategory'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'categories_one_level';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER categories_one_level
    BEFORE INSERT OR UPDATE OF parent_id ON categories
    FOR EACH ROW EXECUTE FUNCTION categories_one_level();

-- Emoji to icon key. Variation selectors (U+FE0F) are stripped first.
UPDATE categories
SET icon = CASE replace(icon, U&'\FE0F', '')
        WHEN '💰' THEN 'wallet'
        WHEN '💵' THEN 'wallet'
        WHEN '🍕' THEN 'utensils'
        WHEN '🍔' THEN 'utensils'
        WHEN '🍜' THEN 'utensils'
        WHEN '🍽' THEN 'utensils'
        WHEN '☕' THEN 'coffee'
        WHEN '🛒' THEN 'shopping-cart'
        WHEN '🏠' THEN 'house'
        WHEN '🏢' THEN 'building'
        WHEN '🚗' THEN 'car'
        WHEN '🚖' THEN 'car'
        WHEN '🚕' THEN 'car'
        WHEN '🚌' THEN 'bus'
        WHEN '⛽' THEN 'fuel'
        WHEN '🎬' THEN 'clapperboard'
        WHEN '🎮' THEN 'clapperboard'
        WHEN '💡' THEN 'lightbulb'
        WHEN '⚡' THEN 'zap'
        WHEN '📶' THEN 'wifi'
        WHEN '📱' THEN 'smartphone'
        WHEN '💊' THEN 'pill'
        WHEN '🏥' THEN 'briefcase-medical'
        WHEN '📚' THEN 'graduation-cap'
        WHEN '🎓' THEN 'graduation-cap'
        WHEN '📺' THEN 'tv'
        WHEN '🏋' THEN 'dumbbell'
        WHEN '🐶' THEN 'paw-print'
        WHEN '🐱' THEN 'paw-print'
        WHEN '👕' THEN 'shirt'
        WHEN '🛍' THEN 'shopping-bag'
        WHEN '🎁' THEN 'gift'
        WHEN '✈' THEN 'plane'
        WHEN '🏛' THEN 'landmark'
        WHEN '🔧' THEN 'wrench'
        WHEN '📦' THEN 'package'
        ELSE CASE WHEN icon ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(icon) <= 32 THEN icon ELSE 'tag' END
    END;

ALTER TABLE categories
    ALTER COLUMN icon SET DEFAULT 'tag',
    ADD CONSTRAINT categories_icon_key_check
        CHECK (icon ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(icon) <= 32);
