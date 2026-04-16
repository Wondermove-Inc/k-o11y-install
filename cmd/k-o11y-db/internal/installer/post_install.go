package installer

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/ssh"
	embedpkg "github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/embed"
)

// PostInstallConfig holds post-install parameters.
type PostInstallConfig struct {
	Host              string
	Password          string // ClickHouse default user password
	OtelEndpoint      string // Host OTel Gateway endpoint (e.g., "10.0.1.50:4317"). 비어있으면 OTel Agent 설치 스킵.
	Environment       string // deployment.environment (default: "prod")
	OtelTLS           bool   // OTel Agent TLS 활성화
	OtelTLSSkipVerify bool   // OTel Agent TLS 인증서 검증 스킵
}

// RunPostInstall applies DDL and metadata tables to ClickHouse.
func RunPostInstall(exec ssh.Executor, cfg *PostInstallConfig, verbose bool) error {
	chClient := fmt.Sprintf("clickhouse-client --host localhost --password '%s'", cfg.Password)

	steps := []struct {
		name string
		fn   func() error
	}{
		{"Custom DDL 적용", func() error {
			ddlBytes, err := embedpkg.ReadSQL("clickhouse-ddl.sql")
			if err != nil {
				return fmt.Errorf("DDL 파일 로드 실패: %w", err)
			}

			// {{CH_PASSWORD}} 플레이스홀더 치환
			ddlBytes = bytes.ReplaceAll(ddlBytes, []byte("{{CH_PASSWORD}}"), []byte(cfg.Password))

			// 업로드 + 실행
			if err := exec.UploadBytes(ddlBytes, "/tmp/clickhouse-ddl.sql", 0644); err != nil {
				return fmt.Errorf("DDL 업로드 실패: %w", err)
			}
			result, err := exec.Exec(fmt.Sprintf("%s --multiquery < /tmp/clickhouse-ddl.sql", chClient))
			if err != nil {
				return fmt.Errorf("DDL 실행 실패: %w", err)
			}
			if result.ExitCode != 0 {
				return fmt.Errorf("DDL 실행 에러: %s", result.Stderr)
			}
			return nil
		}},
		// cold_storage_config는 레거시 — 삭제됨 ( 에이전트 도입)
		{"메타 테이블 생성 (data_lifecycle_config)", func() error {
			return applySQLFile(exec, chClient, "k-o11y-data-lifecycle-config.sql")
		}},
		{"lifecycle 초기값 INSERT", func() error {
			initSQL := `INSERT INTO k_o11y.data_lifecycle_config
				(signal_type, hot_days, warm_days, glacier_enabled, glacier_retention_days,
				 backup_frequency_hours, updated_by, version)
				SELECT 'global', 7, 90, 0, 0, 24, 'install-script', toUnixTimestamp(now())
				WHERE NOT EXISTS (
					SELECT 1 FROM k_o11y.data_lifecycle_config FINAL
					WHERE signal_type = 'global'
				)`
			return execSQL(exec, chClient, initSQL)
		}},
		// cold → lifecycle 마이그레이션은 레거시 — 삭제됨
		{"메타 테이블 생성 (s3_config)", func() error {
			return applySQLFile(exec, chClient, "k-o11y-s3-config.sql")
		}},
		{"메타 테이블 생성 (agent_status)", func() error {
			return applySQLFile(exec, chClient, "k-o11y-agent-status.sql")
		}},
		{"메타 테이블 생성 (sso_config)", func() error {
			return applySQLFile(exec, chClient, "k-o11y-sso-config.sql")
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

	fmt.Println("  ✓ Post-Install (DDL) 완료")

	// OTel Agent 설치 (--otel-endpoint가 지정된 경우만)
	if cfg.OtelEndpoint != "" {
		fmt.Println()
		fmt.Println("  OTel Agent 설치 중...")
		otelCfg := &OtelAgentConfig{
			OtelEndpoint:      cfg.OtelEndpoint,
			Environment:       cfg.Environment,
			OtelTLS:           cfg.OtelTLS,
			OtelTLSSkipVerify: cfg.OtelTLSSkipVerify,
		}
		if err := InstallOtelAgent(exec, otelCfg, verbose); err != nil {
			return fmt.Errorf("OTel Agent 설치 실패: %w", err)
		}
	}

	fmt.Println("  ✓ Post-Install 완료")
	return nil
}

// applySQLFile reads an embedded SQL file and executes it via clickhouse-client.
func applySQLFile(exec ssh.Executor, chClient string, filename string) error {
	sqlBytes, err := embedpkg.ReadSQL(filename)
	if err != nil {
		return fmt.Errorf("%s 로드 실패: %w", filename, err)
	}

	tmpPath := "/tmp/" + filename
	if err := exec.UploadBytes(sqlBytes, tmpPath, 0644); err != nil {
		return fmt.Errorf("%s 업로드 실패: %w", filename, err)
	}

	result, err := exec.Exec(fmt.Sprintf("%s --multiquery < %s", chClient, tmpPath))
	if err != nil {
		return fmt.Errorf("%s 실행 실패: %w", filename, err)
	}
	if result.ExitCode != 0 {
		// 일부 SQL은 이미 존재하면 에러를 반환하지만 무시 가능
		if !strings.Contains(result.Stderr, "already exists") {
			return fmt.Errorf("%s 에러: %s", filename, result.Stderr)
		}
	}
	return nil
}

// execSQL executes a single SQL statement via clickhouse-client.
func execSQL(exec ssh.Executor, chClient string, sql string) error {
	// SQL을 임시 파일에 쓰고 실행 (특수문자 이스케이프 방지)
	tmpPath := "/tmp/k-o11y-exec-sql.sql"
	if err := exec.UploadBytes([]byte(sql), tmpPath, 0644); err != nil {
		return fmt.Errorf("SQL 업로드 실패: %w", err)
	}
	result, err := exec.Exec(fmt.Sprintf("%s --multiquery < %s", chClient, tmpPath))
	if err != nil {
		return fmt.Errorf("SQL 실행 실패: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("SQL 에러: %s", result.Stderr)
	}
	return nil
}
