package installer

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/ssh"
	embedpkg "github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/embed"
)

// ClickHouseConfig holds ClickHouse installation parameters.
type ClickHouseConfig struct {
	Host          string
	ClusterHost   string // internal communication IP (defaults to Host)
	Hostname      string // replica name (default: clickhouse-1)
	Version       string // package version (default: 24.1.8.22)
	Password      string // default user password
	ClusterName   string // cluster name (default: k_o11y_cluster)
	Shard         int    // shard number (default: 1)
	DataPath      string // data directory (default: /var/lib/clickhouse)
	LogPath       string // log directory (default: /var/log/clickhouse-server)
	SystemTTLDays int    // system table TTL (default: 30)

	// Keeper connection
	KeeperHost string // Keeper cluster host
	KeeperPort int    // Keeper client port (default: 9181)

	// SSH password for sudo
	SSHPassword string
}

// DefaultClickHouseConfig returns a ClickHouseConfig with defaults.
func DefaultClickHouseConfig() *ClickHouseConfig {
	return &ClickHouseConfig{
		Hostname:      "clickhouse-1",
		Version:       "24.1.8.22",
		ClusterName:   "k_o11y_cluster",
		Shard:         1,
		DataPath:      "/var/lib/clickhouse",
		LogPath:       "/var/log/clickhouse-server",
		SystemTTLDays: 30,
		KeeperPort:    9181,
	}
}

// InstallResult holds the results of installation.
type InstallResult struct{}

