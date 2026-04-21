# K-O11y Install

[English](README.md) | [한국어](README.ko.md) | [日本語](README.ja.md) | [中文](README.zh-CN.md)

K-O11y インストールパッケージです。ClickHouse/Keeper VM のインストール、DB エージェントのデプロイ、TLS 設定、Kubernetes クラスターへのデプロイを自動化します。
[Wondermove](https://wondermove.net) が開発した [K-O11y](https://github.com/Wondermove-Inc/k-o11y-server) のインストールツールです。

---

## プロジェクト概要

K-O11y は SigNoz ベースのセルフホスト型 K8s 可観測性ソリューションです。メトリクス、ログ、トレースを収集して ClickHouse に保存し、S3 Warm/Cold ストレージティアリングをサポートしています。

### アーキテクチャ

```
Agent Clusters (複数)                    Host Cluster (中央)
┌──────────────────────┐                 ┌──────────────────────┐
│  OTel Collector      │                 │  o11y-hub (UI+API)   │
│  APM Agent (eBPF)    │  ── OTLP ──>   │  o11y-core           │
│  KSM                 │    (4317)       │  OTel Gateway        │
│  OTel Operator       │                 └──────────┬───────────┘
└──────────────────────┘                            │
                                         ┌──────────▼───────────┐
                                         │  ClickHouse VM       │
                                         │   + DB エージェント  │
                                         │  Keeper VM           │
                                         └──────────────────────┘
```

DB エージェントは ClickHouse VM 上に常駐し、DB ポーリングによって S3 アクティベート、コールドバックアップ、DROP PARTITION を自律的に実行します。SSH への依存なく動作します。

---

## プロジェクト構成

```
k-o11y-install/
├── cmd/
│   ├── k-o11y-db/      Go バイナリ: DB インストール + エージェント + アップグレード
│   │   ├── cmd/                            cobra サブコマンド (install, post-install, uninstall, upgrade, agent)
│   │   ├── internal/
│   │   │   ├── agent/                      DB エージェント (daemon, poller, s3_activator, backup, health)
│   │   │   ├── installer/                  インストールロジック (keeper, clickhouse, agent, uninstall)
│   │   │   ├── ssh/                        SSH 抽象化 (ssh, bastion, local)
│   │   │   └── embed/                      埋め込みリソース (DDL, スクリプト, テンプレート)
│   │   └── Makefile                        クロスコンパイル (linux/darwin, amd64/arm64)
│   │
│   └── k-o11y-tls/     Go バイナリ: TLS 証明書設定
│       ├── cmd/                            cobra サブコマンド (setup)
│       ├── internal/
│       │   ├── tls/                        モード別設定 (existing, selfsigned, private-ca, letsencrypt)
│       │   ├── kube/                       kubectl/helm os/exec ラッパー
│       │   └── embed/                      YAML テンプレート (cert-manager CRD)
│       └── Makefile
│
├── charts/                               Helm チャート
│   ├── k-o11y-host/      Host umbrella chart (SigNoz + OTel Gateway)
│   ├── k-o11y-agent/     Agent umbrella chart
│   ├── k-o11y-otel-agent/                  OTel Collector (サブチャート)
│   ├── k-o11y-otel-operator/               OTel Operator (サブチャート)
│   ├── k-o11y-apm-agent/                   eBPF APM Agent (サブチャート)
│   └── k-o11y-ksm/                         Kube State Metrics (サブチャート)
│
└── upstream-versions.yaml                Upstream イメージバージョン追跡
```

### インストールツール

| バイナリ | 役割 | ビルド |
|---------|------|--------|
| `k-o11y-db` | DB インストール + エージェント + DDL 適用 + 削除 | `cmd/k-o11y-db/` |
| `k-o11y-tls` | TLS 証明書設定 | `cmd/k-o11y-tls/` |

```bash
cd cmd/k-o11y-db && make build-all
cd cmd/k-o11y-tls && make build-all
```

---

## 開始前の注意

> **事前ビルド済みの Docker イメージおよび Helm チャートは提供されていません。**
> [メインリポジトリ](https://github.com/Wondermove-Inc/k-o11y-server)からイメージをビルドし、自社レジストリに push する必要があります。
> デプロイ前に `values.yaml` のイメージレジストリを自社レジストリに変更してください。

```bash
# 例: Helm チャートのパッケージングと自社レジストリへの push
helm package charts/k-o11y-host
helm push k-o11y-host-*.tgz oci://<your-registry>/charts
```

## インストールガイド

### 事前準備

| 項目 | 要件 |
|------|------|
| VM (ClickHouse/Keeper) | Ubuntu 22.04 LTS、sudo SSH アカウント、8+ vCPU、32GB+ RAM |
| Host K8s クラスター | Kubernetes 1.28+、Helm 3.12+、kubectl |
| Agent K8s クラスター | Kubernetes 1.28+、Linux カーネル 5.8+（eBPF） |
| OCI Registry | `<YOUR_REGISTRY>` へのアクセス権 |

K_O11Y_ENCRYPTION_KEY を事前に生成してください（Step 1、3 で同じキーを使用）:

```bash
openssl rand -hex 32
```

### Step 1. DB インストール + エージェントデプロイ

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

インストール内容: Keeper、ClickHouse、clickhouse-backup、get-s3-creds、DB エージェント（systemd）

> Post-Install（Step 4）では OTel Agent も合わせてインストールされます（CH VM ホスト/ClickHouse メトリクス収集）。

接続モード: `ssh`（デフォルト）、`bastion`（踏み台サーバー）、`local`（VM 直接実行）

### Step 2.（任意）TLS 証明書設定

Agent が異なる VPC/ネットワークからパブリック区間を経由する場合にのみ必要です。

```bash
./k-o11y-tls setup \
    --mode selfsigned \
    --domain <DOMAIN> \
    --secret-name k-o11y-otel-collector-tls \
    --kube-context <HOST_CONTEXT> -y
```

モード: `existing`、`selfsigned`、`private-ca`、`letsencrypt`

### Step 3. Host クラスターインストール

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

> **SSO**: SSO はデフォルトで無効（sso.enabled=false）です。必要に応じて values.yaml で有効化してください。
> 内部環境で複数テナントがアクセスする場合のみ `--set 'o11yHub.sso.allowedTenants=*'` を追加してください。
> 顧客環境ではデフォルト値（空）のままにし、初回ログイン時に tenant auto-lock が適用されます。

TLS 使用時の追加設定:

```bash
    --set otelCollector.tls.enabled=true \
    --set otelCollector.tls.existingSecretName=k-o11y-otel-collector-tls \
    --set otelCollector.tls.path=/etc/otel/tls
```

### Step 4. Post-Install（DDL 適用 + OTel Agent）

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

適用内容:
- DDL: 50+ テーブル、MV、Dictionary、メタデータテーブル（data_lifecycle_config、s3_config、sso_config、agent_status）
- OTel Agent: otelcol-contrib v0.109.0 インストール（ホストメトリクス + ClickHouse Prometheus 収集 → Host OTel GW 送信）

#### コールドバックアップ状態値（`last_backup_status`）

| 状態 | 意味 |
|------|------|
| `never` | 一度も実行されていない（初期値） |
| `skipped_no_partitions` | 実行されたがアーカイブ対象パーティションなし |
| `success` | 全パーティションのバックアップ成功 |
| `partial_failure` | 一部パーティション成功、一部失敗 |
| `failed` | 全パーティションのバックアップ失敗 |

- スケジューラーは `backup_frequency_hours` 間隔で実行（デフォルト 24h）
- cutoff 以前（`today - hot_days - warm_days`）の全パーティションを対象に、最大 7 件/run 処理
- 失敗パーティションは次のサイクルで自動リトライ

`--otel-endpoint` を省略した場合、OTel Agent のインストールをスキップします。

TLS 使用時の追加設定:

```bash
    --otel-tls                  # TLS 有効化
    --otel-tls-skip-verify      # self-signed 証明書用
```

### Step 5. cert-manager インストール（Agent クラスター）

```bash
helm install cert-manager jetstack/cert-manager \
    --namespace cert-manager --create-namespace \
    --version v1.17.1 \
    --set crds.enabled=true \
    --kube-context <AGENT_CONTEXT> \
    --wait --timeout 5m
```

### Step 6. Agent クラスターインストール

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

TLS 使用時: `global.otelInsecure=false`、`http://` → `https://`、`k-o11y-otel-agent.insecureSkipVerify=true`（self-signed）

---

## アンインストール

インストールの逆順で実行してください。

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

## アップグレード

CH VM コンポーネント（DB エージェント、OTel Agent、DDL）をアップグレードします。Host/Agent は `helm upgrade` で対応してください。

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

| 対象 | トリガー | 失敗時 |
|------|---------|--------|
| DB エージェント | 常時 | 自動ロールバック（.bak 復元） |
| OTel Agent | `--otel-endpoint` 指定時 | 自動ロールバック（config 復元） |
| DDL マイグレーション | `--clickhouse-password` 指定時 | 冪等（IF NOT EXISTS） |

バージョン確認: `./k-o11y-db --version`

---

## Helm チャート

| チャート | バージョン | 説明 |
|---------|----------|------|
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

## 管理

[Wondermove](https://wondermove.net) が開発・維持管理しています。

## ライセンス

MIT License - [LICENSE](LICENSE) 参照
