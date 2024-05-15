package main

import (
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"net/http"
	"time"
)

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func init() {
	rand.Seed(time.Now().UnixNano())
}

func generateRandomString(length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}
func main() {
	id := generateRandomString(8)

	http.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		slog.Info("request", "host", r.Host, "path", r.URL.Path)
		fmt.Fprint(w, `{"endpoints": [{"domain": "example.com", "path":"/api/v1"}]}`)
	})

	http.HandleFunc("/api/v1", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Received from %s: %s\n", id, r.URL.Path)
	})

	fmt.Printf("Starting server at port 8000\n")

	if err := http.ListenAndServe(":8000", nil); err != nil {
		log.Fatal(err)
	}
}
