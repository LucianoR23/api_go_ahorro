DROP INDEX IF EXISTS idx_user_identities_user;
DROP TABLE IF EXISTS user_identities;

-- Solo se puede volver a SET NOT NULL si no quedan users con password_hash NULL.
-- En la práctica esto significa: borrar manualmente los users registrados
-- únicamente con Google antes de rollback. Si la migración falla acá, esa
-- es la razón.
ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL;