// InstallClickHouse installs ClickHouse Server on the target host.
func InstallClickHouse(exec ssh.Executor, cfg *ClickHouseConfig, verbose bool) (*InstallResult, error) {
	result := &InstallResult{}
	if cfg.ClusterHost == "" {
		cfg.ClusterHost = cfg.Host
	}

	steps := []struct {
		name string
		fn   func() error
	}{
		{"시스템 패키지 업데이트", func() error {
			_, err := exec.ExecSudo("apt-get update -qq")
			return err
		}},
		{"필수 패키지 설치", func() error {
			_, err := exec.ExecSudo("apt-get install -y -qq apt-transport-https ca-certificates dirmngr curl gnupg")
			return err
		}},
		{"ClickHouse 레포지토리 추가", func() error {
			exec.ExecSudo("apt-key adv --keyserver hkp://keyserver.ubuntu.com:80 --recv 8919F6BD2B48D754")
			exec.ExecSudo("echo 'deb https://packages.clickhouse.com/deb stable main' > /etc/apt/sources.list.d/clickhouse.list")
			_, err := exec.ExecSudo("apt-get update -qq")
			return err
		}},
		{"ClickHouse 패키지 설치", func() error {
			exec.ExecSudo("apt-mark unhold clickhouse-server clickhouse-client clickhouse-common-static 2>/dev/null || true")
			installCmd := "DEBIAN_FRONTEND=noninteractive apt-get install --reinstall -y -qq"
			if cfg.Version != "" {
				installCmd += fmt.Sprintf(" clickhouse-server=%s clickhouse-client=%s clickhouse-common-static=%s",
					cfg.Version, cfg.Version, cfg.Version)
			} else {
				installCmd += " clickhouse-server clickhouse-client"
			}
			if _, err := exec.ExecSudo(installCmd); err != nil {
				return fmt.Errorf("패키지 설치 실패: %w", err)
			}
			_, err := exec.ExecSudo("apt-mark hold clickhouse-server clickhouse-client clickhouse-common-static")
			return err
		}},
		{"자동 생성 비밀번호 제거", func() error {
			_, err := exec.ExecSudo("rm -f /etc/clickhouse-server/users.d/default-password.xml 2>/dev/null || true")
			return err
		}},
		{"데이터 디렉토리 설정", func() error {
			cmds := []string{
				fmt.Sprintf("mkdir -p %s %s", cfg.DataPath, cfg.LogPath),
				fmt.Sprintf("chown -R clickhouse:clickhouse %s %s", cfg.DataPath, cfg.LogPath),
				"mkdir -p /etc/clickhouse-server/config.d /etc/clickhouse-server/users.d",
			}
			for _, cmd := range cmds {
				if _, err := exec.ExecSudo(cmd); err != nil {
					return err
				}
			}
			return nil
		}},
		{"설정 파일 배포", func() error {
			configs := map[string]string{
				"macros.xml":            renderTemplate(macrosTemplate, cfg),
				"zookeeper.xml":         renderTemplate(zookeeperTemplate, cfg),
				"cluster.xml":           renderTemplate(clusterTemplate, cfg),
				"listen.xml":            listenConfig,
				"prometheus.xml":        renderTemplate(prometheusTemplate, cfg),
				"system_tables_ttl.xml": renderTemplate(systemTTLTemplate, cfg),
			}
			for name, content := range configs {
				path := "/etc/clickhouse-server/config.d/" + name
				tmpPath := "/tmp/" + name
				if err := exec.UploadBytes([]byte(content), tmpPath, 0644); err != nil {
					return fmt.Errorf("%s 업로드 실패: %w", name, err)
				}
				if _, err := exec.ExecSudo(fmt.Sprintf("mv %s %s", tmpPath, path)); err != nil {
					return fmt.Errorf("%s 배포 실패: %w", name, err)
				}
			}

			// users.xml (비밀번호 설정)
			if cfg.Password != "" {
				usersContent := renderTemplate(usersTemplate, cfg)
				tmpPath := "/tmp/default-password.xml"
				if err := exec.UploadBytes([]byte(usersContent), tmpPath, 0644); err != nil {
					return err
				}
				if _, err := exec.ExecSudo("mv /tmp/default-password.xml /etc/clickhouse-server/users.d/default-password.xml"); err != nil {
					return err
				}
			}

			// async_insert.xml
			tmpPath := "/tmp/async-insert.xml"
			if err := exec.UploadBytes([]byte(asyncInsertConfig), tmpPath, 0644); err != nil {
				return err
			}
			exec.ExecSudo("mv /tmp/async-insert.xml /etc/clickhouse-server/users.d/async-insert.xml")

			return nil
		}},
		{"S3 환경 파일 배포", func() error {
			// s3.env (빈 파일 — UI 활성화 시 채워짐)
			exec.ExecSudo("touch /etc/clickhouse-server/s3.env && chmod 600 /etc/clickhouse-server/s3.env && chown clickhouse:clickhouse /etc/clickhouse-server/s3.env")
			// systemd override
			exec.ExecSudo("mkdir -p /etc/systemd/system/clickhouse-server.service.d/")
			overrideContent := "[Service]\nEnvironmentFile=/etc/clickhouse-server/s3.env\n"
			if err := exec.UploadBytes([]byte(overrideContent), "/tmp/s3-env.conf", 0644); err != nil {
				return err
			}
			exec.ExecSudo("mv /tmp/s3-env.conf /etc/systemd/system/clickhouse-server.service.d/s3-env.conf")
			return nil
		}},
		// k-o11y-s3 계정 + SSH keypair 생성은 에이전트 도입으로 불필요 ()
		{"get-s3-creds 배포", func() error {
			exec.ExecSudo("mkdir -p /opt/scripts")
			binData, err := embedpkg.ReadBin("get-s3-creds")
			if err != nil {
				return fmt.Errorf("get-s3-creds 로드 실패: %w", err)
			}
			if err := exec.UploadBytes(binData, "/tmp/get-s3-creds", 0755); err != nil {
				return fmt.Errorf("get-s3-creds 업로드 실패: %w", err)
			}
			exec.ExecSudo("mv /tmp/get-s3-creds /opt/scripts/get-s3-creds && chmod 755 /opt/scripts/get-s3-creds")
			return nil
		}},
		{"ch-glacier-cron.sh 배포", func() error {
			cronScript, err := embedpkg.ReadScript("ch-glacier-cron.sh")
			if err != nil {
				return fmt.Errorf("ch-glacier-cron.sh 로드 실패: %w", err)
			}
			if err := exec.UploadBytes(cronScript, "/tmp/ch-glacier-cron.sh", 0755); err != nil {
				return err
			}
			exec.ExecSudo("mv /tmp/ch-glacier-cron.sh /opt/scripts/ch-glacier-cron.sh && chmod 755 /opt/scripts/ch-glacier-cron.sh")
			return nil
		}},
		{"clickhouse-backup 설치", func() error {
			result, _ := exec.Exec("command -v clickhouse-backup &>/dev/null && echo yes || echo no")
			if strings.TrimSpace(result.Stdout) == "yes" {
				return nil
			}
			// 아키텍처 감지
			archResult, _ := exec.Exec("dpkg --print-architecture 2>/dev/null || uname -m")
			arch := strings.TrimSpace(archResult.Stdout)
			switch arch {
			case "arm64", "aarch64":
				arch = "arm64"
			default:
				arch = "amd64"
			}
			exec.Exec(fmt.Sprintf("rm -rf /tmp/cb.tar.gz /tmp/build && wget -q https://github.com/Altinity/clickhouse-backup/releases/download/v2.6.5/clickhouse-backup-linux-%s.tar.gz -O /tmp/cb.tar.gz && tar xzf /tmp/cb.tar.gz -C /tmp", arch))
			exec.ExecSudo(fmt.Sprintf("mv /tmp/build/linux/%s/clickhouse-backup /usr/local/bin/clickhouse-backup && chmod +x /usr/local/bin/clickhouse-backup", arch))
			exec.Exec("rm -rf /tmp/cb.tar.gz /tmp/build")
			return nil
		}},
		{"clickhouse-backup config 배포", func() error {
			exec.ExecSudo("mkdir -p /etc/clickhouse-backup")
			cbConfig := fmt.Sprintf(`general:
  remote_storage: s3
  disable_progress_bar: true

clickhouse:
  host: localhost
  port: 9000
  password: "%s"

s3:
  bucket: ""
  region: "ap-northeast-2"
  path: "backup/"
  storage_class: GLACIER_IR
  object_disk_path: "data/"
  access_key: ""
  secret_key: ""`, cfg.Password)
			if err := exec.UploadBytes([]byte(cbConfig), "/tmp/config.yml", 0600); err != nil {
				return err
			}
			exec.ExecSudo("mv /tmp/config.yml /etc/clickhouse-backup/config.yml && chmod 600 /etc/clickhouse-backup/config.yml")
			return nil
		}},
		{"설정 파일 권한 설정", func() error {
			_, err := exec.ExecSudo("chown -R clickhouse:clickhouse /etc/clickhouse-server/config.d/ /etc/clickhouse-server/users.d/")
			return err
		}},
		{"서비스 시작", func() error {
			cmds := []string{
				"systemctl daemon-reload",
				"systemctl enable clickhouse-server",
				"systemctl start clickhouse-server",
			}
			for _, cmd := range cmds {
				exec.ExecSudo(cmd)
			}
			return nil
		}},
		{"서비스 상태 확인", func() error {
			exec.Exec("sleep 5")
			result, err := exec.Exec("systemctl is-active clickhouse-server")
			if err != nil {
				return fmt.Errorf("상태 확인 실패: %w", err)
			}
			if strings.TrimSpace(result.Stdout) != "active" {
				return fmt.Errorf("ClickHouse 서비스 시작 실패 (status: %s)", result.Stdout)
			}
			return nil
		}},
	}

	for i, step := range steps {
		if verbose {
			fmt.Printf("  [%d/%d] %s...\n", i+1, len(steps), step.name)
		}
		if err := step.fn(); err != nil {
			return nil, fmt.Errorf("Step %d (%s) 실패: %w", i+1, step.name, err)
		}
		if verbose {
			fmt.Printf("  ✓ %s 완료\n", step.name)
		}
	}

	fmt.Printf("  ✓ ClickHouse 설치 완료 (%s)\n", cfg.Host)
	return result, nil
}

