-- ============================================================
-- users.deleted_at + anonymization support
-- ============================================================
-- Soft delete: no borramos la fila para no romper FKs ON DELETE RESTRICT
-- (expenses.created_by, settlements.from_user/to_user, incomes.received_by).
-- En su lugar, marcamos deleted_at y anonimizamos el email a un valor único
-- no-login ("deleted+<id>@ahorra.deleted") — así el user puede re-registrarse
-- con el mismo email sin colisionar con la fila vieja.
ALTER TABLE users
    ADD COLUMN deleted_at TIMESTAMPTZ;

-- Permite listar "cuentas activas" eficientemente.
CREATE INDEX idx_users_deleted_at ON users(deleted_at);
