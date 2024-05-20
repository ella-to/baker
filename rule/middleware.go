package rule

import (
	"encoding/json"
	"net/http"
)

type Middleware interface {
	Process(next http.Handler) http.Handler
	IsCachable() bool
	UpdateMiddelware(newImpl Middleware) Middleware
}

type BuilderFunc func(raw json.RawMessage) (Middleware, error)
type RegisterFunc func(map[string]BuilderFunc) error

var Empty = []Middleware{}

func Chain(next http.Handler, rules ...Middleware) http.Handler {
	for i := len(rules) - 1; i >= 0; i-- {
		next = rules[i].Process(next)
	}

	return next
}
