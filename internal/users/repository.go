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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/valuo_api/internal/db/sqlc"
	"github.com/LucianoR23/valuo_api/internal/domain"
)

// Repository ofrece operaciones de persistencia sobre User.
// Los handlers y services NUNCA tocan sqlcgen directo: pasan por acá.
type Repository struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

// NewRepository envuelve el pool en el Queries generado por sqlc.
// Alternativamente podríamos aceptar una tx para operaciones transaccionales;
// lo agregamos cuando haga falta (ej: bootstrap del register que crea
// user + household + efectivo en una sola tx).
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: sqlcgen.New(pool)}
}

// Credentials es lo único que sale del repo con el password hash adentro.
// Se usa solo en el service de auth para validar login; cualquier otra
// capa trabaja con domain.User (sin hash).
//
// HasPassword distingue users con login local de los OAuth-only (registrados
// vía Google sin password_hash). El service de Login rechaza HasPassword=false
// con el mismo error genérico que email inexistente — no filtra qué método
// usa la cuenta (anti-enumeration).
type Credentials struct {
	User         domain.User
	PasswordHash string
	HasPassword  bool
}

// Create inserta un usuario y devuelve la fila mapeada a dominio.
// El caller ya debe haber hasheado el password — el repo no conoce bcrypt.
func (r *Repository) Create(ctx context.Context, email, passwordHash, firstName, lastName string) (domain.User, error) {
	return create(ctx, r.q, email, passwordHash, firstName, lastName)
}

// CreateTx is Create inside the caller's transaction (registration, plan 0.8).
func (r *Repository) CreateTx(ctx context.Context, tx pgx.Tx, email, passwordHash, firstName, lastName string) (domain.User, error) {
	return create(ctx, r.q.WithTx(tx), email, passwordHash, firstName, lastName)
}

func create(ctx context.Context, q *sqlcgen.Queries, email, passwordHash, firstName, lastName string) (domain.User, error) {
	row, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Email:        email,
		PasswordHash: pgtype.Text{String: passwordHash, Valid: true},
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

// CreateWithoutPassword inserta un user para registro OAuth (Google).
// password_hash queda NULL y email_verified_at = now() (el provider externo
// nos garantiza la propiedad del email). Misma semántica de conflicto que
// Create: email duplicado → ErrConflict.
func (r *Repository) CreateWithoutPassword(ctx context.Context, email, firstName, lastName string) (domain.User, error) {
	row, err := r.q.CreateUserWithoutPassword(ctx, sqlcgen.CreateUserWithoutPasswordParams{
		Email:     email,
		FirstName: firstName,
		LastName:  lastName,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return domain.User{}, fmt.Errorf("email ya registrado: %w", domain.ErrConflict)
		}
		return domain.User{}, fmt.Errorf("users.CreateWithoutPassword: %w", err)
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
		PasswordHash: pgtype.Text{String: newHash, Valid: true},
	}); err != nil {
		return fmt.Errorf("users.UpdatePasswordHash: %w", err)
	}
	return nil
}

// IsSuperadmin devuelve true si el user tiene el flag global is_superadmin.
// Se setea manualmente por DB; no hay endpoint para modificarlo. Usado por
// auth.RequireSuperadmin para gatear endpoints /admin/*.
func (r *Repository) IsSuperadmin(ctx context.Context, userID uuid.UUID) (bool, error) {
	ok, err := r.q.IsUserSuperadmin(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		// User no existe o está soft-deleted → trato como "no admin".
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("users.IsSuperadmin: %w", err)
	}
	return ok, nil
}

// SetPendingEmail stores (or clears, with nil) an email change waiting for
// verification.
func (r *Repository) SetPendingEmail(ctx context.Context, id uuid.UUID, email *string) error {
	var pe pgtype.Text
	if email != nil {
		pe = pgtype.Text{String: *email, Valid: true}
	}
	if err := r.q.SetUserPendingEmail(ctx, sqlcgen.SetUserPendingEmailParams{ID: id, PendingEmail: pe}); err != nil {
		return fmt.Errorf("users.SetPendingEmail: %w", err)
	}
	return nil
}

