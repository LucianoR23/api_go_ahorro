-- name: CreateCategory :one
INSERT INTO categories (household_id, name, icon, color, parent_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetCategoryByID :one
SELECT * FROM categories WHERE id = $1;

-- name: ListCategoriesByHousehold :many
SELECT * FROM categories
WHERE household_id = $1
ORDER BY name ASC;

-- name: UpdateCategory :one
UPDATE categories
SET name = $2,
    icon = $3,
    color = $4,
    parent_id = $5,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteCategory :exec
DELETE FROM categories WHERE id = $1;

-- name: CategoryInHousehold :one
-- A category id sent by a client belongs to that household (plan 0.6).
SELECT EXISTS (SELECT 1 FROM categories WHERE id = $1 AND household_id = $2)::bool;

-- name: CategoryHasChildren :one
SELECT EXISTS (SELECT 1 FROM categories WHERE parent_id = $1)::bool;
