package httpclient

import (
	"net/http"
)

func fetchUsers() {
	resp, err := http.Get("https://api.example.com/users")
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
}

func createUser() {
	resp, err := http.Post("https://api.example.com/users", "application/json", nil)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
}

func login() {
	http.PostForm("https://api.example.com/login", nil)
}

func healthCheck() {
	http.Head("https://api.example.com/health")
}

func multiSegment() {
	http.Get("https://user-svc/api/v2/users")
}
