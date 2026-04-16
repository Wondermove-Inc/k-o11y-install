package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/installer"
	sshpkg "github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/ssh"
)

var (
	uninstallKeeperHost     string
	uninstallClickhouseHost string
	keepData                bool
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Keeper + ClickHouse 전체 삭제",
	Long:  "SSH로 VM에 접속하여 ClickHouse Keeper와 ClickHouse Server를 삭제합니다.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if uninstallKeeperHost == "" || uninstallClickhouseHost == "" {
			fmt.Println("필수 인자가 누락되었습니다:")
			if uninstallClickhouseHost == "" {
				fmt.Println("  --clickhouse-host")
			}
			if uninstallKeeperHost == "" {
				fmt.Println("  --keeper-host")
			}
			os.Exit(1)
		}

		fmt.Println("==========================================")
		fmt.Println("  K-O11y DB 삭제")
		fmt.Println("==========================================")
		fmt.Printf("  Mode:            %s\n", mode)
		fmt.Printf("  Keeper Host:     %s\n", uninstallKeeperHost)
		fmt.Printf("  ClickHouse Host: %s\n", uninstallClickhouseHost)
		fmt.Printf("  데이터 유지:     %v\n", keepData)
		fmt.Println("==========================================")

		if dryRun {
			fmt.Println("[DRY-RUN] 실제 삭제를 수행하지 않습니다.")
			fmt.Println()
			fmt.Println("수행할 작업:")
			fmt.Println("  1. ClickHouse 서비스 중지 + 패키지 제거 + S3 정리")
			fmt.Println("  2. Keeper 서비스 중지 + 패키지 제거")
			if !keepData {
				fmt.Println("  3. 데이터 디렉토리 삭제")
			}
			return nil
		}

		confirmOrExit("정말 삭제합니까? 모든 데이터가 삭제됩니다.")

		uninstallCfg := &installer.UninstallConfig{
			KeepData: keepData,
		}

		// ============================
		// Step 1: ClickHouse 삭제
		// ============================
		fmt.Println()
		fmt.Println("[1/2] ClickHouse 삭제 중...")

		chSSHCfg := &sshpkg.Config{
			Mode:        mode,
			Host:        uninstallClickhouseHost,
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

		if err := installer.UninstallClickHouse(chExec, uninstallCfg, verbose); err != nil {
			return fmt.Errorf("ClickHouse 삭제 실패: %w", err)
		}

		// ============================
		// Step 2: Keeper 삭제
		// ============================
		fmt.Println()
		fmt.Println("[2/2] Keeper 삭제 중...")

		keeperSSHCfg := &sshpkg.Config{
			Mode:        mode,
			Host:        uninstallKeeperHost,
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

		if err := installer.UninstallKeeper(keeperExec, uninstallCfg, verbose); err != nil {
			return fmt.Errorf("Keeper 삭제 실패: %w", err)
		}

		// ============================
		// 완료
		// ============================
		fmt.Println()
		fmt.Println("==========================================")
		fmt.Println("  삭제 완료!")
		fmt.Println("==========================================")

		return nil
	},
}

func init() {
	uninstallCmd.Flags().StringVar(&uninstallKeeperHost, "keeper-host", "", "Keeper 서버 IP")
	uninstallCmd.Flags().StringVar(&uninstallClickhouseHost, "clickhouse-host", "", "ClickHouse 서버 IP")
	uninstallCmd.Flags().BoolVar(&keepData, "keep-data", false, "데이터 디렉토리 보존")
}
