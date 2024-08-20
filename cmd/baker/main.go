package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"ella.to/baker"
	"ella.to/baker/driver"
	"ella.to/baker/internal/acme"
	"ella.to/baker/internal/httpclient"
	"ella.to/baker/internal/metrics"
	"ella.to/baker/rule"
)

var Version = "master"
var GitCommit = "development"

func main() {
	fmt.Fprintf(os.Stdout, `

██████╗░░█████╗░██╗░░██╗███████╗██████╗░
██╔══██╗██╔══██╗██║░██╔╝██╔════╝██╔══██╗
██████╦╝███████║█████═╝░█████╗░░██████╔╝
██╔══██╗██╔══██║██╔═██╗░██╔══╝░░██╔══██╗
██████╦╝██║░░██║██║░╚██╗███████╗██║░░██║
╚═════╝░╚═╝░░╚═╝╚═╝░░╚═╝╚══════╝╚═╝░░╚═╝
                           
Version: %s
Git Hash: %s 
https://ella.to/baker
`, Version, GitCommit)

	acmePath := os.Getenv("BAKER_ACME_PATH")
	acmeEnable := strings.ToLower(os.Getenv("BAKER_ACME")) == "yes"
	logLevel := strings.ToLower(os.Getenv("BAKER_LOG_LEVEL"))
	bufferSize := parseInt(os.Getenv("BAKER_BUFFER_SIZE"), 100)
	pingDuration := parseDuration(os.Getenv("BAKER_PING_DURATION"), 2*time.Second)
	metricsAddr := os.Getenv("BAKER_METRICS_ADDR")
	if metricsAddr == "" {
		metricsAddr = "0.0.0.0:8089"
	}

	slog.SetLogLoggerLevel(parseLogLevel(logLevel))

	metricsHandler := metrics.SetupHandler()

	dockerGetter, err := httpclient.NewClient(
		httpclient.WithUnixSock("/var/run/docker.sock", "http://localhost"),
	)
	if err != nil {
		slog.Error("failed to create http client", "error", err)
		os.Exit(1)
	}

	docker := driver.NewDocker(dockerGetter)

	handler := baker.NewServer(
		baker.WithBufferSize(bufferSize),
		baker.WithPingDuration(pingDuration),
		baker.WithRules(
			rule.RegisterAppendPath(),
			rule.RegisterReplacePath(),
			rule.RegisterRateLimiter(),
		),
	)
	handler.RegisterDriver(docker.RegisterDriver)

	metricsServer := http.Server{
		Addr:    metricsAddr,
		Handler: metricsHandler,
	}

	defer metricsServer.Shutdown(context.Background())

	go func() {
		slog.Info("starting metrics server", "addr", metricsAddr)
		err := metricsServer.ListenAndServe()
		if err != nil {
			slog.Error("failed to start metrics server", "error", err)
		}
	}()

	if acmeEnable {
		slog.Info("starting acme server", "addr", acmePath)
		err := acme.Start(handler, acmePath)
		if err != nil {
			slog.Error("failed to start acme", "error", err)
			os.Exit(1)
		}
	} else {
		slog.Info("starting server", "addr", ":80")
		err := http.ListenAndServe(":80", handler)
		if err != nil {
			slog.Error("failed to start server", "error", err)
		}
	}
}

func parseDuration(s string, defaultValue time.Duration) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultValue
	}

	return d
}

func parseInt(s string, defaultValue int) int {
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return defaultValue
	}

	return int(i)
}

func parseLogLevel(logLevel string) slog.Level {
	switch logLevel {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
