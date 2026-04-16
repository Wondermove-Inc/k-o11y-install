package installer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/ssh"
	embedpkg "github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/embed"
)

// UpgradeConfig holds upgrade parameters.
type UpgradeConfig struct {
	ClickHouseHost    string
	ClickHousePass    string // 비어있으면 DDL 마이그레이션 스킵
	OtelEndpoint      string // 비어있으면 OTel Agent 업데이트 스킵
	OtelTLS           bool
	OtelTLSSkipVerify bool
	Environment       string
	AgentBinaryPath   string // linux 바이너리 경로 (빈 값이면 자동 탐색)
}

// RunUpgrade performs the upgrade of CH VM components.
func RunUpgrade(exec ssh.Executor, cfg *UpgradeConfig, verbose bool) error {
	if cfg.Environment == "" {
		cfg.Environment = "prod"
	}

	step := 1

	// DB 에이전트 업그레이드 (항상 수행)
	fmt.Printf("[%d] DB 에이전트 업그레이드\n", step)
	if err := upgradeDBAgent(exec, cfg, verbose); err != nil {
		return fmt.Errorf("DB 에이전트 업그레이드 실패: %w", err)
	}
	step++

	// OTel Agent 업데이트 (--otel-endpoint 지정 시)
	if cfg.OtelEndpoint != "" {
		fmt.Println()
		fmt.Printf("[%d] OTel Agent 업데이트\n", step)
		if err := upgradeOtelAgent(exec, cfg, verbose); err != nil {
			return fmt.Errorf("OTel Agent 업데이트 실패: %w", err)
		}
		step++
	}

	// DDL 마이그레이션 (--clickhouse-password 지정 시)
	if cfg.ClickHousePass != "" {
		fmt.Println()
		fmt.Printf("[%d] DDL 마이그레이션\n", step)
		if err := runDDLMigrations(exec, cfg, verbose); err != nil {
			return fmt.Errorf("DDL 마이그레이션 실패: %w", err)
		}
	}

	return nil
}

