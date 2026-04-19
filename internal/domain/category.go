package domain

import (
	"time"

	"github.com/google/uuid"
)

// Category es una categoría de gasto bajo un household.
// No tiene soft-delete: se elimina físicamente y expenses.category_id
// queda en NULL (ON DELETE SET NULL) para no perder el gasto histórico.
type Category struct {
	ID          uuid.UUID
	HouseholdID uuid.UUID
	Name        string
	Icon        string
	Color       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// DefaultCategorySeed es la plantilla que se siembra al crear un household.
// Nombre + ícono + color sugerido. El user puede editar libremente.
type DefaultCategorySeed struct {
	Name  string
	Icon  string
	Color string
}

// DefaultCategories: set que se crea en cada household nuevo.
// Orden = orden visual en el onboarding. No cambiar sin migración explícita.
var DefaultCategories = []DefaultCategorySeed{
	{Name: "Comida", Icon: "🍕", Color: "#E76F51"},
	{Name: "Hogar", Icon: "🏠", Color: "#2A9D8F"},
	{Name: "Transporte", Icon: "🚗", Color: "#264653"},
	{Name: "Entretenimiento", Icon: "🎬", Color: "#E9C46A"},
	{Name: "Servicios", Icon: "💡", Color: "#F4A261"},
	{Name: "Salud", Icon: "💊", Color: "#6A994E"},
	{Name: "Otros", Icon: "📦", Color: "#6C757D"},
}
