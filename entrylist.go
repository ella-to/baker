package baker

import (
	"encoding/json"
	"net/http"
)

type entryList struct {
	cahced     []byte
	collection []struct {
		Domain string `json:"domain"`
		Path   string `json:"path"`
		Rules  []struct {
			Type string `json:"type"`
			Args any    `json:"args"`
		} `json:"rules"`
	}
}

func (e *entryList) New(domain, path string, ready bool) *entryList {
	if !ready {
		return e
	}

	e.collection = append(e.collection, struct {
		Domain string `json:"domain"`
		Path   string `json:"path"`
		Rules  []struct {
			Type string `json:"type"`
			Args any    `json:"args"`
		} `json:"rules"`
	}{
		Domain: domain,
		Path:   path,
		Rules: []struct {
			Type string `json:"type"`
			Args any    `json:"args"`
		}{},
	})

	return e
}

func (e *entryList) WithRules(rules ...struct {
	Type string `json:"type"`
	Args any    `json:"args"`
}) *entryList {
	if len(e.collection) == 0 {
		return e
	}

	e.collection[len(e.collection)-1].Rules = rules

	return e
}

func (e *entryList) getPayload() any {
	return struct {
		Endpoints any `json:"endpoints"`
	}{
		Endpoints: e.collection,
	}
}

// CacheResponse caches the response and this can be used to optimize the response
// If you call this method, the next call should be WriteResponse
func (e *entryList) CacheResponse() *entryList {
	e.cahced, _ = json.Marshal(e.getPayload())
	return e
}

func (e *entryList) WriteResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if len(e.cahced) > 0 {
		w.Write(e.cahced)
		return
	}
	json.NewEncoder(w).Encode(e.getPayload())
}

func NewEntryList() *entryList {
	return &entryList{}
}