// XML config templates
var macrosTemplate = `<clickhouse>
    <macros>
        <cluster>{{.ClusterName}}</cluster>
        <shard>{{.Shard}}</shard>
        <replica>{{.Hostname}}</replica>
    </macros>
</clickhouse>`

var zookeeperTemplate = `<clickhouse>
    <zookeeper>
        <node>
            <host>{{.KeeperHost}}</host>
            <port>{{.KeeperPort}}</port>
        </node>
        <session_timeout_ms>100000</session_timeout_ms>
        <operation_timeout_ms>10000</operation_timeout_ms>
    </zookeeper>
</clickhouse>`

var clusterTemplate = `<clickhouse>
    <remote_servers>
        <{{.ClusterName}}>
            <shard>
                <replica>
                    <host>{{.ClusterHost}}</host>
                    <port>9000</port>
                </replica>
            </shard>
        </{{.ClusterName}}>
    </remote_servers>
</clickhouse>`

var listenConfig = `<clickhouse>
    <listen_host>0.0.0.0</listen_host>
</clickhouse>`

var prometheusTemplate = `<?xml version="1.0"?>
<clickhouse>
    <prometheus>
        <endpoint>/metrics</endpoint>
        <port>9363</port>
        <metrics>true</metrics>
        <events>true</events>
        <asynchronous_metrics>true</asynchronous_metrics>
    </prometheus>
</clickhouse>`

var usersTemplate = `<clickhouse>
    <users>
        <default replace="true">
            <password><![CDATA[{{.Password}}]]></password>
            <networks>
                <ip>::/0</ip>
            </networks>
            <profile>default</profile>
            <quota>default</quota>
            <access_management>1</access_management>
        </default>
    </users>
</clickhouse>`

var asyncInsertConfig = `<clickhouse>
    <profiles>
        <default>
            <async_insert>1</async_insert>
            <wait_for_async_insert>0</wait_for_async_insert>
            <async_insert_max_data_size>10485760</async_insert_max_data_size>
            <async_insert_busy_timeout_ms>200</async_insert_busy_timeout_ms>
        </default>
    </profiles>
</clickhouse>`

var systemTTLTemplate = `<?xml version="1.0"?>
<clickhouse>
    <query_log>
        <ttl>event_date + INTERVAL {{.SystemTTLDays}} DAY DELETE</ttl>
    </query_log>
    <trace_log>
        <ttl>event_date + INTERVAL {{.SystemTTLDays}} DAY DELETE</ttl>
    </trace_log>
    <part_log>
        <ttl>event_date + INTERVAL {{.SystemTTLDays}} DAY DELETE</ttl>
    </part_log>
    <query_views_log>
        <ttl>event_date + INTERVAL {{.SystemTTLDays}} DAY DELETE</ttl>
    </query_views_log>
    <metric_log>
        <ttl>event_date + INTERVAL {{.SystemTTLDays}} DAY DELETE</ttl>
    </metric_log>
    <asynchronous_metric_log>
        <ttl>event_date + INTERVAL {{.SystemTTLDays}} DAY DELETE</ttl>
    </asynchronous_metric_log>
</clickhouse>`

func renderTemplate(tmplStr string, data interface{}) string {
	tmpl, err := template.New("config").Parse(tmplStr)
	if err != nil {
		return ""
	}
	var buf bytes.Buffer
	tmpl.Execute(&buf, data)
	return buf.String()
}
