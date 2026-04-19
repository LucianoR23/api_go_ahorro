-- name: CreateCategory :one
INSERT INTO categories (household_id, name, icon, color)
VALUES ($1, $2, $3, $4)
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
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteCategory :exec
DELETE FROM categories WHERE id = $1;
