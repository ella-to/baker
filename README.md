```
██████╗░░█████╗░██╗░░██╗███████╗██████╗░
██╔══██╗██╔══██╗██║░██╔╝██╔════╝██╔══██╗
██████╦╝███████║█████═╝░█████╗░░██████╔╝
██╔══██╗██╔══██║██╔═██╗░██╔══╝░░██╔══██╗
██████╦╝██║░░██║██║░╚██╗███████╗██║░░██║
╚═════╝░╚═╝░░╚═╝╚═╝░░╚═╝╚══════╝╚═╝░░╚═╝
```

<div align="center">

[![Go Reference](https://pkg.go.dev/badge/ella.to/baker.svg)](https://pkg.go.dev/ella.to/baker)
[![Go Report Card](https://goreportcard.com/badge/ella.to/baker)](https://goreportcard.com/report/ella.to/baker)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

</div>

## Introduction

**Baker** is a dynamic HTTP reverse proxy with a focus on extensibility and flexibility. It is designed to adapt to a variety of orchestration engines and provides dynamic configuration capabilities, eliminating the need to restart the reverse proxy when changing configurations.

## Features

| Feature | Description |
|---------|-------------|
| 🐳 **Docker Integration** | Native Docker driver for real-time container event listening |
| 🔌 **Pluggable Drivers** | Exposed driver interface for easy integration with other orchestration engines |
| ⚡ **Dynamic Config** | Hot-reload configuration without restarts |
| 🌳 **Fast Routing** | Custom trie data structure for high-performance path pattern matching |
| 📚 **Library Mode** | Use as a library—implements the standard `http.Handler` interface |
| 🔧 **Extensible** | Exposed interfaces for most components |
| 🔀 **Middleware** | Modify incoming and outgoing traffic with built-in or custom middleware |
| ⚖️ **Load Balancing** | Built-in load balancing across service instances |
| 🔒 **Auto SSL** | Automatic HTTPS: Let's Encrypt for public domains + local CA certs for `*.localhost` |
| 🚦 **Rate Limiting** | Configurable rate limiter per domain and path |
| 📊 **Metrics** | Prometheus metrics available at `/metrics` endpoint |
| 📋 **Static Config** | Support for services without dynamic configuration endpoints |
| 🔄 **WebSocket** | Full WebSocket proxy support |

## Quick Start

### 1. Deploy Baker

Create a `docker-compose.yml` for Baker:

```yaml
services:
  baker:
    image: ellato/baker:latest
    environment:
      - BAKER_ACME=NO              # Enable/disable ACME (Let's Encrypt)
      - BAKER_ACME_PATH=/acme/cert # Certificate storage path
      - BAKER_LOG_LEVEL=DEBUG      # Log level: DEBUG, INFO, WARN, ERROR
      - BAKER_BUFFER_SIZE=100      # Event buffer size
      - BAKER_PING_DURATION=2s     # Health check interval
      - BAKER_METRICS_ADDR=:8089   # Metrics endpoint address
      # OpenTelemetry (service.name/version are set by baker at build time)
      - OTEL_DEPLOYMENT_ENVIRONMENT=dev
      - OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317
      - OTEL_EXPORTER_OTLP_INSECURE=true
      - OTEL_TRACES_SAMPLER=parentbased_traceidratio
      - OTEL_TRACES_SAMPLER_ARG=1.0
      - OTEL_METRIC_EXPORT_INTERVAL=15s
      - ELLA_OTEL_DISABLED=false
      - ELLA_OTEL_LOGS_ENABLED=true
      - ELLA_OTEL_LOGS_STDOUT=true
      - ELLA_OTEL_LOGS_LEVEL=info
    ports:
      - "80:80"
      - "443:443"
    networks:
      - baker
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./acme/cert:/acme/cert

networks:
  baker:
    name: baker
    driver: bridge
```

### 2. Configure Your Service

Add Baker labels to your service's `docker-compose.yml`:

```yaml
services:
  my-service:
    image: my-service:latest
    labels:
      - "baker.enable=true"
      - "baker.network=baker"
      - "baker.service.port=8000"
      - "baker.service.ping=/config"
      # Optional: Static configuration (for non-dynamic services)
      - "baker.service.static.domain=api.example.com"
      - "baker.service.static.path=/*"
      - "baker.service.static.headers.host=api.example.com"
    networks:
      - baker

networks:
  baker:
    name: baker
    external: true
```

> **Note:** Ensure both Baker and your service share the same Docker network.

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `BAKER_ACME` | `NO` | Enable HTTPS (`YES`): Let's Encrypt for public domains and local certificates for `*.localhost` |
| `BAKER_ACME_PATH` | `/acme/cert` | Directory for SSL certificates |
| `BAKER_LOG_LEVEL` | `INFO` | Logging level |
| `BAKER_BUFFER_SIZE` | `100` | Docker event buffer size |
| `BAKER_PING_DURATION` | `2s` | Service health check interval |
| `BAKER_METRICS_ADDR` | `:8089` | Prometheus metrics endpoint |

### OpenTelemetry Environment Variables

Baker initialises the `ella.to/otel` SDK at startup and **always overrides
`service.name` (`baker`) and `service.version` (injected at build time)**,
so setting `OTEL_SERVICE_NAME` or `OTEL_SERVICE_VERSION` has no effect.

| Variable | Default | Description |
|----------|---------|-------------|
| `OTEL_DEPLOYMENT_ENVIRONMENT` | `dev` | Sets `deployment.environment` resource attribute |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OTLP gRPC collector endpoint (`host:port` or `http(s)://…`) |
| `OTEL_EXPORTER_OTLP_INSECURE` | `true` | Disable TLS for plain `:4317` |
| `OTEL_EXPORTER_OTLP_HEADERS` | *(none)* | Extra headers sent to the collector (`key=value,key=value`) |
| `OTEL_TRACES_SAMPLER` | `parentbased_traceidratio` | Head sampler (`always_on`, `always_off`, `parentbased_traceidratio`, …) |
| `OTEL_TRACES_SAMPLER_ARG` | `1.0` | Sampling ratio when sampler is `*_traceidratio` |
| `OTEL_METRIC_EXPORT_INTERVAL` | `15s` | Metric export interval (duration string) |
| `OTEL_RESOURCE_ATTRIBUTES` | *(none)* | Extra resource attributes (`region=fra1,role=edge`) |
| `ELLA_OTEL_DISABLED` | `false` | Install no-op providers (disables all telemetry) |
| `ELLA_OTEL_LOGS_ENABLED` | `true` | Export logs via OTLP |
| `ELLA_OTEL_LOGS_STDOUT` | `true` | Also write JSON logs to stdout |
| `ELLA_OTEL_LOGS_LEVEL` | `info` | Minimum log level (`debug`/`info`/`warn`/`error`) |

### Service Labels

| Label | Required | Description |
|-------|----------|-------------|
| `baker.enable` | Yes | Enable Baker for this container |
| `baker.network` | Yes | Docker network name |
| `baker.service.port` | Yes | Service port to proxy to |
| `baker.service.ping` | Yes | Health check / config endpoint |
| `baker.service.static.domain` | No | Static domain (bypasses dynamic config) |
| `baker.service.static.path` | No | Static path pattern |
| `baker.service.static.headers.*` | No | Custom headers to add |

### Dynamic Configuration Endpoint

Your service should expose a REST endpoint (specified by `baker.service.ping`) that returns routing configuration:

```json
[
  {
    "domain": "example.com",
    "path": "/api/v1",
    "ready": true
  },
  {
    "domain": "example.com",
    "path": "/api/v2",
    "ready": false
  },
  {
    "domain": "app.example.com",
    "path": "/app/*",
    "ready": true,
    "rules": [
      {
        "type": "ReplacePath",
        "args": {
          "search": "/app",
          "replace": "",
          "times": 1
        }
      }
    ]
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `domain` | string | Domain to match |
| `path` | string | Path pattern (supports `*` wildcard) |
| `ready` | boolean | Whether the route is active |
| `rules` | array | Optional middleware rules |

## HTTPS for Localhost Domains

When `BAKER_ACME=YES`, Baker supports both public and local development HTTPS:

- Public domains (e.g. `api.example.com`) use Let's Encrypt certificates.
- Localhost domains (e.g. `app.localhost`, `api.localhost`) use certificates generated locally by Baker.

Localhost certificate files are written under:

- `<BAKER_ACME_PATH>/localhost/ca.crt`
- `<BAKER_ACME_PATH>/localhost/*.crt`
- `<BAKER_ACME_PATH>/localhost/*.key`

To trust localhost certificates on macOS (system-wide, requires admin):

```bash
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain <BAKER_ACME_PATH>/localhost/ca.crt
```

To trust without sudo for only the current user (login keychain):

```bash
security add-trusted-cert -d -r trustRoot -k ~/Library/Keychains/login.keychain-db <BAKER_ACME_PATH>/localhost/ca.crt
```

After trusting the CA, domains that are already registered in Baker (for example via labels or dynamic config) can be served over HTTPS as `https://<name>.localhost`.

## Middleware

Baker includes several built-in middleware for request/response modification:

### ReplacePath

Replaces or removes parts of the request path before forwarding to the backend service.

```json
{
  "type": "ReplacePath",
  "args": {
    "search": "/api/v1",
    "replace": "",
    "times": 1
  }
}
```

| Argument | Type | Description |
|----------|------|-------------|
| `search` | string | Pattern to search for |
| `replace` | string | Replacement string |
| `times` | integer | Number of replacements (`-1` for all) |

**Example:** Request to `/api/v1/users` → Backend receives `/users`

---

### AppendPath

Adds prefixes and/or suffixes to the request path.

```json
{
  "type": "AppendPath",
  "args": {
    "begin": "/v2",
    "end": ".json"
  }
}
```

| Argument | Type | Description |
|----------|------|-------------|
| `begin` | string | Prefix to add |
| `end` | string | Suffix to add |

**Example:** Request to `/users` → Backend receives `/v2/users.json`

---

### RateLimiter

Applies rate limiting per client IP address. Supports two algorithms:

- **Sliding Window** (`window`): Smooths out bursts by considering requests from the previous window. Default algorithm.
- **Token Bucket** (`bucket`): Allows controlled bursts up to the bucket capacity, with tokens refilling over time.

**Sliding Window (default):**

```json
{
  "type": "RateLimiter",
  "args": {
    "request_limit": 100,
    "window_duration": "60s"
  }
}
```

**Token Bucket:**

```json
{
  "type": "RateLimiter",
  "args": {
    "algo": "bucket",
    "request_limit": 100,
    "window_duration": "60s"
  }
}
```

| Argument | Type | Description |
|----------|------|-------------|
| `algo` | string | Algorithm: `window` (default) or `bucket` |
| `request_limit` | integer | Maximum requests per window / bucket capacity |
| `window_duration` | string | Time window or refill duration (e.g., `60s`, `1m`, `1h`) |

When the limit is exceeded, clients receive a `429 Too Many Requests` response.

## License

Baker is licensed under the [MIT LICENSE](LICENSE.md).
