package httpclient

import (
	"net/http"
)

var globalClient = &http.Client{}

func fetchUsers() {
	resp, err := http.Get("https://api.example.com/users")
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
}

func getUser(id string) {
	globalClient.Get("https://api.example.com/users/" + id)
}

func createUser() {
	resp, _ := http.Post("https://api.example.com/users", "application/json", nil)
	defer resp.Body.Close()
}

func login() {
	http.PostForm("https://api.example.com/login", nil)
}

func healthCheck() {
	http.Head("https://api.example.com/health")
}

func dynamicURL(url string) {
	globalClient.Get(url)
}

func multiSegment() {
	http.Get("https://user-svc/api/v2/users")
}

func doRequest(req *http.Request) {
	globalClient.Do(req)
}
