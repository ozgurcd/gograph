package service

import (
	"context"
	"fmt"

	"example.com/refactorshop/catalog"
)

type CheckoutService struct {
	repository catalog.ProductRepository
}

func NewCheckoutService(repository catalog.ProductRepository) *CheckoutService {
	return &CheckoutService{repository: repository}
}

func ReserveStock(product catalog.Product) (catalog.Product, error) {
	if product.Stock == 0 {
		return catalog.Product{}, fmt.Errorf("product %q is out of stock", product.ID)
	}
	product.Stock--
	return product, nil
}

func (s *CheckoutService) Checkout(ctx context.Context, productID string) (catalog.Product, error) {
	product, err := s.repository.Find(ctx, productID)
	if err != nil {
		return catalog.Product{}, fmt.Errorf("find product: %w", err)
	}
	product, err = ReserveStock(product)
	if err != nil {
		return catalog.Product{}, err
	}
	if err := s.repository.Save(ctx, product); err != nil {
		return catalog.Product{}, fmt.Errorf("save product: %w", err)
	}
	return product, nil
}
