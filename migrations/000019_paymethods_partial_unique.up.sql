-- Cambia el UNIQUE (owner_user_id, name) de banks y payment_methods a un
-- índice parcial WHERE is_active = true. Motivo: antes, si el user
-- desactivaba (soft-delete) un medio de pago y quería crearlo de nuevo
-- con el mismo nombre, el UNIQUE global lo rechazaba con 409, porque la
-- fila vieja seguía ahí aunque estuviera inactiva.
--
-- Con el índice parcial pueden coexistir N inactivos con el mismo nombre
-- y solo uno activo por (owner, name). El service además implementa
-- "revive": si al crear ya existe una fila inactiva con ese nombre, la
-- reactiva en vez de insertar una nueva (preserva el id y por lo tanto
-- el historial de expenses que lo referencian).

ALTER TABLE payment_methods DROP CONSTRAINT IF EXISTS payment_methods_owner_user_id_name_key;
CREATE UNIQUE INDEX IF NOT EXISTS payment_methods_owner_name_active_uniq
    ON payment_methods(owner_user_id, name) WHERE is_active = true;

ALTER TABLE banks DROP CONSTRAINT IF EXISTS banks_owner_user_id_name_key;
CREATE UNIQUE INDEX IF NOT EXISTS banks_owner_name_active_uniq
    ON banks(owner_user_id, name) WHERE is_active = true;
