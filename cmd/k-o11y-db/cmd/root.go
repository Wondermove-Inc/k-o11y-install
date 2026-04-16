package cmd

import (
	"fmt"
	"os"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/agent"

	"github.com/spf13/cobra"
)

var (
	// 공통 플래그
	mode     string
	sshUser  string
	sshKey   string
	sshPass  string
	sshPort  int
	verbose  bool
	dryRun   bool
	yes      bool

	// Bastion 플래그
	bastionHost string
	bastionUser string
	bastionKey  string
	bastionPort int
)

var rootCmd = &cobra.Command{
	Use:     "k-o11y-db",
	Short:   "K-O11y DB 설치 + 에이전트 도구",
	Version: agent.Version,
	Long: `ClickHouse + Keeper 설치, DDL 적용, 삭제를 수행하고,
VM 상주 에이전트로 운영 작업을 자율 수행하는 단일 바이너리 도구입니다.

서브커맨드:
  install       Keeper + ClickHouse 설치
  post-install  DDL + 메타데이터 테이블 적용
  uninstall     Keeper + ClickHouse 전체 삭제
  agent         CH VM 경량 에이전트 (start/status)`,
}

func init() {
	// 공통 SSH 플래그
	rootCmd.PersistentFlags().StringVar(&mode, "mode", "ssh", "접속 모드 (ssh|bastion|local)")
	rootCmd.PersistentFlags().StringVar(&sshUser, "ssh-user", "ubuntu", "SSH 사용자")
	rootCmd.PersistentFlags().StringVar(&sshKey, "ssh-key", "", "SSH 개인키 경로")
	rootCmd.PersistentFlags().StringVar(&sshPass, "ssh-password", "", "SSH/sudo 비밀번호")
	rootCmd.PersistentFlags().IntVar(&sshPort, "ssh-port", 22, "SSH 포트")

	// Bastion 플래그
	rootCmd.PersistentFlags().StringVar(&bastionHost, "bastion-host", "", "Bastion 호스트 IP")
	rootCmd.PersistentFlags().StringVar(&bastionUser, "bastion-user", "ubuntu", "Bastion SSH 사용자")
	rootCmd.PersistentFlags().StringVar(&bastionKey, "bastion-key", "", "Bastion SSH 키 경로")
	rootCmd.PersistentFlags().IntVar(&bastionPort, "bastion-port", 22, "Bastion SSH 포트")

	// 실행 옵션
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "상세 로그 출력")
	rootCmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "실제 실행 없이 명령어만 출력")
	rootCmd.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "확인 프롬프트 생략")

	// 서브커맨드 등록
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(postInstallCmd)
	rootCmd.AddCommand(uninstallCmd)
	rootCmd.AddCommand(upgradeCmd)
	rootCmd.AddCommand(agentCmd)
}

func Execute() error {
	return rootCmd.Execute()
}

// getSSHConfig는 CLI 플래그에서 SSH 설정을 구성합니다.
// 필수 플래그가 비어있으면 대화형 프롬프트로 입력받습니다.
func getSSHConfig() map[string]interface{} {
	return map[string]interface{}{
		"mode":         mode,
		"ssh_user":     sshUser,
		"ssh_key":      sshKey,
		"ssh_password": sshPass,
		"ssh_port":     sshPort,
		"bastion_host": bastionHost,
		"bastion_user": bastionUser,
		"bastion_key":  bastionKey,
		"bastion_port": bastionPort,
		"verbose":      verbose,
		"dry_run":      dryRun,
	}
}

// confirmOrExit는 --yes 플래그가 없으면 사용자 확인을 요청합니다.
func confirmOrExit(msg string) {
	if yes {
		return
	}
	fmt.Printf("%s (y/n): ", msg)
	var answer string
	fmt.Scanln(&answer)
	if answer != "y" && answer != "Y" {
		fmt.Println("취소됨")
		os.Exit(0)
	}
}
