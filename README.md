# K-O11y Install

[English](README.md) | [한국어](README.ko.md)

Installation tools for [K-O11y](https://github.com/Wondermove-Inc/k-o11y-server) — a self-hosted Kubernetes observability platform.


Built by [Wondermove](https://wondermove.net).

## What's Included

| Component | Description |
|-----------|-------------|
| **Helm Charts** | Host cluster (SigNoz + OTel + Core API) and Agent cluster charts |
| **Go CLI Tools** | `k-o11y-db` (ClickHouse/Keeper installer) and `k-o11y-tls` (TLS setup) |
| **Shell Scripts** | ClickHouse DDL, S3 tiering, Glacier cold backup, codec conversion |
| **SQL Schemas** | ClickHouse DDL, S3 config, SSO config, data lifecycle config |

## Architecture

```
Host Cluster (Helm)                    Agent Cluster (Helm)
┌─────────────────────┐                ┌──────────────────┐
│ SigNoz (Hub)        │                │ OTel DaemonSet   │
│ OTel Collector (GW) │ ◄── OTLP ──── │ OTel Deployment  │
│ Core API            │                │ Beyla (eBPF)     │
└─────────┬───────────┘                └──────────────────┘
          │
          ▼
┌─────────────────────┐
│ ClickHouse (VM)     │
│ + Keeper            │
│ + S3 Tiering        │
└─────────────────────┘
```

## Project Structure

```
k-o11y-install/
├── charts/
│   ├── k-o11y-host/    # Host cluster Helm chart
│   ├── k-o11y-agent/   # Agent cluster Helm chart
│   ├── k-o11y-otel-agent/               # OTel agent sub-chart
│   ├── k-o11y-apm-agent/                # APM agent sub-chart
│   ├── k-o11y-ksm/                      # Kube State Metrics sub-chart
│   └── k-o11y-otel-operator/            # OTel Operator sub-chart
│
├── cmd/
│   ├── k-o11y-db/     # Go CLI: ClickHouse/Keeper installer + DB agent
│   └── k-o11y-tls/    # Go CLI: TLS certificate setup
│
├── upstream-versions.yaml              # Upstream image version tracking
└── README.md
```

## Before You Start

> **Pre-built Docker images and Helm charts are not provided.**
> You must build images from the [main repository](https://github.com/Wondermove-Inc/k-o11y-server) and push to your own registry.
> Then update `values.yaml` image registries to point to your registry before deploying.

```bash
# Example: package and push Helm charts to your registry
helm package charts/k-o11y-host
helm push k-o11y-host-*.tgz oci://<your-registry>/charts
```

## Quick Start

### 1. Install ClickHouse + Keeper (VM)

```bash
cd cmd/k-o11y-db
go build -o k-o11y-db .

# Interactive install
./k-o11y-db install
```

### 2. Deploy Host Cluster (Helm)

```bash
helm install k-o11y charts/k-o11y-host \
  --namespace k-o11y --create-namespace \
  --set externalClickhouse.host=<YOUR_CLICKHOUSE_IP> \
  --set externalClickhouse.password=<YOUR_PASSWORD>
```

### 3. Deploy Agent Cluster (Helm)

```bash
helm install k-o11y-agent charts/k-o11y-agent \
  --namespace k-o11y --create-namespace \
  --set k-o11y-otel-agent.otelCollectorEndpoint=<HOST_OTLP_IP>:4317
```

## Go CLI Tools

### k-o11y-db

Interactive ClickHouse/Keeper VM installer with SSH-based deployment.

```bash
cd cmd/k-o11y-db
go build -o k-o11y-db .

# Install
./k-o11y-db install

# Post-install (DDL, S3 config)
./k-o11y-db post-install

# Start DB agent (S3 activator, cold backup)
./k-o11y-db agent start
```

### k-o11y-tls

TLS certificate setup for OTel Collector (cert-manager, Let's Encrypt, self-signed).

```bash
cd cmd/k-o11y-tls
go build -o k-o11y-tls .
./k-o11y-tls setup
```

## Related Repositories

| Repository | Description |
|-----------|-------------|
| [k-o11y](https://github.com/Wondermove-Inc/k-o11y-server) | Main platform (SigNoz fork + custom extensions) |
| [k-o11y-otel-collector](https://github.com/Wondermove-Inc/k-o11y-otel-collector) | Custom OTel Collector with CRD processor |
| [k-o11y-otel-gateway](https://github.com/Wondermove-Inc/k-o11y-otel-gateway) | SigNoz OTel Collector with License Guard |

## Maintainers

Built and maintained by [Wondermove](https://wondermove.net).

## License

MIT License - See [LICENSE](LICENSE)