// ApplyEmailChange makes the verified new address the user's email.
// Another account took it meanwhile → ErrConflict.
func (r *Repository) ApplyEmailChange(ctx context.Context, id uuid.UUID, newEmail string) (domain.User, error) {
	row, err := r.q.ApplyUserEmailChange(ctx, sqlcgen.ApplyUserEmailChangeParams{ID: id, NewEmail: newEmail})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		if isUniqueViolation(err) {
			return domain.User{}, domain.NewConflictError("ese email ya está registrado en otra cuenta")
		}
		return domain.User{}, fmt.Errorf("users.ApplyEmailChange: %w", err)
	}
	return toDomain(row), nil
}

// MarkEmailVerified setea users.email_verified_at = now() si aún no lo estaba.
// Idempotente (la query tiene `AND email_verified_at IS NULL`).
func (r *Repository) MarkEmailVerified(ctx context.Context, id uuid.UUID) error {
	if err := r.q.MarkUserEmailVerified(ctx, id); err != nil {
		return fmt.Errorf("users.MarkEmailVerified: %w", err)
	}
	return nil
}

// MarkEmailVerifiedTx is MarkEmailVerified inside the caller's transaction.
func (r *Repository) MarkEmailVerifiedTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := r.q.WithTx(tx).MarkUserEmailVerified(ctx, id); err != nil {
		return fmt.Errorf("users.MarkEmailVerified: %w", err)
	}
	return nil
}

// DeleteAccount deletes the account in one transaction (plan 0.8): unless
// the user still owns an active household, it removes their memberships and
// push subscriptions, revokes their sessions, marks the user deleted and
// anonymizes the email (which frees it to register again). Rows that
// reference the user (expenses, settlements, ...) keep the id to preserve
// the history. It returns the active households still owned; when that is
// above zero nothing changed. An unknown or already deleted user is a no-op.
func (r *Repository) DeleteAccount(ctx context.Context, id uuid.UUID) (owned int64, err error) {
	err = pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		if _, err := q.LockUserForDeletion(ctx, id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if err := q.LockUserMemberships(ctx, id); err != nil {
			return err
		}
		n, err := q.CountHouseholdsOwnedByUser(ctx, id)
		if err != nil {
			return err
		}
		if n > 0 {
			owned = n
			return nil
		}
		if err := q.SoftDeletePersonalHouseholdOfUser(ctx, id); err != nil {
			return err
		}
		if err := q.RemoveAllMembershipsForUser(ctx, id); err != nil {
			return err
		}
		if err := q.DeleteUserPushSubscriptions(ctx, id); err != nil {
			return err
		}
		if err := q.RevokeAllUserSessions(ctx, id); err != nil {
			return err
		}
		return q.SoftDeleteUser(ctx, id)
	})
	if err != nil {
		return 0, fmt.Errorf("users.DeleteAccount: %w", err)
	}
	return owned, nil
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
		PasswordHash: row.PasswordHash.String,
		HasPassword:  row.PasswordHash.Valid,
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
		PasswordHash: row.PasswordHash.String,
		HasPassword:  row.PasswordHash.Valid,
	}, nil
}

// toDomain mapea el struct de sqlc al de domain. Extrae .Time de los
// pgtype.Timestamptz (sqlc los genera así porque son NULLables en general,
// aunque acá son NOT NULL — el .Time siempre es válido).
func toDomain(u sqlcgen.User) domain.User {
	d := domain.User{
		ID:           u.ID,
		Email:        string(u.Email),
		FirstName:    u.FirstName,
		LastName:     u.LastName,
		IsSuperadmin: u.IsSuperadmin,
		CreatedAt:    u.CreatedAt.Time,
		UpdatedAt:    u.UpdatedAt.Time,
	}
	if u.EmailVerifiedAt.Valid {
		t := u.EmailVerifiedAt.Time
		d.EmailVerifiedAt = &t
	}
	if u.PendingEmail.Valid {
		e := u.PendingEmail.String
		d.PendingEmail = &e
	}
	return d
}
