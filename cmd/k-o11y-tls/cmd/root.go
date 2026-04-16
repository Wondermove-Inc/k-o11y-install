package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	namespace   string
	secretName  string
	kubeContext string
	verbose     bool
	dryRun      bool
	yes         bool
)

var rootCmd = &cobra.Command{
	Use:   "k-o11y-tls",
	Short: "OTel Collector TLS 인증서 설정 도구",
	Long: `Host OTel Collector의 TLS 인증서를 설정하는 단일 바이너리 도구입니다.

서브커맨드:
  setup    TLS 인증서 설정 (existing/selfsigned/private-ca/letsencrypt)`,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&namespace, "namespace", "k-o11y", "K8s 네임스페이스")
	rootCmd.PersistentFlags().StringVar(&secretName, "secret-name", "otel-collector-tls", "TLS Secret 이름")
	rootCmd.PersistentFlags().StringVar(&kubeContext, "kube-context", "", "K8s 컨텍스트")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "상세 로그 출력")
	rootCmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "실제 실행 없이 명령어만 출력")
	rootCmd.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "확인 프롬프트 생략")

	rootCmd.AddCommand(setupCmd)
}

func Execute() error {
	return rootCmd.Execute()
}

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
