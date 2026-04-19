-- Categorías por hogar. Cada hogar tiene su set independiente; al crear
-- un household sembramos 7 default (Comida, Hogar, Transporte, Entretenimiento,
-- Servicios, Salud, Otros) desde el service dentro de la misma transacción.
--
-- UNIQUE(household_id, name) para que no se dupliquen. Si el user quiere
-- renombrar "Otros" a algo más útil, es libre.
--
-- Sin soft-delete: el dominio tolera perder la categoría porque
-- expenses.category_id usa ON DELETE SET NULL (un gasto sin categoría
-- sigue siendo un gasto). Eso se materializa en la migración de expenses.
CREATE TABLE categories (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id  UUID NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    icon          TEXT NOT NULL DEFAULT '💰',
    color         TEXT NOT NULL DEFAULT '#2E75B6',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (household_id, name)
);

CREATE INDEX idx_categories_household ON categories(household_id);
