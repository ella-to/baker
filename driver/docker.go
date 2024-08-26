package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"

	"ella.to/baker"
	"ella.to/baker/internal/httpclient"
)

type Docker struct {
	driver baker.Driver
	getter httpclient.GetterFunc
	close  chan struct{}
}

type Label struct {
	Enable  bool
	Network string
	Service struct {
		Port int
		Ping string

		Static struct {
			Domain  string
			Path    string
			Headers map[string]string
		}
	}
}

func parseLabels(labels map[string]string) (*Label, error) {
	var err error

	l := &Label{}

	for key, value := range labels {
		switch key {
		case "baker.enable":
			l.Enable = strings.ToLower(value) == "true"
		case "baker.network":
			l.Network = value
		case "baker.service.port":
			l.Service.Port, err = strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("failed to parse port because %s", err)
			}

		case "baker.service.ping":
			l.Service.Ping = value
		case "baker.service.static.domain":
			l.Service.Static.Domain = value
		case "baker.service.static.path":
			l.Service.Static.Path = value
		default:
			if strings.HasPrefix(key, "baker.service.static.headers.") {
				if l.Service.Static.Headers == nil {
					l.Service.Static.Headers = make(map[string]string)
				}
				l.Service.Static.Headers[strings.TrimPrefix(key, "baker.service.static.headers.")] = value
			}
		}
	}

	return l, nil
}

func (d *Docker) loadContainerById(ctx context.Context, id string) (*baker.Container, error) {
	r, _, err := d.getter(ctx, "/containers/"+id+"/json")
	if err != nil {
		return nil, err
	}
	defer r.Close()

	payload := struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string `json:"IPAddress"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
		ID string `json:"Id"`
	}{}

	err = json.NewDecoder(r).Decode(&payload)
	if err != nil {
		return nil, err
	}

	labels, err := parseLabels(payload.Config.Labels)
	if err != nil {
		return nil, fmt.Errorf("failed to parse labels for container '%s' because %s", id, err)
	}

	if !labels.Enable {
		return nil, fmt.Errorf("label 'baker.enable' is not set to true")
	}

	network, ok := payload.NetworkSettings.Networks[labels.Network]
	if !ok {
		return nil, fmt.Errorf("network '%s' not exists in labels", labels.Network)
	}

	var addr netip.AddrPort

	if network.IPAddress != "" {
		addr, err = netip.ParseAddrPort(fmt.Sprintf("%s:%d", network.IPAddress, labels.Service.Port))
		if err != nil {
			return nil, fmt.Errorf("failed to parse address for container '%s' because %s", id, err)
		}
	}

	slog.Debug("docker driver loaded container", "id", id, "addr", addr, "config", labels.Service.Ping)

	container := &baker.Container{
		Id:         id,
		Addr:       addr,
		ConfigPath: labels.Service.Ping,
	}

	container.Meta.Static.Domain = labels.Service.Static.Domain
	container.Meta.Static.Path = labels.Service.Static.Path
	container.Meta.Static.Headers = labels.Service.Static.Headers

	return container, nil
}

func (d *Docker) loadCurrentContainers(ctx context.Context) {
	r, _, err := d.getter(ctx, "/containers/json")
	if err != nil {
		slog.Error("failed to get containers", "error", err)
		return
	}
	defer r.Close()

	events := []struct {
		ID    string `json:"Id"`
		State string `json:"State"`
	}{}

	err = json.NewDecoder(r).Decode(&events)
	if err != nil {
		slog.Error("failed to decode containers", "error", err)
		return
	}

	for _, event := range events {
		var container *baker.Container

		if event.State != "running" {
			continue
		}

		slog.Debug("docker driver received current event", "id", event.ID, "state", event.State)

		container, err := d.loadContainerById(ctx, event.ID)
		if err != nil {
			slog.Error("failed to load container", "id", event.ID, "error", err)
			continue
		}

		d.driver.Add(container)
	}
}

func (d *Docker) loadFutureContainers(ctx context.Context) {
	r, _, err := d.getter(ctx, "/events")
	if err != nil {
		return
	}
	defer r.Close()

	decoder := json.NewDecoder(r)

	event := struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}{}

	for {
		event.ID = ""
		event.Status = ""

		if err := decoder.Decode(&event); err != nil {
			slog.Error("failed to decode event", "error", err)
			continue
		}

		slog.Debug("docker driver received future event", "id", event.ID, "status", event.Status)

		if event.Status == "die" {
			d.driver.Remove(&baker.Container{Id: event.ID})
			continue
		}

		if event.Status != "die" && event.Status != "start" {
			continue
		}

		container, err := d.loadContainerById(ctx, event.ID)
		if err != nil {
			slog.Error("failed to load container", "id", event.ID, "error", err)
			continue
		}

		d.driver.Add(container)
	}
}

func (d *Docker) run() {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-d.close
		cancel()
	}()

	d.loadCurrentContainers(ctx)
	d.loadFutureContainers(ctx)
}

func (d *Docker) Close() {
	close(d.close)
}

func (d *Docker) RegisterDriver(driver baker.Driver) {
	if d.driver != nil {
		panic("driver already registered")
	}

	d.driver = driver
	go d.run()
}

func NewDocker(getter httpclient.Getter) *Docker {
	return &Docker{
		getter: getter.Get,
		close:  make(chan struct{}),
	}
}
