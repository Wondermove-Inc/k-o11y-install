package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/installer"
	sshpkg "github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/ssh"
)

var (
	postClickhouseHost    string
	postClickhousePass    string
	postOtelEndpoint      string
	postEnvironment       string
	postOtelTLS           bool
	postOtelTLSSkipVerify bool
)

var postInstallCmd = &cobra.Command{
	Use:   "post-install",
	Short: "DDL + 메타데이터 테이블 적용",
	Long:  "Host 클러스터 배포 후, ClickHouse에 DDL과 메타데이터 테이블을 적용합니다.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if postClickhouseHost == "" || postClickhousePass == "" {
			fmt.Println("필수 인자가 누락되었습니다:")
			if postClickhouseHost == "" {
				fmt.Println("  --clickhouse-host")
			}
			if postClickhousePass == "" {
				fmt.Println("  --clickhouse-password")
			}
			os.Exit(1)
		}

		fmt.Println("==========================================")
		fmt.Println("  Post-Install (DDL 적용)")
		fmt.Println("==========================================")
		fmt.Printf("  Mode:            %s\n", mode)
		fmt.Printf("  ClickHouse Host: %s\n", postClickhouseHost)
		fmt.Println("==========================================")

		if dryRun {
			fmt.Println("[DRY-RUN] 실제 적용을 수행하지 않습니다.")
			fmt.Println()
			fmt.Println("수행할 작업:")
			fmt.Println("  1. Custom DDL 적용 (50+ 테이블, MV, Dictionary)")
			fmt.Println("  2. 메타 테이블 생성 (data_lifecycle_config)")
			fmt.Println("  3. lifecycle 초기값 INSERT")
			fmt.Println("  4. 메타 테이블 생성 (s3_config)")
			fmt.Println("  5. 메타 테이블 생성 (agent_status)")
			return nil
		}

		// SSH 연결
		sshCfg := &sshpkg.Config{
			Mode:        mode,
			Host:        postClickhouseHost,
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

		cfg := &installer.PostInstallConfig{
			Host:              postClickhouseHost,
			Password:          postClickhousePass,
			OtelEndpoint:      postOtelEndpoint,
			Environment:       postEnvironment,
			OtelTLS:           postOtelTLS,
			OtelTLSSkipVerify: postOtelTLSSkipVerify,
		}

		if err := installer.RunPostInstall(exec, cfg, verbose); err != nil {
			return fmt.Errorf("Post-Install 실패: %w", err)
		}

		fmt.Println()
		fmt.Println("==========================================")
		fmt.Println("  Post-Install 완료")
		fmt.Println("==========================================")
		fmt.Println()
		fmt.Println("  다음 단계: UI에서 데이터 보존 기간(TTL)과 S3 스토리지를 설정하세요.")

		return nil
	},
}

func init() {
	postInstallCmd.Flags().StringVar(&postClickhouseHost, "clickhouse-host", "", "ClickHouse 호스트")
	postInstallCmd.Flags().StringVar(&postClickhousePass, "clickhouse-password", "", "ClickHouse 비밀번호")
	postInstallCmd.Flags().StringVar(&postOtelEndpoint, "otel-endpoint", "", "Host OTel Gateway endpoint (예: 10.0.1.50:4317). 지정 시 OTel Agent 설치")
	postInstallCmd.Flags().StringVar(&postEnvironment, "environment", "prod", "deployment.environment (dev/staging/prod)")
	postInstallCmd.Flags().BoolVar(&postOtelTLS, "otel-tls", false, "OTel Agent → Host Gateway TLS 활성화")
	postInstallCmd.Flags().BoolVar(&postOtelTLSSkipVerify, "otel-tls-skip-verify", false, "TLS 서버 인증서 검증 스킵 (self-signed용)")
}
