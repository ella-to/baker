package baker

import (
	"encoding/json"
	"net/netip"
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

type Driver interface {
	Add(*Container)
	Remove(*Container)
}
