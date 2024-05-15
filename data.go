package baker

import (
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
}

type Config struct {
	Endpoints []Endpoint `json:"endpoints"`
}

type Driver interface {
	Add(*Container)
	Remove(*Container)
}
