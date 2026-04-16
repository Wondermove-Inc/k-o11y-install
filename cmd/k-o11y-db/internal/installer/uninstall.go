package installer

import (
	"fmt"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/ssh"
)

// UninstallConfig holds uninstall parameters.
type UninstallConfig struct {
	KeepData bool // preserve data directories
}

// UninstallClickHouse removes ClickHouse Server from the target host.
func UninstallClickHouse(exec ssh.Executor, cfg *UninstallConfig, verbose bool) error {
	steps := []struct {
		name string
		fn   func() error
	}{
		{"서비스 중지", func() error {
			exec.ExecSudo("systemctl stop clickhouse-server 2>/dev/null || true")
			exec.ExecSudo("systemctl disable clickhouse-server 2>/dev/null || true")
			return nil
		}},
		{"프로세스 강제 종료", func() error {
			exec.ExecSudo("killall -9 clickhouse-server clickhouse-watchdog 2>/dev/null || true")
			exec.Exec("sleep 2")
			return nil
		}},
		{"패키지 제거", func() error {
			exec.ExecSudo("apt-mark unhold clickhouse-server clickhouse-client clickhouse-common-static 2>/dev/null || true")
			exec.ExecSudo("DEBIAN_FRONTEND=noninteractive apt-get purge -y clickhouse-server clickhouse-client clickhouse-common-static 2>/dev/null || true")
			exec.ExecSudo("apt-get autoremove -y 2>/dev/null || true")
			return nil
		}},
		{"설정 파일 삭제", func() error {
			exec.ExecSudo("rm -rf /etc/clickhouse-server /etc/clickhouse-client")
			return nil
		}},
		{"데이터 디렉토리 삭제", func() error {
			if cfg.KeepData {
				return nil
			}
			exec.ExecSudo("rm -rf /var/lib/clickhouse")
			exec.ExecSudo("rm -rf /var/log/clickhouse-server")
			return nil
		}},
		{"APT 레포지토리 삭제", func() error {
			exec.ExecSudo("rm -f /etc/apt/sources.list.d/clickhouse.list /usr/share/keyrings/clickhouse-keyring.gpg")
			return nil
		}},
		{"S3 관련 정리", func() error {
			exec.ExecSudo("crontab -l 2>/dev/null | grep -v ch-glacier-cron | crontab - 2>/dev/null || true")
			exec.ExecSudo("userdel -r k-o11y-s3 2>/dev/null || true")
			exec.ExecSudo("rm -f /etc/sudoers.d/k-o11y-s3")
			exec.ExecSudo("rm -f /opt/scripts/get-s3-creds /opt/scripts/ch-glacier-cron.sh")
			exec.ExecSudo("rm -rf /etc/clickhouse-backup")
			exec.ExecSudo("rm -f /usr/local/bin/clickhouse-backup")
			exec.ExecSudo("rm -rf /etc/systemd/system/clickhouse-server.service.d")
			exec.ExecSudo("systemctl daemon-reload 2>/dev/null || true")
			return nil
		}},
		{"에이전트 정리", func() error {
			exec.ExecSudo("systemctl stop k-o11y-db-agent 2>/dev/null || true")
			exec.ExecSudo("systemctl disable k-o11y-db-agent 2>/dev/null || true")
			exec.ExecSudo("rm -f /etc/systemd/system/k-o11y-db-agent.service")
			exec.ExecSudo("rm -rf /etc/k-o11y-db-agent")
			exec.ExecSudo("rm -f /usr/local/bin/k-o11y-db")
			exec.ExecSudo("systemctl daemon-reload 2>/dev/null || true")
			return nil
		}},
		{"OTel Agent 정리", func() error {
			exec.ExecSudo("systemctl stop otelcol-contrib 2>/dev/null || true")
			exec.ExecSudo("systemctl disable otelcol-contrib 2>/dev/null || true")
			exec.ExecSudo("rm -f /etc/systemd/system/otelcol-contrib.service")
			exec.ExecSudo("apt purge -y otelcol-contrib 2>/dev/null || true")
			exec.ExecSudo("rm -rf /etc/otelcol-contrib")
			exec.ExecSudo("rm -f /usr/local/bin/otelcol-contrib /usr/bin/otelcol-contrib")
			exec.ExecSudo("systemctl daemon-reload 2>/dev/null || true")
			return nil
		}},
		{"APT 캐시 정리", func() error {
			exec.ExecSudo("apt-get clean 2>/dev/null || true")
			return nil
		}},
	}

	for i, step := range steps {
		if verbose {
			fmt.Printf("  [%d/%d] %s...\n", i+1, len(steps), step.name)
		}
		step.fn()
		if verbose {
			fmt.Printf("  ✓ %s 완료\n", step.name)
		}
	}

	fmt.Println("  ✓ ClickHouse 삭제 완료")
	return nil
}

// UninstallKeeper removes ClickHouse Keeper from the target host.
func UninstallKeeper(exec ssh.Executor, cfg *UninstallConfig, verbose bool) error {
	steps := []struct {
		name string
		fn   func() error
	}{
		{"서비스 중지", func() error {
			exec.ExecSudo("systemctl stop clickhouse-keeper 2>/dev/null || true")
			exec.ExecSudo("systemctl disable clickhouse-keeper 2>/dev/null || true")
			exec.ExecSudo("killall -9 clickhouse-keeper 2>/dev/null || true")
			exec.Exec("sleep 2")
			return nil
		}},
		{"패키지 제거", func() error {
			exec.ExecSudo("apt-mark unhold clickhouse-keeper 2>/dev/null || true")
			exec.ExecSudo("DEBIAN_FRONTEND=noninteractive apt-get purge -y clickhouse-keeper 2>/dev/null || true")
			exec.ExecSudo("apt-get autoremove -y 2>/dev/null || true")
			return nil
		}},
		{"설정/데이터 삭제", func() error {
			exec.ExecSudo("rm -rf /etc/clickhouse-keeper")
			if !cfg.KeepData {
				exec.ExecSudo("rm -rf /var/lib/clickhouse/coordination")
				exec.ExecSudo("rmdir /var/lib/clickhouse 2>/dev/null || true")
				exec.ExecSudo("rm -rf /var/log/clickhouse-keeper")
			}
			return nil
		}},
		{"APT 레포지토리/캐시 삭제", func() error {
			exec.ExecSudo("rm -f /etc/apt/sources.list.d/clickhouse.list /usr/share/keyrings/clickhouse-keyring.gpg")
			exec.ExecSudo("apt-get clean 2>/dev/null || true")
			return nil
		}},
	}

	for i, step := range steps {
		if verbose {
			fmt.Printf("  [%d/%d] %s...\n", i+1, len(steps), step.name)
		}
		step.fn()
		if verbose {
			fmt.Printf("  ✓ %s 완료\n", step.name)
		}
	}

	fmt.Println("  ✓ Keeper 삭제 완료")
	return nil
}
