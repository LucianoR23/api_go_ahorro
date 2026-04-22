-- Revertir al UNIQUE total. Asume que no hay duplicados (owner, name)
-- en filas inactivas. Si los hay, la restauración fallará: ejecutar
-- antes una limpieza manual de duplicados antes de correr el down.

DROP INDEX IF EXISTS banks_owner_name_active_uniq;
ALTER TABLE banks ADD CONSTRAINT banks_owner_user_id_name_key UNIQUE (owner_user_id, name);

DROP INDEX IF EXISTS payment_methods_owner_name_active_uniq;
ALTER TABLE payment_methods ADD CONSTRAINT payment_methods_owner_user_id_name_key UNIQUE (owner_user_id, name);
