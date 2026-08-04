package httpapi

import (
	"net/http"
	"strings"

	"example.com/refactorshop/service"
)

type Handler struct {
	checkout *service.CheckoutService
}

func NewHandler(checkout *service.CheckoutService) *Handler {
	return &Handler{checkout: checkout}
}

func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	productID := strings.TrimPrefix(r.URL.Path, "/checkout/")
	if _, err := h.checkout.Checkout(r.Context(), productID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func Register(mux *http.ServeMux, handler *Handler) {
	mux.HandleFunc("/checkout/", handler.Checkout)
}
