// Package installer implements ClickHouse and Keeper installation logic.
package installer

import (
	"fmt"
	"strings"
	"text/template"
	"bytes"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/ssh"
)

// KeeperConfig holds Keeper installation parameters.
type KeeperConfig struct {
	Host       string
	ClusterHost string // internal communication IP (defaults to Host)
	Hostname   string // node name (default: keeper-1)
	Version    string // package version (default: 24.1.8.22)
	ServerID   int    // raft server ID (default: 1)
	ClientPort int    // client port (default: 9181)
	RaftPort   int    // raft port (default: 9234)
	DataPath   string // data directory (default: /var/lib/clickhouse/coordination)
}

// DefaultKeeperConfig returns a KeeperConfig with defaults.
func DefaultKeeperConfig() *KeeperConfig {
	return &KeeperConfig{
		Hostname:   "keeper-1",
		Version:    "24.1.8.22",
		ServerID:   1,
		ClientPort: 9181,
		RaftPort:   9234,
		DataPath:   "/var/lib/clickhouse/coordination",
	}
}

const keeperConfigTemplate = `<clickhouse>
    <listen_host>0.0.0.0</listen_host>
    <max_connections>4096</max_connections>

    <logger>
        <level>information</level>
        <log>/var/log/clickhouse-keeper/clickhouse-keeper.log</log>
        <errorlog>/var/log/clickhouse-keeper/clickhouse-keeper.err.log</errorlog>
        <size>1000M</size>
        <count>10</count>
    </logger>

    <keeper_server>
        <tcp_port>{{.ClientPort}}</tcp_port>
        <server_id>{{.ServerID}}</server_id>

        <log_storage_path>{{.DataPath}}/log</log_storage_path>
        <snapshot_storage_path>{{.DataPath}}/snapshots</snapshot_storage_path>

        <coordination_settings>
            <operation_timeout_ms>10000</operation_timeout_ms>
            <min_session_timeout_ms>10000</min_session_timeout_ms>
            <session_timeout_ms>100000</session_timeout_ms>
            <raft_logs_level>information</raft_logs_level>
            <compress_logs>false</compress_logs>
            <snapshot_distance>100000</snapshot_distance>
            <snapshots_to_keep>3</snapshots_to_keep>
        </coordination_settings>

        <hostname_checks_enabled>true</hostname_checks_enabled>
        <enable_reconfiguration>true</enable_reconfiguration>

        <raft_configuration>
            <server>
                <id>{{.ServerID}}</id>
                <hostname>{{.ClusterHost}}</hostname>
                <port>{{.RaftPort}}</port>
            </server>
        </raft_configuration>
    </keeper_server>

    <prometheus>
        <endpoint>/metrics</endpoint>
        <port>9363</port>
        <metrics>true</metrics>
        <events>true</events>
        <asynchronous_metrics>true</asynchronous_metrics>
    </prometheus>
</clickhouse>`

// InstallKeeper installs ClickHouse Keeper on the target host.
func InstallKeeper(exec ssh.Executor, cfg *KeeperConfig, verbose bool) error {
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
			if _, err := exec.ExecSudo("apt-key adv --keyserver hkp://keyserver.ubuntu.com:80 --recv 8919F6BD2B48D754"); err != nil {
				return err
			}
			if _, err := exec.ExecSudo("echo 'deb https://packages.clickhouse.com/deb stable main' > /etc/apt/sources.list.d/clickhouse.list"); err != nil {
				return err
			}
			_, err := exec.ExecSudo("apt-get update -qq")
			return err
		}},
		{"Keeper 패키지 설치", func() error {
			exec.ExecSudo("apt-mark unhold clickhouse-keeper 2>/dev/null || true")
			installCmd := "DEBIAN_FRONTEND=noninteractive apt-get install --reinstall -y -qq clickhouse-keeper"
			if cfg.Version != "" {
				installCmd += "=" + cfg.Version
			}
			if _, err := exec.ExecSudo(installCmd); err != nil {
				return fmt.Errorf("패키지 설치 실패: %w", err)
			}
			_, err := exec.ExecSudo("apt-mark hold clickhouse-keeper")
			return err
		}},
		{"데이터 디렉토리 생성", func() error {
			cmds := []string{
				fmt.Sprintf("mkdir -p %s/log %s/snapshots", cfg.DataPath, cfg.DataPath),
				"mkdir -p /var/log/clickhouse-keeper",
				fmt.Sprintf("chown -R clickhouse:clickhouse %s", cfg.DataPath),
				"chown -R clickhouse:clickhouse /var/log/clickhouse-keeper",
			}
			for _, cmd := range cmds {
				if _, err := exec.ExecSudo(cmd); err != nil {
					return err
				}
			}
			return nil
		}},
		{"설정 파일 생성", func() error {
			configContent, err := renderKeeperConfig(cfg)
			if err != nil {
				return fmt.Errorf("설정 파일 생성 실패: %w", err)
			}
			// 임시 경로에 업로드 후 sudo로 이동
			if err := exec.UploadBytes(configContent, "/tmp/keeper_config.xml", 0644); err != nil {
				return fmt.Errorf("설정 파일 업로드 실패: %w", err)
			}
			if _, err := exec.ExecSudo("mkdir -p /etc/clickhouse-keeper && mv /tmp/keeper_config.xml /etc/clickhouse-keeper/keeper_config.xml && chown clickhouse:clickhouse /etc/clickhouse-keeper/keeper_config.xml"); err != nil {
				return fmt.Errorf("설정 파일 배포 실패: %w", err)
			}
			return nil
		}},
		{"서비스 시작", func() error {
			cmds := []string{
				"systemctl daemon-reload",
				"systemctl enable clickhouse-keeper",
				"systemctl start clickhouse-keeper",
			}
			for _, cmd := range cmds {
				if _, err := exec.ExecSudo(cmd); err != nil {
					return err
				}
			}
			return nil
		}},
		{"서비스 상태 확인", func() error {
			result, err := exec.Exec("systemctl is-active clickhouse-keeper")
			if err != nil {
				return fmt.Errorf("상태 확인 실패: %w", err)
			}
			if strings.TrimSpace(result.Stdout) != "active" {
				// 재시도 (3초 대기)
				exec.Exec("sleep 5")
				result, err = exec.Exec("systemctl is-active clickhouse-keeper")
				if err != nil || strings.TrimSpace(result.Stdout) != "active" {
					return fmt.Errorf("Keeper 서비스 시작 실패 (status: %s)", result.Stdout)
				}
			}
			return nil
		}},
	}

	for i, step := range steps {
		if verbose {
			fmt.Printf("  [%d/%d] %s...\n", i+1, len(steps), step.name)
		}
		if err := step.fn(); err != nil {
			return fmt.Errorf("Step %d (%s) 실패: %w", i+1, step.name, err)
		}
		if verbose {
			fmt.Printf("  ✓ %s 완료\n", step.name)
		}
	}

	fmt.Printf("  ✓ Keeper 설치 완료 (%s)\n", cfg.Host)
	return nil
}

func renderKeeperConfig(cfg *KeeperConfig) ([]byte, error) {
	tmpl, err := template.New("keeper").Parse(keeperConfigTemplate)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
