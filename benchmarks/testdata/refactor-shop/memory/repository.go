package memory

import (
	"context"
	"fmt"
	"sync"

	"example.com/refactorshop/catalog"
)

type MemoryRepository struct {
	mu       sync.Mutex
	products map[string]catalog.Product
}

func NewRepository(products ...catalog.Product) *MemoryRepository {
	stored := make(map[string]catalog.Product, len(products))
	for _, product := range products {
		stored[product.ID] = product
	}
	return &MemoryRepository{products: stored}
}

func (r *MemoryRepository) Find(_ context.Context, id string) (catalog.Product, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	product, ok := r.products[id]
	if !ok {
		return catalog.Product{}, fmt.Errorf("product %q not found", id)
	}
	return product, nil
}

func (r *MemoryRepository) Save(_ context.Context, product catalog.Product) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.products[product.ID] = product
	return nil
}
