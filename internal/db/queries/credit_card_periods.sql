-- name: UpsertCreditCardPeriod :one
INSERT INTO credit_card_periods (credit_card_id, period_ym, closing_date, due_date)
VALUES ($1, $2, $3, $4)
ON CONFLICT (credit_card_id, period_ym) DO UPDATE
SET closing_date = EXCLUDED.closing_date,
    due_date = EXCLUDED.due_date,
    updated_at = NOW()
RETURNING *;

-- name: GetCreditCardPeriod :one
SELECT * FROM credit_card_periods
WHERE credit_card_id = $1 AND period_ym = $2;

-- name: ListCreditCardPeriods :many
SELECT * FROM credit_card_periods
WHERE credit_card_id = $1
ORDER BY period_ym DESC;

-- name: DeleteCreditCardPeriod :exec
DELETE FROM credit_card_periods
WHERE credit_card_id = $1 AND period_ym = $2;

-- name: GetLatestCreditCardPeriod :one
SELECT * FROM credit_card_periods
WHERE credit_card_id = $1
ORDER BY period_ym DESC
LIMIT 1;

-- name: ListActiveCardsWithLatestPeriodByHousehold :many
-- Active credit cards of every member of the household, each with its most
-- recent period, in one query (plan 0.14: this was one query per member plus
-- one per card). Cards with no period are left out.
SELECT
    pm.id            AS payment_method_id,
    pm.owner_user_id,
    cc.id            AS credit_card_id,
    cc.alias,
    p.period_ym,
    p.closing_date,
    p.due_date
FROM household_members hm
JOIN payment_methods pm ON pm.owner_user_id = hm.user_id AND pm.kind = 'credit' AND pm.is_active
JOIN credit_cards cc ON cc.payment_method_id = pm.id
JOIN LATERAL (
    SELECT period_ym, closing_date, due_date
    FROM credit_card_periods
    WHERE credit_card_id = cc.id
    ORDER BY period_ym DESC
    LIMIT 1
) p ON true
WHERE hm.household_id = $1
ORDER BY pm.owner_user_id, cc.alias;
