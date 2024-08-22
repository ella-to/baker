package baker

import (
	"encoding/json"
	"net/netip"
	"strings"
)

type Container struct {
	Id         string
	ConfigPath string
	Addr       netip.AddrPort
}

type Endpoint struct {
	Domain string `json:"domain"`
	Path   string `json:"path"`
	Rules  []Rule `json:"rules"`
}

func (e *Endpoint) getHashKey() string {
	var sb strings.Builder

	sb.WriteString(e.Domain)
	sb.WriteString(e.Path)

	return sb.String()
}

type Rule struct {
	Type string          `json:"type"`
	Args json.RawMessage `json:"args"`
}

type Config struct {
	Endpoints []Endpoint `json:"endpoints"`
}

type Service struct {
	Containers []*Container
	Endpoint   *Endpoint
}

type MetaData struct {
	StaticDomain string
	StaticPath   string
}

type Driver interface {
	Add(*Container, *MetaData)
	Remove(*Container)
}
