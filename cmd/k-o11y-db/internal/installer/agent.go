package installer

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/ssh"
	embedpkg "github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/embed"
)

// AgentConfig는 에이전트 배포에 필요한 설정입니다.
type AgentConfig struct {
	ClickHousePassword string
	EncryptionKey      string
	BinaryPath         string // linux 바이너리 경로 (빈 값이면 자동 탐색)
}

// InstallAgent는 CH VM에 에이전트 바이너리 + systemd 서비스를 배포합니다.
// install 서브커맨드의 Step 3/4에서 호출됩니다.
func InstallAgent(exec ssh.Executor, cfg *AgentConfig, verbose bool) error {
	steps := []struct {
		name string
		fn   func() error
	}{
		{"에이전트 바이너리 배포", func() error {
			// linux/amd64 바이너리 경로 결정
			binaryPath := cfg.BinaryPath
			if binaryPath == "" {
				// 현재 실행 파일 기준으로 dist/linux-amd64/ 자동 탐색
				selfPath, err := os.Executable()
				if err != nil {
					return fmt.Errorf("실행 파일 경로 확인 실패: %w", err)
				}
				dir := filepath.Dir(selfPath)
				// dist/darwin-arm64/ → dist/linux-amd64/ 로 변환
				parentDir := filepath.Dir(dir)
				binaryPath = filepath.Join(parentDir, "linux-amd64", "k-o11y-db")
				if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
					return fmt.Errorf("linux 바이너리를 찾을 수 없습니다: %s\n  --agent-binary 플래그로 직접 지정하거나 make build-all을 실행하세요", binaryPath)
				}
			}

			selfBytes, err := os.ReadFile(binaryPath)
			if err != nil {
				return fmt.Errorf("바이너리 읽기 실패 (%s): %w", binaryPath, err)
			}
			if verbose {
				fmt.Printf("    바이너리: %s (%d MB)\n", binaryPath, len(selfBytes)/1024/1024)
			}

			// /tmp에 업로드 후 /usr/local/bin으로 이동
			if err := exec.UploadBytes(selfBytes, "/tmp/k-o11y-db", 0755); err != nil {
				return fmt.Errorf("바이너리 업로드 실패: %w", err)
			}
			if _, err := exec.ExecSudo("mv /tmp/k-o11y-db /usr/local/bin/k-o11y-db"); err != nil {
				return fmt.Errorf("바이너리 이동 실패: %w", err)
			}
			if _, err := exec.ExecSudo("chmod 755 /usr/local/bin/k-o11y-db"); err != nil {
				return fmt.Errorf("바이너리 권한 설정 실패: %w", err)
			}
			return nil
		}},
		{"agent.env 생성", func() error {
			if _, err := exec.ExecSudo("mkdir -p /etc/k-o11y-db-agent"); err != nil {
				return fmt.Errorf("디렉토리 생성 실패: %w", err)
			}

			envContent := fmt.Sprintf("CH_PASSWORD=%s\nK_O11Y_ENCRYPTION_KEY=%s\n",
				cfg.ClickHousePassword, cfg.EncryptionKey)

			if err := exec.UploadBytes([]byte(envContent), "/tmp/agent.env", 0600); err != nil {
				return fmt.Errorf("agent.env 업로드 실패: %w", err)
			}
			if _, err := exec.ExecSudo("mv /tmp/agent.env /etc/k-o11y-db-agent/agent.env"); err != nil {
				return fmt.Errorf("agent.env 이동 실패: %w", err)
			}
			if _, err := exec.ExecSudo("chmod 600 /etc/k-o11y-db-agent/agent.env"); err != nil {
				return fmt.Errorf("agent.env 권한 설정 실패: %w", err)
			}
			return nil
		}},
		{"systemd unit 배포", func() error {
			unitBytes, err := embedpkg.ReadTemplate("k-o11y-db-agent.service.tmpl")
			if err != nil {
				return fmt.Errorf("systemd unit 템플릿 로드 실패: %w", err)
			}

			if err := exec.UploadBytes(unitBytes, "/tmp/k-o11y-db-agent.service", 0644); err != nil {
				return fmt.Errorf("systemd unit 업로드 실패: %w", err)
			}
			if _, err := exec.ExecSudo("mv /tmp/k-o11y-db-agent.service /etc/systemd/system/k-o11y-db-agent.service"); err != nil {
				return fmt.Errorf("systemd unit 이동 실패: %w", err)
			}
			return nil
		}},
		{"에이전트 서비스 시작", func() error {
			cmds := []string{
				"systemctl daemon-reload",
				"systemctl enable k-o11y-db-agent",
				"systemctl start k-o11y-db-agent",
			}
			for _, cmd := range cmds {
				if _, err := exec.ExecSudo(cmd); err != nil {
					return fmt.Errorf("'%s' 실패: %w", cmd, err)
				}
			}
			return nil
		}},
		{"에이전트 서비스 상태 확인", func() error {
			exec.Exec("sleep 3")
			result, err := exec.Exec("systemctl is-active k-o11y-db-agent")
			if err != nil {
				return fmt.Errorf("상태 확인 실패: %w", err)
			}
			status := result.Stdout
			if len(status) > 0 && status[len(status)-1] == '\n' {
				status = status[:len(status)-1]
			}
			if status != "active" {
				return fmt.Errorf("에이전트 서비스 시작 실패 (status: %s)", status)
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

	fmt.Println("  ✓ 에이전트 배포 완료")
	return nil
}
