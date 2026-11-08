-- Reverts 000034. Subcategories become plain categories; icon keys go back
-- to one emoji each (keys with no emoji get the old default).
ALTER TABLE categories DROP CONSTRAINT categories_icon_key_check;

UPDATE categories
SET icon = CASE icon
        WHEN 'wallet' THEN '💰'
        WHEN 'utensils' THEN '🍕'
        WHEN 'coffee' THEN '☕'
        WHEN 'shopping-cart' THEN '🛒'
        WHEN 'house' THEN '🏠'
        WHEN 'building' THEN '🏢'
        WHEN 'car' THEN '🚗'
        WHEN 'bus' THEN '🚌'
        WHEN 'fuel' THEN '⛽'
        WHEN 'clapperboard' THEN '🎬'
        WHEN 'lightbulb' THEN '💡'
        WHEN 'zap' THEN '⚡'
        WHEN 'wifi' THEN '📶'
        WHEN 'smartphone' THEN '📱'
        WHEN 'pill' THEN '💊'
        WHEN 'briefcase-medical' THEN '🏥'
        WHEN 'graduation-cap' THEN '🎓'
        WHEN 'tv' THEN '📺'
        WHEN 'dumbbell' THEN '🏋️'
        WHEN 'paw-print' THEN '🐶'
        WHEN 'shirt' THEN '👕'
        WHEN 'shopping-bag' THEN '🛍️'
        WHEN 'gift' THEN '🎁'
        WHEN 'plane' THEN '✈️'
        WHEN 'landmark' THEN '🏛️'
        WHEN 'wrench' THEN '🔧'
        WHEN 'package' THEN '📦'
        ELSE '💰'
    END;

ALTER TABLE categories ALTER COLUMN icon SET DEFAULT '💰';

DROP TRIGGER categories_one_level ON categories;
DROP FUNCTION categories_one_level();
DROP INDEX idx_categories_parent;
ALTER TABLE categories
    DROP CONSTRAINT categories_parent_not_self,
    DROP CONSTRAINT categories_parent_fk,
    DROP COLUMN parent_id;
ALTER TABLE categories DROP CONSTRAINT categories_household_id_id_key;
