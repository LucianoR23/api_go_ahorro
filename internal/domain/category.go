package domain

import (
	"time"

	"github.com/google/uuid"
)

// Category es una categoría de gasto bajo un household.
// No tiene soft-delete: se elimina físicamente y expenses.category_id
// queda en NULL (ON DELETE SET NULL) para no perder el gasto histórico.
//
// ParentID makes it a subcategory (plan 1.4), one level deep. Deleting the
// parent turns its children into top-level categories. Icon is a Lucide key
// such as "shopping-cart"; the app maps it to the icon.
type Category struct {
	ID          uuid.UUID
	HouseholdID uuid.UUID
	ParentID    *uuid.UUID
	Name        string
	Icon        string
	Color       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// DefaultCategorySeed es la plantilla que se siembra al crear un household.
// Nombre + ícono + color sugerido, and its subcategories. El user puede
// editar libremente.
type DefaultCategorySeed struct {
	Name     string
	Icon     string
	Color    string
	Children []DefaultCategorySeed
}

// DefaultCategories: the Argentine set every new household and personal
// space starts with (plan 1.4). Subcategories take their parent's color.
var DefaultCategories = []DefaultCategorySeed{
	{Name: "Súper", Icon: "shopping-cart", Color: "#2A9D8F"},
	{Name: "Almacén", Icon: "store", Color: "#2A9D8F"},
	{Name: "Delivery", Icon: "bike", Color: "#E76F51"},
	{Name: "Restaurantes", Icon: "utensils", Color: "#E76F51"},
	{Name: "Transporte", Icon: "bus", Color: "#264653", Children: []DefaultCategorySeed{
		{Name: "SUBE", Icon: "ticket"},
		{Name: "Taxi y apps", Icon: "car-taxi-front"},
	}},
	{Name: "Combustible", Icon: "fuel", Color: "#264653"},
	{Name: "Expensas", Icon: "building", Color: "#6D597A"},
	{Name: "Alquiler", Icon: "key-round", Color: "#6D597A"},
	{Name: "Servicios", Icon: "lightbulb", Color: "#F4A261", Children: []DefaultCategorySeed{
		{Name: "Luz", Icon: "zap"},
		{Name: "Gas", Icon: "flame"},
		{Name: "Agua", Icon: "droplet"},
		{Name: "Internet", Icon: "wifi"},
		{Name: "Celular", Icon: "smartphone"},
	}},
	{Name: "Salud", Icon: "heart-pulse", Color: "#6A994E", Children: []DefaultCategorySeed{
		{Name: "Prepaga", Icon: "briefcase-medical"},
		{Name: "Farmacia", Icon: "pill"},
	}},
	{Name: "Educación", Icon: "graduation-cap", Color: "#3A86FF"},
	{Name: "Suscripciones", Icon: "tv", Color: "#8338EC"},
	{Name: "Mascotas", Icon: "paw-print", Color: "#BC6C25"},
	{Name: "Ropa", Icon: "shirt", Color: "#D62828"},
	{Name: "Hogar", Icon: "sofa", Color: "#6D597A"},
	{Name: "Regalos", Icon: "gift", Color: "#D62828"},
	{Name: "Viajes", Icon: "plane", Color: "#3A86FF"},
	{Name: "Impuestos", Icon: "landmark", Color: "#6C757D"},
	{Name: "Otros", Icon: "package", Color: "#6C757D"},
}
