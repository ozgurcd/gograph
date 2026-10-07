package app

import (
	"example.test/answers1711/paths"
	"net/http"
)

const Admin = "/admin"

type APIResource struct{ TokenTTLSecs int }
type Client struct{ TokenTTLSecs *int }
type LoadedClient struct{ Lifetime *int }

type Resource interface{ Lifetime() int }

func (r *APIResource) Lifetime() int { return r.TokenTTLSecs }

type Wrapper struct{ APIResource }

func LoadResource() *APIResource { return &APIResource{} }
func LoadClient() *Client        { return &Client{} }
func Update() {
	r := LoadResource()
	r.TokenTTLSecs = 60
	c := LoadClient()
	v := 90
	c.TokenTTLSecs = &v
}
func Handler(http.ResponseWriter, *http.Request) { Update() }
func Register(mux *http.ServeMux, dynamic string) {
	mux.HandleFunc("GET "+Admin, Handler)
	mux.HandleFunc("POST "+Admin+paths.Resource, Handler)
	mux.HandleFunc("DELETE "+paths.Resource, Handler)
	mux.HandleFunc(dynamic, Handler)
}
