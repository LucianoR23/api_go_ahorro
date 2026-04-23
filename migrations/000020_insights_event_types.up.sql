-- Permite que insights "por evento" (shared_expense, invite, settlement)
-- referencien la entidad origen vía ref_id, sin chocar con el UNIQUE viejo
-- pensado para insights diarios agregados (un row por día/tipo).
--
-- Estrategia:
--   * ref_id NULL  → insights agregados (daily_summary, weekly_review, alert).
--                    Mantenemos el viejo UNIQUE para idempotencia diaria.
--   * ref_id !NULL → insights por evento. Únicos por (hh, user, type, ref_id).
--                    Esto evita duplicar el mismo insight si el handler se
--                    ejecuta dos veces por el mismo expense/invite/settlement.

ALTER TABLE daily_insights ADD COLUMN ref_id UUID NULL;

-- Drop del UNIQUE original. Postgres autogenera nombres truncados a 63 chars
-- y la combinación exacta varía según versión, así que lo buscamos por shape.
DO $$
DECLARE
    cname TEXT;
BEGIN
    SELECT conname INTO cname
    FROM pg_constraint
    WHERE conrelid = 'daily_insights'::regclass
      AND contype = 'u'
      AND conkey = (
          SELECT array_agg(attnum ORDER BY ord)
          FROM (
              SELECT a.attnum, ord FROM unnest(ARRAY['household_id','user_id','insight_date','insight_type']) WITH ORDINALITY AS u(name, ord)
              JOIN pg_attribute a ON a.attrelid = 'daily_insights'::regclass AND a.attname = u.name
          ) s
      );
    IF cname IS NOT NULL THEN
        EXECUTE format('ALTER TABLE daily_insights DROP CONSTRAINT %I', cname);
    END IF;
END $$;

CREATE UNIQUE INDEX daily_insights_unique_no_ref
    ON daily_insights (household_id, user_id, insight_date, insight_type)
    WHERE ref_id IS NULL;

CREATE UNIQUE INDEX daily_insights_unique_with_ref
    ON daily_insights (household_id, user_id, insight_type, ref_id)
    WHERE ref_id IS NOT NULL;
