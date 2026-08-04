package service_test

import (
	"context"
	"testing"

	"example.com/refactorshop/catalog"
	"example.com/refactorshop/memory"
	"example.com/refactorshop/service"
)

func TestCheckoutDecrementsStock(t *testing.T) {
	repository := memory.NewRepository(catalog.Product{ID: "gopher", Stock: 2})
	checkout := service.NewCheckoutService(repository)

	product, err := checkout.Checkout(context.Background(), "gopher")
	if err != nil {
		t.Fatal(err)
	}
	if product.Stock != 1 {
		t.Fatalf("stock = %d, want 1", product.Stock)
	}
}

func TestReserveStockRejectsEmptyInventory(t *testing.T) {
	_, err := service.ReserveStock(catalog.Product{ID: "gopher"})
	if err == nil {
		t.Fatal("ReserveStock returned nil error for empty inventory")
	}
}
