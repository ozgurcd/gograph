package sqlrepo

import (
	"context"
	"database/sql"

	"example.com/refactorshop/catalog"
)

type SQLRepository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *SQLRepository {
	return &SQLRepository{db: db}
}

func (r *SQLRepository) Find(ctx context.Context, id string) (catalog.Product, error) {
	var product catalog.Product
	err := r.db.QueryRowContext(ctx, "SELECT id, stock FROM products WHERE id = ?", id).Scan(&product.ID, &product.Stock)
	return product, err
}

func (r *SQLRepository) Save(ctx context.Context, product catalog.Product) error {
	_, err := r.db.ExecContext(ctx, "UPDATE products SET stock = ? WHERE id = ?", product.Stock, product.ID)
	return err
}
