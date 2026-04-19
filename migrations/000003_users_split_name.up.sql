-- Divide users.name en first_name + last_name.
-- Motivo: permitir mostrar/filtrar por apellido, y preparar para futuros
-- features donde importe distinguirlos (reportes, AI export).

-- Paso 1: agregar columnas como NULLables para poder hacer backfill.
ALTER TABLE users
    ADD COLUMN first_name TEXT,
    ADD COLUMN last_name  TEXT;

-- Paso 2: backfill desde la columna vieja.
-- split_part con delimitador ' ' (espacio) y posición 1/2.
-- Si no hay espacio en name: first_name = name completo, last_name = '' (vacío).
-- trim() limpia espacios sobrantes (ej: doble espacio, bordes).
UPDATE users
SET
    first_name = trim(split_part(name, ' ', 1)),
    last_name  = CASE
        WHEN position(' ' IN name) > 0
            -- Todo lo que viene después del primer espacio queda como apellido,
            -- incluyendo apellidos compuestos ("De la Cruz", "López García").
            THEN trim(substring(name FROM position(' ' IN name) + 1))
        ELSE ''
    END;

-- Paso 3: garantizar NOT NULL. Si un row se cuela con first_name='' se
-- acepta; el validador del service corta antes de llegar acá en inserts
-- nuevos. last_name='' es explícitamente válido (mononombres, apodos).
ALTER TABLE users
    ALTER COLUMN first_name SET NOT NULL,
    ALTER COLUMN last_name  SET NOT NULL;

-- Paso 4: borrar la columna vieja. Si algún día queremos rollback, el
-- .down.sql la reconstruye a partir de first_name + last_name.
ALTER TABLE users
    DROP COLUMN name;
