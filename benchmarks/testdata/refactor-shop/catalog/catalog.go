package catalog

import "context"

type Product struct {
	ID    string
	Stock int
}

type ProductRepository interface {
	Find(context.Context, string) (Product, error)
	Save(context.Context, Product) error
}
