-- Queries sobre la tabla user_identities (OAuth: hoy Google, mañana otros).
-- (provider, subject) es PK; el matching de login pasa por acá.


-- name: GetUserIdentity :one
-- Lookup por (provider, subject). pgx.ErrNoRows si no existe → el service
-- lo mapea a domain.ErrNotFound y arranca el flujo de auto-vinculación.
SELECT provider, subject, user_id, email, linked_at
FROM user_identities
WHERE provider = $1 AND subject = $2;


-- name: CreateUserIdentity :exec
-- Inserta la vinculación. Conflicto en PK (provider, subject) → ErrConflict
-- (no debería darse en condiciones normales: si Get devolvió NotFound y
-- alguien crea la misma identidad en paralelo, la segunda inserción falla).
INSERT INTO user_identities (provider, subject, user_id, email)
VALUES ($1, $2, $3, $4);


-- name: ListUserIdentitiesByUser :many
-- Para mostrar al user qué providers tiene vinculados en su perfil.
SELECT provider, subject, email, linked_at
FROM user_identities
WHERE user_id = $1
ORDER BY linked_at ASC;
