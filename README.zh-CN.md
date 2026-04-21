# K-O11y Install

[English](README.md) | [한국어](README.ko.md) | [日本語](README.ja.md) | [中文](README.zh-CN.md)

K-O11y 安装包。自动化完成 ClickHouse/Keeper VM 安装、DB Agent 部署、TLS 配置及 Kubernetes 集群部署。
本工具由 [Wondermove](https://wondermove.net) 开发，用于安装 [K-O11y](https://github.com/Wondermove-Inc/k-o11y-server)。

---

## 项目简介

K-O11y 是基于 SigNoz 的自托管 K8s 可观测性解决方案。收集指标、日志和链路追踪数据并存储至 ClickHouse，支持 S3 Warm/Cold 存储分层。

### 架构

```
Agent Clusters (多个)                    Host Cluster (中央)
┌──────────────────────┐                 ┌──────────────────────┐
│  OTel Collector      │                 │  o11y-hub (UI+API)   │
│  APM Agent (eBPF)    │  ── OTLP ──>   │  o11y-core           │
│  KSM                 │    (4317)       │  OTel Gateway        │
│  OTel Operator       │                 └──────────┬───────────┘
└──────────────────────┘                            │
                                         ┌──────────▼───────────┐
                                         │  ClickHouse VM       │
                                         │   + DB Agent         │
                                         │  Keeper VM           │
                                         └──────────────────────┘
```

DB Agent 常驻于 ClickHouse VM，通过 DB 轮询自主执行 S3 激活、冷备份及 DROP PARTITION，无需 SSH 依赖。

---

## 项目结构

```
k-o11y-install/
├── cmd/
│   ├── k-o11y-db/      Go 二进制: DB 安装 + Agent + 升级
│   │   ├── cmd/                            cobra 子命令 (install, post-install, uninstall, upgrade, agent)
│   │   ├── internal/
│   │   │   ├── agent/                      DB Agent (daemon, poller, s3_activator, backup, health)
│   │   │   ├── installer/                  安装逻辑 (keeper, clickhouse, agent, uninstall)
│   │   │   ├── ssh/                        SSH 抽象层 (ssh, bastion, local)
│   │   │   └── embed/                      内嵌资源 (DDL, 脚本, 模板)
│   │   └── Makefile                        交叉编译 (linux/darwin, amd64/arm64)
│   │
│   └── k-o11y-tls/     Go 二进制: TLS 证书配置
│       ├── cmd/                            cobra 子命令 (setup)
│       ├── internal/
│       │   ├── tls/                        各模式配置 (existing, selfsigned, private-ca, letsencrypt)
│       │   ├── kube/                       kubectl/helm os/exec 封装
│       │   └── embed/                      YAML 模板 (cert-manager CRD)
│       └── Makefile
│
├── charts/                               Helm Chart
│   ├── k-o11y-host/      Host umbrella chart (SigNoz + OTel Gateway)
│   ├── k-o11y-agent/     Agent umbrella chart
│   ├── k-o11y-otel-agent/                  OTel Collector (子 Chart)
│   ├── k-o11y-otel-operator/               OTel Operator (子 Chart)
│   ├── k-o11y-apm-agent/                   eBPF APM Agent (子 Chart)
│   └── k-o11y-ksm/                         Kube State Metrics (子 Chart)
│
└── upstream-versions.yaml                Upstream 镜像版本追踪
```

### 安装工具

| 二进制 | 用途 | 构建路径 |
|--------|------|---------|
| `k-o11y-db` | DB 安装 + Agent + DDL 应用 + 卸载 | `cmd/k-o11y-db/` |
| `k-o11y-tls` | TLS 证书配置 | `cmd/k-o11y-tls/` |

```bash
cd cmd/k-o11y-db && make build-all
cd cmd/k-o11y-tls && make build-all
```

---

## 开始前须知

> **不提供预构建的 Docker 镜像和 Helm Chart。**
> 需从[主仓库](https://github.com/Wondermove-Inc/k-o11y-server)构建镜像并推送至自有镜像仓库。
> 部署前请将 `values.yaml` 中的镜像仓库地址更新为自有仓库地址。

```bash
# 示例: 打包 Helm Chart 并推送至自有仓库
helm package charts/k-o11y-host
helm push k-o11y-host-*.tgz oci://<your-registry>/charts
```

## 安装指南

### 前置条件

| 项目 | 要求 |
|------|------|
| VM (ClickHouse/Keeper) | Ubuntu 22.04 LTS、sudo SSH 账户、8+ vCPU、32GB+ RAM |
| Host K8s 集群 | Kubernetes 1.28+、Helm 3.12+、kubectl |
| Agent K8s 集群 | Kubernetes 1.28+、Linux 内核 5.8+（eBPF） |
| OCI Registry | 可访问 `<YOUR_REGISTRY>` |

预先生成 K_O11Y_ENCRYPTION_KEY（Step 1 和 Step 3 使用相同密钥）:

```bash
openssl rand -hex 32
```

### Step 1. DB 安装 + Agent 部署

```bash
./k-o11y-db install \
    --mode ssh \
    --ssh-user <SSH_USER> \
    --ssh-key <SSH_KEY_PATH> \
    --ssh-password '<SSH_PASSWORD>' \
    --keeper-host <KEEPER_IP> \
    --clickhouse-host <CLICKHOUSE_IP> \
    --clickhouse-password '<CLICKHOUSE_PASSWORD>' \
    --encryption-key <K_O11Y_ENCRYPTION_KEY> \
    --verbose --yes
```

安装内容: Keeper、ClickHouse、clickhouse-backup、get-s3-creds、DB Agent（systemd）

> Post-Install（Step 4）中同时安装 OTel Agent（收集 CH VM 主机/ClickHouse 指标）。

连接模式: `ssh`（默认）、`bastion`（跳板机）、`local`（VM 本地执行）

### Step 2.（可选）TLS 证书配置

仅当 Agent 需要经由公网区间跨 VPC/网络通信时才需要。

```bash
./k-o11y-tls setup \
    --mode selfsigned \
    --domain <DOMAIN> \
    --secret-name k-o11y-otel-collector-tls \
    --kube-context <HOST_CONTEXT> -y
```

模式: `existing`、`selfsigned`、`private-ca`、`letsencrypt`

### Step 3. Host 集群安装

```bash
helm upgrade --install k-o11y-host \
    --kube-context <HOST_CONTEXT> \
    oci://<YOUR_REGISTRY>/charts/k-o11y-host \
    --version <CHART_VERSION> \
    --namespace k-o11y --create-namespace \
    --set externalClickhouse.host=<NLB_DNS_OR_IP> \
    --set externalClickhouse.user=default \
    --set externalClickhouse.password='<CLICKHOUSE_PASSWORD>' \
    --set o11yCore.image.tag=<CORE_TAG> \
    --set o11yHub.image.tag=<HUB_TAG> \
    --set o11yHub.additionalEnvs.CH_HOST=<CLICKHOUSE_VM_IP> \
    --set o11yHub.additionalEnvs.CH_PASSWORD='<CLICKHOUSE_PASSWORD>' \
    --set o11yHub.additionalEnvs.K_O11Y_ENCRYPTION_KEY=<ENCRYPTION_KEY>
```

> **SSO**: SSO 默认禁用（sso.enabled=false）。如需启用，请在 values.yaml 中配置。
> 仅在内部环境中多租户需要访问时才添加 `--set 'o11yHub.sso.allowedTenants=*'`。
> 客户环境保持默认值（空），首次登录时自动应用 tenant auto-lock。

启用 TLS 时追加:

```bash
    --set otelCollector.tls.enabled=true \
    --set otelCollector.tls.existingSecretName=k-o11y-otel-collector-tls \
    --set otelCollector.tls.path=/etc/otel/tls
```

### Step 4. Post-Install（应用 DDL + OTel Agent）

```bash
./k-o11y-db post-install \
    --mode ssh \
    --ssh-user <SSH_USER> \
    --ssh-key <SSH_KEY_PATH> \
    --ssh-password '<SSH_PASSWORD>' \
    --clickhouse-host <CLICKHOUSE_IP> \
    --clickhouse-password '<CLICKHOUSE_PASSWORD>' \
    --otel-endpoint <HOST_GATEWAY_IP>:4317 \
    --environment <ENV> \
    --verbose
```

应用内容:
- DDL: 50+ 张表、MV、Dictionary、元数据表（data_lifecycle_config、s3_config、sso_config、agent_status）
- OTel Agent: 安装 otelcol-contrib v0.109.0（收集主机指标 + ClickHouse Prometheus 数据 → 发送至 Host OTel GW）

#### 冷备份状态值（`last_backup_status`）

| 状态 | 含义 |
|------|------|
| `never` | 从未执行过（初始值） |
| `skipped_no_partitions` | 已执行但无归档目标分区 |
| `success` | 全部分区备份成功 |
| `partial_failure` | 部分分区成功，部分失败 |
| `failed` | 全部分区备份失败 |

- 调度器按 `backup_frequency_hours` 间隔执行（默认 24h）
- 处理 cutoff 之前（`today - hot_days - warm_days`）的所有分区，每次最多处理 7 个
- 失败分区在下一周期自动重试

省略 `--otel-endpoint` 时跳过 OTel Agent 安装。

启用 TLS 时追加:

```bash
    --otel-tls                  # 启用 TLS
    --otel-tls-skip-verify      # 用于 self-signed 证书
```

### Step 5. 安装 cert-manager（Agent 集群）

```bash
helm install cert-manager jetstack/cert-manager \
    --namespace cert-manager --create-namespace \
    --version v1.17.1 \
    --set crds.enabled=true \
    --kube-context <AGENT_CONTEXT> \
    --wait --timeout 5m
```

### Step 6. Agent 集群安装

```bash
helm upgrade --install k-o11y-agent \
    --kube-context <AGENT_CONTEXT> \
    oci://<YOUR_REGISTRY>/charts/k-o11y-agent \
    --version <CHART_VERSION> \
    --namespace k-o11y --create-namespace \
    --set global.clusterName=<CLUSTER_NAME> \
    --set global.deploymentEnvironment=<ENV> \
    --set global.otelInsecure=true \
    --set global.hostEndpointHttp=http://<HOST_GATEWAY_IP>:4318 \
    --set k-o11y-otel-agent.otelCollectorEndpoint=<HOST_GATEWAY_IP>:4317 \
    --set k-o11y-apm-agent.config.data.attributes.kubernetes.cluster_name=<CLUSTER_NAME> \
    --set instrumentation.exporter.endpoint=http://<HOST_GATEWAY_IP>:4317 \
    --wait --timeout 25m
```

启用 TLS 时: `global.otelInsecure=false`、`http://` → `https://`、`k-o11y-otel-agent.insecureSkipVerify=true`（self-signed）

---

## 卸载

按安装的逆序执行。

```bash
helm uninstall k-o11y-agent --kube-context <AGENT_CONTEXT> -n k-o11y
helm uninstall k-o11y-host --kube-context <HOST_CONTEXT> -n k-o11y

./k-o11y-db uninstall \
    --mode ssh \
    --ssh-user <SSH_USER> \
    --ssh-key <SSH_KEY_PATH> \
    --ssh-password '<SSH_PASSWORD>' \
    --keeper-host <KEEPER_IP> \
    --clickhouse-host <CLICKHOUSE_IP> \
    --verbose --yes
```

---

## 升级

升级 CH VM 组件（DB Agent、OTel Agent、DDL）。Host/Agent 请使用 `helm upgrade` 处理。

```bash
./k-o11y-db upgrade \
    --mode ssh \
    --ssh-user <SSH_USER> \
    --ssh-key <SSH_KEY_PATH> \
    --ssh-password '<SSH_PASSWORD>' \
    --clickhouse-host <CLICKHOUSE_IP> \
    --clickhouse-password '<CLICKHOUSE_PASSWORD>' \
    --otel-endpoint <HOST_GATEWAY_IP>:4317 \
    --verbose --yes
```

| 对象 | 触发条件 | 失败时 |
|------|---------|--------|
| DB Agent | 始终 | 自动回滚（恢复 .bak） |
| OTel Agent | 指定 `--otel-endpoint` 时 | 自动回滚（恢复 config） |
| DDL 迁移 | 指定 `--clickhouse-password` 时 | 幂等（IF NOT EXISTS） |

查看版本: `./k-o11y-db --version`

---

## Helm Chart

| Chart | 版本 | 说明 |
|-------|------|------|
| k-o11y-host | 26.2.1 | Host umbrella (SigNoz + OTel Gateway) |
| k-o11y-agent | 26.2.1 | Agent umbrella |
| k-o11y-otel-agent | 26.2.1 | OTel Collector |
| k-o11y-otel-operator | 26.2.1 | OTel Operator |
| k-o11y-apm-agent | 26.2.1 | eBPF APM Agent |
| k-o11y-ksm | 26.2.1 | Kube State Metrics |

OCI Registry: `oci://<YOUR_REGISTRY>/charts`

```bash
helm package charts/<CHART_NAME>/
helm push <CHART_NAME>-<VERSION>.tgz oci://<YOUR_REGISTRY>/charts
```

## 维护者

由 [Wondermove](https://wondermove.net) 开发并维护。

## 许可证

MIT License - 参见 [LICENSE](LICENSE)
