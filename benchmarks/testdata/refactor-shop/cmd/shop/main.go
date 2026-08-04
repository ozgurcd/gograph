package main

import (
	"log"
	"net/http"

	"example.com/refactorshop/catalog"
	"example.com/refactorshop/httpapi"
	"example.com/refactorshop/memory"
	"example.com/refactorshop/service"
)

func main() {
	repository := memory.NewRepository(catalog.Product{ID: "gopher", Stock: 10})
	checkout := service.NewCheckoutService(repository)
	handler := httpapi.NewHandler(checkout)
	mux := http.NewServeMux()
	httpapi.Register(mux, handler)
	log.Fatal(http.ListenAndServe(":8080", mux))
}
