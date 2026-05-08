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
	bakerotel "ella.to/baker/internal/otel"
	"ella.to/baker/rule"
)

var (
	Version   = "master"
	GitCommit = "development"
)

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

	ctx := context.Background()

	acmePath := os.Getenv("BAKER_ACME_PATH")
	acmeEnable := strings.ToLower(os.Getenv("BAKER_ACME")) == "yes"
	logLevel := strings.ToLower(os.Getenv("BAKER_LOG_LEVEL"))
	bufferSize := parseInt(os.Getenv("BAKER_BUFFER_SIZE"), 100)
	pingDuration := parseDuration(os.Getenv("BAKER_PING_DURATION"), 2*time.Second)

	slog.SetLogLoggerLevel(parseLogLevel(logLevel))

	defer bakerotel.Init(ctx, Version+"-"+GitCommit)()

	dockerGetter, err := httpclient.NewClient(
		httpclient.WithUnixSock("/var/run/docker.sock", "http://localhost"),
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create http client", "error", err)
		os.Exit(1)
	}

	docker := driver.NewDocker(dockerGetter)

	srv := baker.NewServer(
		baker.WithBufferSize(bufferSize),
		baker.WithPingDuration(pingDuration),
		baker.WithRules(
			rule.RegisterAppendPath(),
			rule.RegisterReplacePath(),
			rule.RegisterRateLimiter(),
		),
	)
	srv.RegisterDriver(docker.RegisterDriver)

	handler := bakerotel.NewHandler(srv)

	if acmeEnable {
		slog.InfoContext(
			ctx,
			"starting tls server",
			"acme_cache_path", acmePath,
			"localhost_ca_cert_path", acme.LocalhostCAPath(acmePath),
		)
		err := acme.Start(handler, acmePath, func(ctx context.Context, host string) error {
			if srv.HasDomain(ctx, host) {
				return nil
			}
			slog.WarnContext(ctx, "acme: rejecting certificate request for unregistered domain", "host", host)
			return acme.ErrHostNotAllowed
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to start acme", "error", err)
			os.Exit(1)
		}
	} else {
		slog.InfoContext(ctx, "starting server", "addr", ":80")
		err := http.ListenAndServe(":80", handler)
		if err != nil {
			slog.ErrorContext(ctx, "failed to start server", "error", err)
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
