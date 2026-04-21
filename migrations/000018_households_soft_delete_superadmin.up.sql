-- ============================================================
-- households.deleted_at — soft delete
-- ============================================================
-- Soft delete de hogares: el owner "borra" con DELETE /households/{id},
-- pero la fila queda con deleted_at != NULL. Los reads del API filtran
-- por deleted_at IS NULL → los miembros pierden acceso inmediato.
--
-- La recuperación (restore) y el borrado físico (purge con CASCADE) quedan
-- gateados por un flag global is_superadmin en users (ver abajo).
ALTER TABLE households
    ADD COLUMN deleted_at TIMESTAMPTZ;

-- Permite listar eficientemente soft-deleted vs activos.
CREATE INDEX idx_households_deleted_at ON households(deleted_at);


-- ============================================================
-- users.is_superadmin — flag global de administración
-- ============================================================
-- Dimensión independiente del rol por-hogar. Un superadmin NO tiene acceso
-- implícito a hogares de los que no es miembro — el flag solo habilita los
-- endpoints /admin/* (purge, restore, listar soft-deleted).
--
-- Se setea manualmente por DB:
--   UPDATE users SET is_superadmin = true WHERE email = 'yo@dominio.com';
ALTER TABLE users
    ADD COLUMN is_superadmin BOOLEAN NOT NULL DEFAULT false;
