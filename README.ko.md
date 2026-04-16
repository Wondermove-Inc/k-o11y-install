# K-O11y Install

[English](README.md)


K-O11y 설치 패키지. ClickHouse/Keeper VM 설치, DB 에이전트 배포, TLS 설정, Kubernetes 클러스터 배포까지 자동화합니다.
[Wondermove](https://wondermove.net)가 개발한 [K-O11y](https://github.com/Wondermove-Inc/k-o11y-server)의 설치 도구입니다.

---

## 프로젝트 소개

K-O11y는 SigNoz 기반의 설치형 K8s 관측성 솔루션입니다. 메트릭, 로그, 트레이스를 수집하여 ClickHouse에 저장하고, S3 Warm/Cold 스토리지 티어링을 지원합니다.

### 아키텍처

```
Agent Clusters (다중)                     Host Cluster (중앙)
┌──────────────────────┐                 ┌──────────────────────┐
│  OTel Collector      │                 │  o11y-hub (UI+API)   │
│  APM Agent (eBPF)    │  ── OTLP ──>   │  o11y-core           │
│  KSM                 │    (4317)       │  OTel Gateway        │
│  OTel Operator       │                 └──────────┬───────────┘
└──────────────────────┘                            │
                                         ┌──────────▼───────────┐
                                         │  ClickHouse VM       │
                                         │   + DB 에이전트      │
                                         │  Keeper VM           │
                                         └──────────────────────┘
```

DB 에이전트가 ClickHouse VM에 상주하며, DB 폴링으로 S3 Activate, Cold Backup, DROP PARTITION을 자율 수행합니다. SSH 의존성 없이 동작합니다.

---

## 프로젝트 구성

```
k-o11y-install/
├── cmd/
│   ├── k-o11y-db/      Go 바이너리: DB 설치 + 에이전트 + 업그레이드
│   │   ├── cmd/                            cobra 서브커맨드 (install, post-install, uninstall, upgrade, agent)
│   │   ├── internal/
│   │   │   ├── agent/                      DB 에이전트 (daemon, poller, s3_activator, backup, health)
│   │   │   ├── installer/                  설치 로직 (keeper, clickhouse, agent, uninstall)
│   │   │   ├── ssh/                        SSH 추상화 (ssh, bastion, local)
│   │   │   └── embed/                      내장 리소스 (DDL, 스크립트, 템플릿)
│   │   └── Makefile                        크로스 컴파일 (linux/darwin, amd64/arm64)
│   │
│   └── k-o11y-tls/     Go 바이너리: TLS 인증서 설정
│       ├── cmd/                            cobra 서브커맨드 (setup)
│       ├── internal/
│       │   ├── tls/                        모드별 설정 (existing, selfsigned, private-ca, letsencrypt)
│       │   ├── kube/                       kubectl/helm os/exec 래퍼
│       │   └── embed/                      YAML 템플릿 (cert-manager CRD)
│       └── Makefile
│
├── charts/                               Helm 차트
│   ├── k-o11y-host/      Host umbrella chart (SigNoz + OTel Gateway)
│   ├── k-o11y-agent/     Agent umbrella chart
│   ├── k-o11y-otel-agent/                  OTel Collector (sub-chart)
│   ├── k-o11y-otel-operator/               OTel Operator (sub-chart)
│   ├── k-o11y-apm-agent/                   eBPF APM Agent (sub-chart)
│   └── k-o11y-ksm/                         Kube State Metrics (sub-chart)
│
└── upstream-versions.yaml                Upstream 이미지 버전 추적
```

### 설치 도구

| 바이너리 | 역할 | 빌드 |
|---------|------|------|
| `k-o11y-db` | DB 설치 + 에이전트 + DDL 적용 + 삭제 | `cmd/k-o11y-db/` |
| `k-o11y-tls` | TLS 인증서 설정 | `cmd/k-o11y-tls/` |

```bash
cd cmd/k-o11y-db && make build-all
cd cmd/k-o11y-tls && make build-all
```

---

## 시작하기 전에

> **사전 빌드된 Docker 이미지와 Helm 차트는 제공되지 않습니다.**
> [메인 레포지토리](https://github.com/Wondermove-Inc/k-o11y-server)에서 이미지를 빌드하고 자체 레지스트리에 push해야 합니다.
> 배포 전 `values.yaml`의 이미지 레지스트리를 자체 레지스트리로 변경하세요.

```bash
# 예시: Helm 차트 패키징 및 자체 레지스트리에 push
helm package charts/k-o11y-host
helm push k-o11y-host-*.tgz oci://<your-registry>/charts
```

## 설치 가이드

### 사전 준비

| 항목 | 요구사항 |
|------|---------|
| VM (ClickHouse/Keeper) | Ubuntu 22.04 LTS, sudo SSH 계정, 8+ vCPU, 32GB+ RAM |
| Host K8s 클러스터 | Kubernetes 1.28+, Helm 3.12+, kubectl |
| Agent K8s 클러스터 | Kubernetes 1.28+, Linux 커널 5.8+ (eBPF) |
| OCI Registry | `<YOUR_REGISTRY>` 접근 가능 |

K_O11Y_ENCRYPTION_KEY를 미리 생성합니다 (Step 1, 3에서 동일한 키 사용):

```bash
openssl rand -hex 32
```

### Step 1. DB 설치 + 에이전트 배포

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

설치 항목: Keeper, ClickHouse, clickhouse-backup, get-s3-creds, DB 에이전트 (systemd)

> Post-Install(Step 4)에서 OTel Agent도 함께 설치됩니다 (CH VM 호스트/ClickHouse 메트릭 수집).

접속 모드: `ssh` (기본), `bastion` (점프호스트), `local` (VM 직접 실행)

### Step 2. (선택) TLS 인증서 설정

Agent가 다른 VPC/네트워크에서 퍼블릭 구간을 경유하는 경우에만 필요합니다.

```bash
./k-o11y-tls setup \
    --mode selfsigned \
    --domain <DOMAIN> \
    --secret-name k-o11y-otel-collector-tls \
    --kube-context <HOST_CONTEXT> -y
```

모드: `existing`, `selfsigned`, `private-ca`, `letsencrypt`

### Step 3. Host 클러스터 설치

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

> **SSO**: SSO는 기본 비활성화(sso.enabled=false)입니다. 필요 시 values.yaml에서 활성화하세요.
> 내부 환경에서 여러 테넌트가 접근해야 하는 경우에만 `--set 'o11yHub.sso.allowedTenants=*'`를 추가하세요.
> 고객사 환경은 기본값(비움)으로 첫 로그인 시 tenant auto-lock이 적용됩니다.

TLS 사용 시 추가:

```bash
    --set otelCollector.tls.enabled=true \
    --set otelCollector.tls.existingSecretName=k-o11y-otel-collector-tls \
    --set otelCollector.tls.path=/etc/otel/tls
```

### Step 4. Post-Install (DDL 적용 + OTel Agent)

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

적용:
- DDL: 50+ 테이블, MV, Dictionary, 메타데이터 테이블 (data_lifecycle_config, s3_config, sso_config, agent_status)
- OTel Agent: otelcol-contrib v0.109.0 설치 (호스트 메트릭 + ClickHouse Prometheus 수집 → Host OTel GW 전송)

#### Cold Backup 상태값 (`last_backup_status`)

| 상태 | 의미 |
|------|------|
| `never` | 한 번도 실행된 적 없음 (초기값) |
| `skipped_no_partitions` | 실행됐지만 아카이브 대상 파티션 없음 |
| `success` | 전체 파티션 백업 성공 |
| `partial_failure` | 일부 파티션 성공, 일부 실패 |
| `failed` | 전체 파티션 백업 실패 |

- 스케줄러는 `backup_frequency_hours` 간격으로 실행 (기본 24h)
- cutoff 이전(`today - hot_days - warm_days`)의 모든 파티션을 대상으로 최대 7개/run 처리
- 실패 파티션은 다음 주기에 자동 재시도

`--otel-endpoint` 생략 시 OTel Agent 설치를 스킵합니다.

TLS 사용 시 추가:

```bash
    --otel-tls                  # TLS 활성화
    --otel-tls-skip-verify      # self-signed 인증서용
```

### Step 5. cert-manager 설치 (Agent 클러스터)

```bash
helm install cert-manager jetstack/cert-manager \
    --namespace cert-manager --create-namespace \
    --version v1.17.1 \
    --set crds.enabled=true \
    --kube-context <AGENT_CONTEXT> \
    --wait --timeout 5m
```

### Step 6. Agent 클러스터 설치

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

TLS 사용 시: `global.otelInsecure=false`, `http://` → `https://`, `k-o11y-otel-agent.insecureSkipVerify=true` (self-signed)

---

## 삭제 (Uninstall)

설치 역순으로 진행합니다.

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

## 업그레이드

CH VM 컴포넌트(DB 에이전트, OTel Agent, DDL)를 업그레이드합니다. Host/Agent는 `helm upgrade`로 처리.

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

| 대상 | 트리거 | 실패 시 |
|------|--------|---------|
| DB 에이전트 | 항상 | 자동 롤백 (.bak 복원) |
| OTel Agent | `--otel-endpoint` 지정 시 | 자동 롤백 (config 복원) |
| DDL 마이그레이션 | `--clickhouse-password` 지정 시 | 멱등 (IF NOT EXISTS) |

버전 확인: `./k-o11y-db --version`

---

## Helm 차트

| 차트 | 버전 | 설명 |
|------|------|------|
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

## 관리

[Wondermove](https://wondermove.net)가 개발 및 관리합니다.

## 라이선스

MIT License - [LICENSE](LICENSE) 참조