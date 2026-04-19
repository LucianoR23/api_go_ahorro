-- Rollback: reconstruir users.name a partir de first_name + last_name.

ALTER TABLE users
    ADD COLUMN name TEXT;

-- trim() limpia el espacio sobrante si last_name es vacío (mononombres).
UPDATE users
SET name = trim(first_name || ' ' || last_name);

ALTER TABLE users
    ALTER COLUMN name SET NOT NULL;

ALTER TABLE users
    DROP COLUMN last_name,
    DROP COLUMN first_name;
