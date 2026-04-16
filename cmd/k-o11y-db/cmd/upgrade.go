package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/installer"
	sshpkg "github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/ssh"
)

var (
	upgradeClickhouseHost    string
	upgradeClickhousePass    string
	upgradeOtelEndpoint      string
	upgradeOtelTLS           bool
	upgradeOtelTLSSkipVerify bool
	upgradeEnvironment       string
	upgradeAgentBinary       string
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "CH VM 컴포넌트 업그레이드",
	Long: `CH VM의 DB 에이전트, OTel Agent, DDL 스키마를 업그레이드합니다.

업그레이드 대상:
  1. DB 에이전트     바이너리 교체 + systemd restart (항상 수행)
  2. OTel Agent     config 업데이트 + restart (--otel-endpoint 지정 시)
  3. DDL 마이그레이션  스키마 확장 (--clickhouse-password 지정 시)

예시:
  # 에이전트만 업그레이드
  k-o11y-db upgrade --clickhouse-host 10.0.1.11

  # 전체 업그레이드 (에이전트 + OTel + DDL)
  k-o11y-db upgrade \
    --clickhouse-host 10.0.1.11 \
    --clickhouse-password 'pw' \
    --otel-endpoint 10.0.1.50:4317 \
    --otel-tls --otel-tls-skip-verify`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if upgradeClickhouseHost == "" {
			fmt.Println("필수 인자가 누락되었습니다:")
			fmt.Println("  --clickhouse-host")
			os.Exit(1)
		}

		fmt.Println("==========================================")
		fmt.Println("  K-O11y 업그레이드")
		fmt.Println("==========================================")
		fmt.Printf("  Mode:            %s\n", mode)
		fmt.Printf("  ClickHouse Host: %s\n", upgradeClickhouseHost)
		if upgradeClickhousePass != "" {
			fmt.Println("  DDL 마이그레이션: 예")
		}
		if upgradeOtelEndpoint != "" {
			fmt.Printf("  OTel Endpoint:   %s\n", upgradeOtelEndpoint)
			if upgradeOtelTLS {
				fmt.Println("  OTel TLS:        예")
			}
		}
		fmt.Println("==========================================")

		if dryRun {
			fmt.Println("[DRY-RUN] 실제 업그레이드를 수행하지 않습니다.")
			fmt.Println()
			fmt.Println("수행할 작업:")
			fmt.Println("  1. DB 에이전트 바이너리 교체 + restart")
			if upgradeOtelEndpoint != "" {
				fmt.Println("  2. OTel Agent config 업데이트 + restart")
			}
			if upgradeClickhousePass != "" {
				fmt.Println("  3. DDL 마이그레이션 (스키마 확장)")
			}
			return nil
		}

		confirmOrExit("업그레이드를 진행합니까?")

		// SSH 연결
		sshCfg := &sshpkg.Config{
			Mode:        mode,
			Host:        upgradeClickhouseHost,
			Port:        sshPort,
			User:        sshUser,
			KeyPath:     sshKey,
			Password:    sshPass,
			BastionHost: bastionHost,
			BastionPort: bastionPort,
			BastionUser: bastionUser,
			BastionKey:  bastionKey,
			Verbose:     verbose,
		}

		exec, err := sshpkg.NewExecutor(sshCfg)
		if err != nil {
			return fmt.Errorf("ClickHouse VM 연결 실패: %w", err)
		}
		defer exec.Close()
		fmt.Println("  ✓ ClickHouse VM 연결 성공")
		fmt.Println()

		cfg := &installer.UpgradeConfig{
			ClickHouseHost:    upgradeClickhouseHost,
			ClickHousePass:    upgradeClickhousePass,
			OtelEndpoint:      upgradeOtelEndpoint,
			OtelTLS:           upgradeOtelTLS,
			OtelTLSSkipVerify: upgradeOtelTLSSkipVerify,
			Environment:       upgradeEnvironment,
			AgentBinaryPath:   upgradeAgentBinary,
		}

		if err := installer.RunUpgrade(exec, cfg, verbose); err != nil {
			return fmt.Errorf("업그레이드 실패: %w", err)
		}

		fmt.Println()
		fmt.Println("==========================================")
		fmt.Println("  업그레이드 완료")
		fmt.Println("==========================================")

		return nil
	},
}

func init() {
	upgradeCmd.Flags().StringVar(&upgradeClickhouseHost, "clickhouse-host", "", "ClickHouse 호스트 (필수)")
	upgradeCmd.Flags().StringVar(&upgradeClickhousePass, "clickhouse-password", "", "ClickHouse 비밀번호 (지정 시 DDL 마이그레이션 수행)")
	upgradeCmd.Flags().StringVar(&upgradeOtelEndpoint, "otel-endpoint", "", "Host OTel Gateway endpoint (지정 시 OTel Agent 업데이트)")
	upgradeCmd.Flags().BoolVar(&upgradeOtelTLS, "otel-tls", false, "OTel Agent TLS 활성화")
	upgradeCmd.Flags().BoolVar(&upgradeOtelTLSSkipVerify, "otel-tls-skip-verify", false, "TLS 인증서 검증 스킵 (self-signed)")
	upgradeCmd.Flags().StringVar(&upgradeEnvironment, "environment", "prod", "deployment.environment")
	upgradeCmd.Flags().StringVar(&upgradeAgentBinary, "agent-binary", "", "에이전트 linux 바이너리 경로 (미지정 시 자동 탐색)")
}
