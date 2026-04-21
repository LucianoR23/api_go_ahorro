// Package users expone el repository de la entidad User:
// traduce entre el struct generado por sqlc y el tipo de dominio,
// y convierte errores de pgx al vocabulario de domain.
package users

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// Repository ofrece operaciones de persistencia sobre User.
// Los handlers y services NUNCA tocan sqlcgen directo: pasan por acá.
type Repository struct {
	q *sqlcgen.Queries
}

// NewRepository envuelve el pool en el Queries generado por sqlc.
// Alternativamente podríamos aceptar una tx para operaciones transaccionales;
// lo agregamos cuando haga falta (ej: bootstrap del register que crea
// user + household + efectivo en una sola tx).
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{q: sqlcgen.New(pool)}
}

// Credentials es lo único que sale del repo con el password hash adentro.
// Se usa solo en el service de auth para validar login; cualquier otra
// capa trabaja con domain.User (sin hash).
type Credentials struct {
	User         domain.User
	PasswordHash string
}

// Create inserta un usuario y devuelve la fila mapeada a dominio.
// El caller ya debe haber hasheado el password — el repo no conoce bcrypt.
func (r *Repository) Create(ctx context.Context, email, passwordHash, firstName, lastName string) (domain.User, error) {
	row, err := r.q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Email:        email,
		PasswordHash: passwordHash,
		FirstName:    firstName,
		LastName:     lastName,
	})
	if err != nil {
		// Unique violation del email → ErrConflict.
		// Usamos el código SQLSTATE de pgx en vez del texto del error
		// (más estable entre versiones de Postgres / idiomas de locale).
		if isUniqueViolation(err) {
			return domain.User{}, fmt.Errorf("email ya registrado: %w", domain.ErrConflict)
		}
		return domain.User{}, fmt.Errorf("users.Create: %w", err)
	}
	return toDomain(row), nil
}

// GetByEmail devuelve el usuario (sin hash) o ErrNotFound. Usado por
// flujos que buscan un user por email sin necesitar sus credenciales
// (ej: invitar a un household).
func (r *Repository) GetByEmail(ctx context.Context, email string) (domain.User, error) {
	row, err := r.q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("users.GetByEmail: %w", err)
	}
	return toDomain(row), nil
}

// GetByID devuelve el usuario o ErrNotFound si no existe.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	row, err := r.q.GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("users.GetByID: %w", err)
	}
	return toDomain(row), nil
}

// UpdateProfile actualiza first_name, last_name y email. El caller valida
// formato; acá solo mapeamos errores de DB. Colisión de email → ErrConflict.
func (r *Repository) UpdateProfile(ctx context.Context, id uuid.UUID, firstName, lastName, email string) (domain.User, error) {
	row, err := r.q.UpdateUserProfile(ctx, sqlcgen.UpdateUserProfileParams{
		ID:        id,
		FirstName: firstName,
		LastName:  lastName,
		Email:     email,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		if isUniqueViolation(err) {
			return domain.User{}, fmt.Errorf("email ya registrado: %w", domain.ErrConflict)
		}
		return domain.User{}, fmt.Errorf("users.UpdateProfile: %w", err)
	}
	return toDomain(row), nil
}

// UpdatePasswordHash pisa el hash del user. Usado por change-password y
// reset-password. El caller debe haber hasheado con bcrypt antes.
func (r *Repository) UpdatePasswordHash(ctx context.Context, id uuid.UUID, newHash string) error {
	if err := r.q.UpdateUserPassword(ctx, sqlcgen.UpdateUserPasswordParams{
		ID:           id,
		PasswordHash: newHash,
	}); err != nil {
		return fmt.Errorf("users.UpdatePasswordHash: %w", err)
	}
	return nil
}

// CountHouseholdsOwned devuelve cuántos hogares tiene el user como owner.
// Usado por la baja de cuenta para bloquear si aún es dueño de alguno.
func (r *Repository) CountHouseholdsOwned(ctx context.Context, userID uuid.UUID) (int64, error) {
	n, err := r.q.CountHouseholdsOwnedByUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("users.CountHouseholdsOwned: %w", err)
	}
	return n, nil
}

// MarkEmailVerified setea users.email_verified_at = now() si aún no lo estaba.
// Idempotente (la query tiene `AND email_verified_at IS NULL`).
func (r *Repository) MarkEmailVerified(ctx context.Context, id uuid.UUID) error {
	if err := r.q.MarkUserEmailVerified(ctx, id); err != nil {
		return fmt.Errorf("users.MarkEmailVerified: %w", err)
	}
	return nil
}

// SoftDelete marca al user como borrado y anonimiza su email. No remueve
// filas referenciadas (expenses, settlements, etc.) — esas quedan con el
// user_id original para preservar el histórico.
func (r *Repository) SoftDelete(ctx context.Context, id uuid.UUID) error {
	if err := r.q.SoftDeleteUser(ctx, id); err != nil {
		return fmt.Errorf("users.SoftDelete: %w", err)
	}
	return nil
}

// GetCredentialsByEmail se usa solo en el login. Devuelve User + hash
// para que el service de auth pueda comparar el password sin exponer
// el hash al resto del sistema.
//
// Si el email no existe devuelve ErrNotFound — el service lo convierte
// en ErrUnauthorized en la respuesta para no filtrar existencia de emails
// (enumeration attack).
func (r *Repository) GetCredentialsByEmail(ctx context.Context, email string) (Credentials, error) {
	row, err := r.q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return Credentials{}, domain.ErrNotFound
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("users.GetCredentialsByEmail: %w", err)
	}
	return Credentials{
		User:         toDomain(row),
		PasswordHash: row.PasswordHash,
	}, nil
}

// GetCredentialsByID: igual que GetCredentialsByEmail pero por ID. Usado
// en change-password para validar la contraseña actual del user logueado.
func (r *Repository) GetCredentialsByID(ctx context.Context, id uuid.UUID) (Credentials, error) {
	row, err := r.q.GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Credentials{}, domain.ErrNotFound
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("users.GetCredentialsByID: %w", err)
	}
	return Credentials{
		User:         toDomain(row),
		PasswordHash: row.PasswordHash,
	}, nil
}

// toDomain mapea el struct de sqlc al de domain. Extrae .Time de los
// pgtype.Timestamptz (sqlc los genera así porque son NULLables en general,
// aunque acá son NOT NULL — el .Time siempre es válido).
func toDomain(u sqlcgen.User) domain.User {
	d := domain.User{
		ID:        u.ID,
		Email:     string(u.Email),
		FirstName: u.FirstName,
		LastName:  u.LastName,
		CreatedAt: u.CreatedAt.Time,
		UpdatedAt: u.UpdatedAt.Time,
	}
	if u.EmailVerifiedAt.Valid {
		t := u.EmailVerifiedAt.Time
		d.EmailVerifiedAt = &t
	}
	return d
}
