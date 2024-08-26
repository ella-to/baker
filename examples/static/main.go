package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
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

	http.HandleFunc("/api/v1", func(w http.ResponseWriter, r *http.Request) {
		for key, value := range r.Header {
			fmt.Printf("%s: %s\n", key, strings.Join(value, ","))
		}
		fmt.Fprintf(w, "Received from %s: %s%s\n", id, r.Host, r.URL.Path)
	})

	fmt.Printf("Starting server at port 8000\n")

	if err := http.ListenAndServe(":8000", nil); err != nil {
		log.Fatal(err)
	}
}
