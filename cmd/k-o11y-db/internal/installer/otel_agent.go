package installer

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/ssh"
	embedpkg "github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/embed"
)

const otelContribVersion = "0.109.0"

// OtelAgentConfig holds OTel Agent installation parameters.
type OtelAgentConfig struct {
	OtelEndpoint      string // Host OTel Gateway endpoint (e.g., "10.0.1.50:4317")
	Environment       string // deployment.environment (default: "prod")
	OtelTLS           bool   // TLS 활성화
	OtelTLSSkipVerify bool   // 서버 인증서 검증 스킵 (self-signed용)
}

// InstallOtelAgent installs otelcol-contrib on the ClickHouse VM.
// Downloads the .deb package from GitHub, generates config, and starts systemd service.
func InstallOtelAgent(exec ssh.Executor, cfg *OtelAgentConfig, verbose bool) error {
	if cfg.Environment == "" {
		cfg.Environment = "prod"
	}

	steps := []struct {
		name string
		fn   func() error
	}{
		{"아키텍처 감지", func() error {
			// 실제 아키텍처는 다음 스텝에서 사용
			return nil
		}},
		{"otelcol-contrib 다운로드 + 설치", func() error {
			// 아키텍처 감지
			archResult, err := exec.Exec("dpkg --print-architecture 2>/dev/null || uname -m")
			if err != nil {
				return fmt.Errorf("아키텍처 감지 실패: %w", err)
			}
			arch := strings.TrimSpace(archResult.Stdout)
			switch arch {
			case "arm64", "aarch64":
				arch = "arm64"
			default:
				arch = "amd64"
			}

			// 이미 설치 확인
			checkResult, _ := exec.Exec("otelcol-contrib --version 2>/dev/null")
			if checkResult != nil && strings.Contains(checkResult.Stdout, otelContribVersion) {
				if verbose {
					fmt.Printf("    otelcol-contrib %s 이미 설치됨, 스킵\n", otelContribVersion)
				}
				return nil
			}

			// GitHub에서 다운로드
			url := fmt.Sprintf(
				"https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v%s/otelcol-contrib_%s_linux_%s.deb",
				otelContribVersion, otelContribVersion, arch)

			debFile := fmt.Sprintf("/tmp/otelcol-contrib_%s_linux_%s.deb", otelContribVersion, arch)

			if verbose {
				fmt.Printf("    다운로드: %s → %s\n", url, debFile)
			}

			if _, err := exec.Exec(fmt.Sprintf("wget -q %s -O %s", url, debFile)); err != nil {
				return fmt.Errorf("다운로드 실패: %w", err)
			}

			// apt install
			if _, err := exec.ExecSudo(fmt.Sprintf("apt install -y %s", debFile)); err != nil {
				return fmt.Errorf("설치 실패: %w", err)
			}

			// 정리
			exec.Exec(fmt.Sprintf("rm -f %s", debFile))
			return nil
		}},
		{"OTel Agent 설정 배포", func() error {
			// config 디렉토리 생성
			exec.ExecSudo("mkdir -p /etc/otelcol-contrib")

			// YAML 템플릿 렌더링
			tmplBytes, err := embedpkg.ReadTemplate("otelcol-contrib.yaml.tmpl")
			if err != nil {
				return fmt.Errorf("config 템플릿 로드 실패: %w", err)
			}

			tmpl, err := template.New("otel-config").Parse(string(tmplBytes))
			if err != nil {
				return fmt.Errorf("config 템플릿 파싱 실패: %w", err)
			}

			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, cfg); err != nil {
				return fmt.Errorf("config 렌더링 실패: %w", err)
			}

			// 업로드
			if err := exec.UploadBytes(buf.Bytes(), "/tmp/k-o11y-otel-agent.yaml", 0644); err != nil {
				return fmt.Errorf("config 업로드 실패: %w", err)
			}
			exec.ExecSudo("mv /tmp/k-o11y-otel-agent.yaml /etc/otelcol-contrib/k-o11y-otel-agent.yaml")
			return nil
		}},
		{"systemd unit 배포", func() error {
			// 바이너리 경로 동적 감지
			binResult, err := exec.Exec("which otelcol-contrib 2>/dev/null")
			binPath := "/usr/bin/otelcol-contrib" // 기본값
			if err == nil && strings.TrimSpace(binResult.Stdout) != "" {
				binPath = strings.TrimSpace(binResult.Stdout)
			}

			unitContent := fmt.Sprintf(`[Unit]
Description=K-O11y OpenTelemetry Collector Agent
After=network-online.target clickhouse-server.service
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=%s --config=/etc/otelcol-contrib/k-o11y-otel-agent.yaml
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
`, binPath)

			if err := exec.UploadBytes([]byte(unitContent), "/tmp/otelcol-contrib.service", 0644); err != nil {
				return fmt.Errorf("systemd 업로드 실패: %w", err)
			}
			exec.ExecSudo("mv /tmp/otelcol-contrib.service /etc/systemd/system/otelcol-contrib.service")

			if verbose {
				fmt.Printf("    바이너리 경로: %s\n", binPath)
			}
			return nil
		}},
		{"서비스 시작", func() error {
			cmds := []string{
				"systemctl daemon-reload",
				"systemctl enable otelcol-contrib",
				"systemctl restart otelcol-contrib",
			}
			for _, cmd := range cmds {
				if _, err := exec.ExecSudo(cmd); err != nil {
					return fmt.Errorf("'%s' 실패: %w", cmd, err)
				}
			}
			return nil
		}},
		{"헬스체크", func() error {
			exec.Exec("sleep 3")
			result, err := exec.Exec("curl -sf http://localhost:13133 2>/dev/null && echo OK || echo FAIL")
			if err != nil || !strings.Contains(result.Stdout, "OK") {
				return fmt.Errorf("otelcol-contrib 헬스체크 실패 — 로그 확인: journalctl -u otelcol-contrib --no-pager -n 20")
			}
			return nil
		}},
	}

	for i, step := range steps {
		if i == 0 {
			continue // 아키텍처 감지는 다음 스텝에 통합
		}
		if verbose {
			fmt.Printf("  [%d/%d] %s...\n", i, len(steps)-1, step.name)
		}
		if err := step.fn(); err != nil {
			return fmt.Errorf("OTel Agent Step %d (%s) 실패: %w", i, step.name, err)
		}
		if verbose {
			fmt.Printf("  ✓ %s 완료\n", step.name)
		}
	}

	fmt.Println("  ✓ OTel Agent 설치 완료")
	return nil
}
