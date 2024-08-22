package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"

	"ella.to/baker"
	"ella.to/baker/internal/httpclient"
)

type Docker struct {
	driver baker.Driver
	getter httpclient.GetterFunc
	close  chan struct{}
}

func (d *Docker) loadContainerById(ctx context.Context, id string) (*baker.Container, *baker.MetaData, error) {
	r, _, err := d.getter(ctx, "/containers/"+id+"/json")
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()

	payload := struct {
		Config struct {
			Labels struct {
				Enable       string `json:"baker.enable"`
				Network      string `json:"baker.network"`
				ServicePort  string `json:"baker.service.port"`
				ServicePing  string `json:"baker.service.ping"`
				StaticDomain string `json:"baker.service.static.domain"`
				StaticPath   string `json:"baker.service.static.path"`
			} `json:"Labels"`
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
		return nil, nil, err
	}

	if payload.Config.Labels.Enable != "true" {
		return nil, nil, fmt.Errorf("label 'baker.enable' is not set to true")
	}

	network, ok := payload.NetworkSettings.Networks[payload.Config.Labels.Network]
	if !ok {
		fmt.Println(payload.NetworkSettings.Networks)
		return nil, nil, fmt.Errorf("network '%s' not exists in labels", payload.Config.Labels.Network)
	}

	port, err := strconv.ParseInt(payload.Config.Labels.ServicePort, 10, 32)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse port for container '%s' because %s", id, err)
	}

	var addr netip.AddrPort

	if network.IPAddress != "" {
		addr, err = netip.ParseAddrPort(fmt.Sprintf("%s:%d", network.IPAddress, port))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse address for container '%s' because %s", id, err)
		}
	}

	slog.Debug("docker driver loaded container", "id", id, "addr", addr, "config", payload.Config.Labels.ServicePing)

	return &baker.Container{
			Id:         id,
			Addr:       addr,
			ConfigPath: payload.Config.Labels.ServicePing,
		}, &baker.MetaData{
			StaticDomain: payload.Config.Labels.StaticDomain,
			StaticPath:   payload.Config.Labels.StaticPath,
		}, nil
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

		container, meta, err := d.loadContainerById(ctx, event.ID)
		if err != nil {
			slog.Error("failed to load container", "id", event.ID, "error", err)
			continue
		}

		d.driver.Add(container, meta)
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

		container, meta, err := d.loadContainerById(ctx, event.ID)
		if err != nil {
			slog.Error("failed to load container", "id", event.ID, "error", err)
			continue
		}

		d.driver.Add(container, meta)
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