// upgradeDBAgent replaces the agent binary and restarts the service.
func upgradeDBAgent(exec ssh.Executor, cfg *UpgradeConfig, verbose bool) error {
	// linux 바이너리 경로 결정
	binaryPath := cfg.AgentBinaryPath
	if binaryPath == "" {
		selfPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("실행 파일 경로 확인 실패: %w", err)
		}
		dir := filepath.Dir(selfPath)
		parentDir := filepath.Dir(dir)
		binaryPath = filepath.Join(parentDir, "linux-amd64", "k-o11y-db")
		if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
			return fmt.Errorf("linux 바이너리를 찾을 수 없습니다: %s\n  --agent-binary 플래그로 직접 지정하거나 make build-all을 실행하세요", binaryPath)
		}
	}

	binBytes, err := os.ReadFile(binaryPath)
	if err != nil {
		return fmt.Errorf("바이너리 읽기 실패: %w", err)
	}

	if verbose {
		fmt.Printf("  바이너리: %s (%d MB)\n", binaryPath, len(binBytes)/1024/1024)
	}

	// 변경 전 버전 확인
	oldVersion := "unknown"
	if result, err := exec.Exec("curl -sf http://127.0.0.1:8099/health 2>/dev/null"); err == nil {
		if v := extractJSONField(result.Stdout, "version"); v != "" {
			oldVersion = v
		}
	}

	// 기존 바이너리 백업
	if verbose {
		fmt.Println("  기존 바이너리 백업 중...")
	}
	exec.ExecSudo("cp /usr/local/bin/k-o11y-db /usr/local/bin/k-o11y-db.bak 2>/dev/null || true")

	// 서비스 중지
	if verbose {
		fmt.Println("  서비스 중지 중...")
	}
	exec.ExecSudo("systemctl stop k-o11y-db-agent 2>/dev/null || true")

	// 바이너리 교체
	if verbose {
		fmt.Println("  바이너리 교체 중...")
	}
	if err := exec.UploadBytes(binBytes, "/tmp/k-o11y-db", 0755); err != nil {
		return fmt.Errorf("바이너리 업로드 실패: %w", err)
	}
	if _, err := exec.ExecSudo("mv /tmp/k-o11y-db /usr/local/bin/k-o11y-db"); err != nil {
		return fmt.Errorf("바이너리 이동 실패: %w", err)
	}
	exec.ExecSudo("chmod 755 /usr/local/bin/k-o11y-db")

	// 서비스 시작
	if verbose {
		fmt.Println("  서비스 시작 중...")
	}
	if _, err := exec.ExecSudo("systemctl start k-o11y-db-agent"); err != nil {
		// 롤백
		fmt.Println("  ⚠️ 서비스 시작 실패, 이전 버전으로 롤백 중...")
		exec.ExecSudo("mv /usr/local/bin/k-o11y-db.bak /usr/local/bin/k-o11y-db 2>/dev/null || true")
		exec.ExecSudo("systemctl start k-o11y-db-agent 2>/dev/null || true")
		return fmt.Errorf("서비스 시작 실패 (롤백 완료): %w", err)
	}

	// 헬스체크
	exec.Exec("sleep 3")
	result, err := exec.Exec("curl -sf http://127.0.0.1:8099/health 2>/dev/null && echo OK || echo FAIL")
	if err != nil || !strings.Contains(result.Stdout, "OK") {
		// 롤백
		fmt.Println("  ⚠️ 헬스체크 실패, 이전 버전으로 롤백 중...")
		exec.ExecSudo("systemctl stop k-o11y-db-agent 2>/dev/null || true")
		exec.ExecSudo("mv /usr/local/bin/k-o11y-db.bak /usr/local/bin/k-o11y-db 2>/dev/null || true")
		exec.ExecSudo("systemctl start k-o11y-db-agent 2>/dev/null || true")
		return fmt.Errorf("헬스체크 실패 (이전 버전으로 롤백 완료) — journalctl -u k-o11y-db-agent 확인")
	}

	// 변경 후 버전 확인
	newVersion := "unknown"
	if result, err := exec.Exec("curl -sf http://127.0.0.1:8099/health 2>/dev/null"); err == nil {
		if v := extractJSONField(result.Stdout, "version"); v != "" {
			newVersion = v
		}
	}

	// 백업 파일 정리
	exec.ExecSudo("rm -f /usr/local/bin/k-o11y-db.bak")

	fmt.Printf("  ✓ DB 에이전트 업그레이드 완료: %s → %s\n", oldVersion, newVersion)
	return nil
}

// extractJSONField extracts a string value from a simple JSON object.
func extractJSONField(jsonStr, field string) string {
	key := `"` + field + `":"`
	idx := strings.Index(jsonStr, key)
	if idx < 0 {
		return ""
	}
	start := idx + len(key)
	end := strings.Index(jsonStr[start:], `"`)
	if end < 0 {
		return ""
	}
	return jsonStr[start : start+end]
}

