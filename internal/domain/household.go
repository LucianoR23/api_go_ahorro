package domain

import (
	"time"

	"github.com/google/uuid"
)

// Role define el rol de un user dentro de un household.
// Usamos string alias (no iota) para que matchee directo con el CHECK
// de la tabla household_members y con el JSON de respuesta.
type Role string

const (
	// RoleOwner puede invitar, editar el hogar, cambiar split rules,
	// borrar el hogar. Al crearlo, el creador queda como owner.
	RoleOwner Role = "owner"

	// RoleMember puede crear/editar sus propios gastos, ver compartidos
	// y pagar deudas. No puede tocar configuración del hogar.
	RoleMember Role = "member"
)

// IsValid permite validar el rol antes de persistirlo. El CHECK de la
// DB lo rechazaría de todas formas, pero fallar temprano en el service
// da mejor mensaje de error.
func (r Role) IsValid() bool {
	return r == RoleOwner || r == RoleMember
}

// Household es la unidad multi-tenant. Un user puede pertenecer a varios.
// BaseCurrency es la moneda en la que se consolidan montos (ARS/USD/EUR)
// cuando el gasto se registra en otra moneda.
type Household struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	BaseCurrency string     `json:"baseCurrency"` // ARS, USD, EUR
	CreatedBy    uuid.UUID  `json:"createdBy"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	// DeletedAt != nil indica soft-delete. Los endpoints públicos nunca
	// devuelven hogares con este campo seteado (los filtra la query). Solo
	// aparece poblado en los endpoints /admin/*.
	DeletedAt *time.Time `json:"deletedAt,omitempty"`
}

// HouseholdWithOwner es la proyección que devuelven los endpoints /admin/*:
// el hogar + su owner actual. El superadmin necesita ver el owner antes de
// restaurar o purgar para evitar operar sobre el hogar equivocado.
type HouseholdWithOwner struct {
	Household Household `json:"household"`
	Owner     User      `json:"owner"`
}

// HouseholdMember representa la membresía de un user en un household,
// con su rol y la fecha en que se unió.
type HouseholdMember struct {
	HouseholdID uuid.UUID `json:"householdId"`
	UserID      uuid.UUID `json:"userId"`
	Role        Role      `json:"role"`
	JoinedAt    time.Time `json:"joinedAt"`
}

// HouseholdMemberDetail agrega info del user al listar miembros, así el
// frontend muestra nombre/email sin hacer queries extra por cada miembro.
type HouseholdMemberDetail struct {
	User     User      `json:"user"`
	Role     Role      `json:"role"`
	JoinedAt time.Time `json:"joinedAt"`
}

// HouseholdInvite representa una invitación pendiente/aceptada/revocada
// a un hogar. El token plano nunca se persiste: solo su hash (SHA-256).
// El token plano se devuelve UNA sola vez, al crear la invitación, para
// armar el link del email.
type HouseholdInvite struct {
	ID          uuid.UUID  `json:"id"`
	HouseholdID uuid.UUID  `json:"householdId"`
	Email       string     `json:"email"`
	InvitedBy   uuid.UUID  `json:"invitedBy"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	AcceptedAt  *time.Time `json:"acceptedAt,omitempty"`
	AcceptedBy  *uuid.UUID `json:"acceptedBy,omitempty"`
	RevokedAt   *time.Time `json:"revokedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}
