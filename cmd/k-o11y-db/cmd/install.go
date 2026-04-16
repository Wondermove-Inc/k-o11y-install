package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/installer"
	sshpkg "github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/ssh"
)

var (
	keeperHost         string
	clickhouseHost     string
	clickhousePass     string
	keeperHostname     string
	clickhouseHostname string
	clickhouseVersion  string
	keeperVersion      string
	clusterName        string
	keeperClusterHost  string
	chClusterHost      string
	installEncKey      string // --encryption-key (에이전트 배포용)
	agentBinary        string // --agent-binary (linux 바이너리 경로)
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Keeper + ClickHouse 설치",
	Long:  "SSH로 VM에 접속하여 ClickHouse Keeper와 ClickHouse Server를 설치합니다.",
	RunE: func(cmd *cobra.Command, args []string) error {
		// 필수값 검증
		if keeperHost == "" || clickhouseHost == "" || clickhousePass == "" {
			fmt.Println("필수 인자가 누락되었습니다:")
			if keeperHost == "" {
				fmt.Println("  --keeper-host")
			}
			if clickhouseHost == "" {
				fmt.Println("  --clickhouse-host")
			}
			if clickhousePass == "" {
				fmt.Println("  --clickhouse-password")
			}
			os.Exit(1)
		}

		fmt.Println("==========================================")
		fmt.Println("  K-O11y DB 설치")
		fmt.Println("==========================================")
		fmt.Printf("  Mode:            %s\n", mode)
		fmt.Printf("  Keeper Host:     %s\n", keeperHost)
		fmt.Printf("  ClickHouse Host: %s\n", clickhouseHost)
		fmt.Printf("  SSH User:        %s\n", sshUser)
		fmt.Println("==========================================")

		// --encryption-key 미입력 시 안내
		if installEncKey == "" {
			fmt.Println()
			fmt.Println("  💡 --encryption-key가 비어있어 에이전트를 배포하지 않습니다.")
			fmt.Println("     에이전트를 함께 설치하려면 --encryption-key를 추가하세요:")
			fmt.Println()
			fmt.Println("     키 생성: openssl rand -hex 32")
			fmt.Println("     (이 키는 Host Helm Install 시 K_O11Y_ENCRYPTION_KEY에도 동일하게 사용됩니다)")
			fmt.Println()
		}

		if dryRun {
			fmt.Println("[DRY-RUN] 실제 설치를 수행하지 않습니다.")
			fmt.Println()
			fmt.Println("수행할 작업:")
			fmt.Println("  1. Keeper VM 연결 테스트")
			fmt.Println("  2. Keeper 설치 (패키지 + 설정 + 서비스)")
			fmt.Println("  3. ClickHouse VM 연결 테스트")
			fmt.Println("  4. ClickHouse 설치 (패키지 + 설정 + S3 준비 + 서비스)")
			return nil
		}

		confirmOrExit("설치를 진행합니까?")

		// ============================
		// Step 1: Keeper 설치
		// ============================
		fmt.Println()
		fmt.Println("[1/2] Keeper 설치 중...")

		keeperSSHCfg := &sshpkg.Config{
			Mode:        mode,
			Host:        keeperHost,
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

		keeperExec, err := sshpkg.NewExecutor(keeperSSHCfg)
		if err != nil {
			return fmt.Errorf("Keeper VM 연결 실패: %w", err)
		}
		defer keeperExec.Close()
		fmt.Println("  ✓ Keeper VM 연결 성공")

		keeperCfg := installer.DefaultKeeperConfig()
		keeperCfg.Host = keeperHost
		keeperCfg.ClusterHost = keeperClusterHost
		keeperCfg.Hostname = keeperHostname
		keeperCfg.Version = keeperVersion

		if err := installer.InstallKeeper(keeperExec, keeperCfg, verbose); err != nil {
			return fmt.Errorf("Keeper 설치 실패: %w", err)
		}

		// ============================
		// Step 2: ClickHouse 설치
		// ============================
		fmt.Println()
		fmt.Println("[2/2] ClickHouse 설치 중...")

		chSSHCfg := &sshpkg.Config{
			Mode:        mode,
			Host:        clickhouseHost,
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

		chExec, err := sshpkg.NewExecutor(chSSHCfg)
		if err != nil {
			return fmt.Errorf("ClickHouse VM 연결 실패: %w", err)
		}
		defer chExec.Close()
		fmt.Println("  ✓ ClickHouse VM 연결 성공")

		chCfg := installer.DefaultClickHouseConfig()
		chCfg.Host = clickhouseHost
		chCfg.ClusterHost = chClusterHost
		chCfg.Hostname = clickhouseHostname
		chCfg.Version = clickhouseVersion
		chCfg.Password = clickhousePass
		chCfg.ClusterName = clusterName
		chCfg.KeeperHost = keeperClusterHost
		if chCfg.KeeperHost == "" {
			chCfg.KeeperHost = keeperHost
		}
		chCfg.SSHPassword = sshPass

		_, err = installer.InstallClickHouse(chExec, chCfg, verbose)
		if err != nil {
			return fmt.Errorf("ClickHouse 설치 실패: %w", err)
		}

		// ============================
		// Step 3: 에이전트 배포 (--encryption-key가 있을 때만)
		// ============================
		if installEncKey != "" {
			fmt.Println()
			fmt.Println("[3/3] 에이전트 배포 중...")

			agentCfg := &installer.AgentConfig{
				ClickHousePassword: clickhousePass,
				EncryptionKey:      installEncKey,
				BinaryPath:         agentBinary,
			}

			if err := installer.InstallAgent(chExec, agentCfg, verbose); err != nil {
				return fmt.Errorf("에이전트 배포 실패: %w", err)
			}
		}

		// ============================
		// 완료 요약
		// ============================
		fmt.Println()
		fmt.Println("==========================================")
		fmt.Println("         설치 완료")
		fmt.Println("==========================================")
		fmt.Printf("  Keeper:     %s\n", keeperHost)
		fmt.Printf("  ClickHouse: %s\n", clickhouseHost)
		if installEncKey != "" {
			fmt.Printf("  Agent:      active (systemd)\n")
		}
		fmt.Println("==========================================")
		fmt.Println()
		fmt.Println("  다음 단계:")
		fmt.Println()
		if installEncKey == "" {
			fmt.Println("  1. Host Helm Install")
			fmt.Println("  2. Post-Install (DDL 적용)")
			fmt.Println("  3. Agent Helm Install")
			fmt.Println()
			fmt.Println("  💡 --encryption-key를 추가하면 에이전트도 함께 설치됩니다.")
			fmt.Println("     키 생성: openssl rand -hex 32")
		} else {
			fmt.Println("  1. TLS 설정 (필요 시)")
			fmt.Println("     k-o11y-tls setup --mode selfsigned --domain otel.example.com")
			fmt.Println()
			fmt.Println("  2. Host Helm Install")
			fmt.Println("  3. Post-Install (DDL 적용)")
			fmt.Println("  4. Agent Helm Install")
		}

		return nil
	},
}

func init() {
	installCmd.Flags().StringVar(&keeperHost, "keeper-host", "", "Keeper 서버 IP")
	installCmd.Flags().StringVar(&clickhouseHost, "clickhouse-host", "", "ClickHouse 서버 IP")
	installCmd.Flags().StringVar(&clickhousePass, "clickhouse-password", "", "ClickHouse default 유저 비밀번호")
	installCmd.Flags().StringVar(&keeperHostname, "keeper-hostname", "keeper-1", "Keeper 노드 이름")
	installCmd.Flags().StringVar(&clickhouseHostname, "clickhouse-hostname", "clickhouse-1", "ClickHouse replica 이름")
	installCmd.Flags().StringVar(&clickhouseVersion, "clickhouse-version", "24.1.8.22", "ClickHouse 버전")
	installCmd.Flags().StringVar(&keeperVersion, "keeper-version", "24.1.8.22", "Keeper 버전")
	installCmd.Flags().StringVar(&clusterName, "cluster-name", "k_o11y_cluster", "클러스터 이름")
	installCmd.Flags().StringVar(&keeperClusterHost, "keeper-cluster-host", "", "Keeper 내부 통신 IP")
	installCmd.Flags().StringVar(&chClusterHost, "clickhouse-cluster-host", "", "ClickHouse 내부 통신 IP")
	installCmd.Flags().StringVar(&installEncKey, "encryption-key", "", "K_O11Y_ENCRYPTION_KEY (에이전트 배포 시 필수, 64자 hex)")
	installCmd.Flags().StringVar(&agentBinary, "agent-binary", "", "에이전트 linux 바이너리 경로 (미지정 시 dist/linux-amd64/ 자동 탐색)")
}