// upgradeOtelAgent updates the OTel Agent config and restarts.
func upgradeOtelAgent(exec ssh.Executor, cfg *UpgradeConfig, verbose bool) error {
	// config 렌더링
	tmplBytes, err := embedpkg.ReadTemplate("otelcol-contrib.yaml.tmpl")
	if err != nil {
		return fmt.Errorf("config 템플릿 로드 실패: %w", err)
	}

	tmpl, err := template.New("otel-config").Parse(string(tmplBytes))
	if err != nil {
		return fmt.Errorf("config 파싱 실패: %w", err)
	}

	data := &OtelAgentConfig{
		OtelEndpoint:      cfg.OtelEndpoint,
		Environment:       cfg.Environment,
		OtelTLS:           cfg.OtelTLS,
		OtelTLSSkipVerify: cfg.OtelTLSSkipVerify,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("config 렌더링 실패: %w", err)
	}

	// 기존 config 백업
	if verbose {
		fmt.Println("  기존 config 백업 중...")
	}
	exec.ExecSudo("cp /etc/otelcol-contrib/k-o11y-otel-agent.yaml /etc/otelcol-contrib/k-o11y-otel-agent.yaml.bak 2>/dev/null || true")

	// config 교체
	if verbose {
		fmt.Println("  config 업데이트 중...")
	}
	if err := exec.UploadBytes(buf.Bytes(), "/tmp/k-o11y-otel-agent.yaml", 0644); err != nil {
		return fmt.Errorf("config 업로드 실패: %w", err)
	}
	exec.ExecSudo("mv /tmp/k-o11y-otel-agent.yaml /etc/otelcol-contrib/k-o11y-otel-agent.yaml")

	// 서비스 재시작
	if verbose {
		fmt.Println("  서비스 재시작 중...")
	}
	exec.ExecSudo("systemctl restart otelcol-contrib")

	// 헬스체크
	exec.Exec("sleep 5")
	result, err := exec.Exec("curl -sf http://localhost:13133 2>/dev/null && echo OK || echo FAIL")
	if err != nil || !strings.Contains(result.Stdout, "OK") {
		// 롤백
		fmt.Println("  ⚠️ OTel Agent 헬스체크 실패, 이전 config로 롤백 중...")
		exec.ExecSudo("mv /etc/otelcol-contrib/k-o11y-otel-agent.yaml.bak /etc/otelcol-contrib/k-o11y-otel-agent.yaml 2>/dev/null || true")
		exec.ExecSudo("systemctl restart otelcol-contrib 2>/dev/null || true")
		return fmt.Errorf("OTel Agent 헬스체크 실패 (이전 config로 롤백 완료) — journalctl -u otelcol-contrib 확인")
	}

	// 백업 파일 정리
	exec.ExecSudo("rm -f /etc/otelcol-contrib/k-o11y-otel-agent.yaml.bak")

	fmt.Println("  ✓ OTel Agent 업데이트 완료")
	return nil
}

// runDDLMigrations applies DDL migration scripts.
func runDDLMigrations(exec ssh.Executor, cfg *UpgradeConfig, verbose bool) error {
	chClient := fmt.Sprintf("clickhouse-client --host localhost --password '%s'", cfg.ClickHousePass)

	// embed/sql/migrations/ 디렉토리의 .sql 파일 실행
	migrations, err := listMigrations()
	if err != nil || len(migrations) == 0 {
		if verbose {
			fmt.Println("  마이그레이션 파일 없음, 스킵")
		}
		fmt.Println("  ✓ DDL 마이그레이션 완료 (변경 없음)")
		return nil
	}

	for i, name := range migrations {
		if verbose {
			fmt.Printf("  [%d/%d] %s...\n", i+1, len(migrations), name)
		}

		sqlBytes, err := embedpkg.ReadSQL("migrations/" + name)
		if err != nil {
			return fmt.Errorf("%s 로드 실패: %w", name, err)
		}

		tmpPath := "/tmp/migration-" + name
		if err := exec.UploadBytes(sqlBytes, tmpPath, 0644); err != nil {
			return fmt.Errorf("%s 업로드 실패: %w", name, err)
		}

		result, err := exec.Exec(fmt.Sprintf("%s --multiquery < %s", chClient, tmpPath))
		if err != nil {
			return fmt.Errorf("%s 실행 실패: %w", name, err)
		}
		if result.ExitCode != 0 {
			// "already exists" 에러는 무시 (멱등성)
			if !strings.Contains(result.Stderr, "already exists") &&
				!strings.Contains(result.Stderr, "duplicate column") {
				return fmt.Errorf("%s 에러: %s", name, result.Stderr)
			}
			if verbose {
				fmt.Printf("  ✓ %s (이미 적용됨, 스킵)\n", name)
			}
		} else if verbose {
			fmt.Printf("  ✓ %s\n", name)
		}

		exec.Exec(fmt.Sprintf("rm -f %s", tmpPath))
	}

	fmt.Println("  ✓ DDL 마이그레이션 완료")
	return nil
}

// listMigrations returns migration SQL files sorted by name.
func listMigrations() ([]string, error) {
	entries, err := embedpkg.EmbeddedFS.ReadDir("sql/migrations")
	if err != nil {
		return nil, nil // 디렉토리 없으면 빈 목록
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
