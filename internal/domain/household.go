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
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	BaseCurrency  string    `json:"baseCurrency"` // ARS, USD, EUR
	CreatedBy     uuid.UUID `json:"createdBy"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
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
