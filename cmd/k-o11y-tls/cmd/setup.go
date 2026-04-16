package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-tls/internal/kube"
	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-tls/internal/tls"
)

var (
	mode              string
	domain            string
	email             string
	certFile          string
	keyFile           string
	caFile            string
	caCertFile        string
	caKeyFile         string
	dnsProvider       string
	certManagerVer    string
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "TLS 인증서 설정",
	Long: `OTel Collector TLS 인증서를 설정합니다.

모드:
  existing     고객 인증서 직접 사용 (cert-manager 불필요)
  selfsigned   self-signed 인증서 자동 생성 (테스트/PoC용)
  private-ca   사내 CA 인증서로 자동 발급+갱신
  letsencrypt  Let's Encrypt 자동 발급+갱신 (퍼블릭 도메인)

예시:
  # 고객 인증서
  k-o11y-tls setup --mode existing --cert ./tls.crt --key ./tls.key

  # self-signed (테스트용)
  k-o11y-tls setup --mode selfsigned --domain otel.example.com

  # 사내 CA
  k-o11y-tls setup --mode private-ca --domain otel.internal.com --ca-cert ./ca.crt --ca-key ./ca.key

  # Let's Encrypt
  k-o11y-tls setup --mode letsencrypt --domain otel.example.com --email admin@example.com`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if mode == "" {
			return fmt.Errorf("--mode는 필수입니다 (existing, selfsigned, private-ca, letsencrypt)")
		}

		// 배너 출력
		fmt.Println()
		fmt.Println("============================================")
		fmt.Println("  OTel Collector TLS 인증서 설정")
		fmt.Println("============================================")
		fmt.Printf("  모드:       %s\n", mode)
		fmt.Printf("  네임스페이스: %s\n", namespace)
		fmt.Printf("  Secret:     %s\n", secretName)
		if domain != "" {
			fmt.Printf("  도메인:      %s\n", domain)
		}
		fmt.Println("============================================")
		fmt.Println()

		if dryRun {
			fmt.Println("[DRY-RUN] 실제 실행하지 않고 명령어만 출력합니다.")
			fmt.Println()
		}

		if !dryRun {
			confirmOrExit("TLS 설정을 진행합니까?")
		}

		cfg := &tls.Config{
			Mode:               mode,
			Domain:             domain,
			Email:              email,
			CertFile:           certFile,
			KeyFile:            keyFile,
			CAFile:             caFile,
			CACertFile:         caCertFile,
			CAKeyFile:          caKeyFile,
			Namespace:          namespace,
			SecretName:         secretName,
			DNSProvider:        dnsProvider,
			CertManagerVersion: certManagerVer,
			Kube: &kube.KubeRunner{
				Context: kubeContext,
				Verbose: verbose,
				DryRun:  dryRun,
			},
		}

		var err error
		switch mode {
		case "existing":
			err = tls.SetupExisting(cfg)
		case "selfsigned":
			err = tls.SetupSelfsigned(cfg)
		case "private-ca":
			err = tls.SetupPrivateCA(cfg)
		case "letsencrypt":
			err = tls.SetupLetsEncrypt(cfg)
		default:
			return fmt.Errorf("알 수 없는 모드: %s (existing, selfsigned, private-ca, letsencrypt)", mode)
		}

		if err != nil {
			return err
		}

		// 다음 단계 안내
		printNextSteps(mode)
		return nil
	},
}

func init() {
	setupCmd.Flags().StringVar(&mode, "mode", "", "인증서 모드 (existing|selfsigned|private-ca|letsencrypt)")
	setupCmd.Flags().StringVar(&domain, "domain", "", "도메인 (selfsigned, private-ca, letsencrypt 필수)")
	setupCmd.Flags().StringVar(&email, "email", "", "이메일 (letsencrypt 필수)")
	setupCmd.Flags().StringVar(&certFile, "cert", "", "인증서 파일 경로 (existing 필수)")
	setupCmd.Flags().StringVar(&keyFile, "key", "", "개인키 파일 경로 (existing 필수)")
	setupCmd.Flags().StringVar(&caFile, "ca", "", "CA 인증서 파일 (existing 선택)")
	setupCmd.Flags().StringVar(&caCertFile, "ca-cert", "", "사내 CA 인증서 파일 (private-ca 필수)")
	setupCmd.Flags().StringVar(&caKeyFile, "ca-key", "", "사내 CA 개인키 파일 (private-ca 필수)")
	setupCmd.Flags().StringVar(&dnsProvider, "dns-provider", "route53", "DNS 제공자 (letsencrypt, route53|cloudflare)")
	setupCmd.Flags().StringVar(&certManagerVer, "cert-manager-version", "v1.17.1", "cert-manager 버전")
}

func printNextSteps(mode string) {
	fmt.Println()
	fmt.Println("============================================")
	fmt.Println("  다음 단계")
	fmt.Println("============================================")
	fmt.Println()
	fmt.Printf("  Secret 확인:\n")
	fmt.Printf("    kubectl -n %s get secret %s\n", namespace, secretName)
	fmt.Println()
	fmt.Println("  Host Helm 설치 (TLS 활성화):")
	fmt.Println("    helm upgrade --install k-o11y-host \\")
	fmt.Println("      oci://<YOUR_REGISTRY>/charts/k-o11y-host \\")
	fmt.Printf("      --namespace %s \\\n", namespace)
	fmt.Println("      --set otelCollector.tls.enabled=true \\")
	fmt.Printf("      --set otelCollector.tls.existingSecretName=%s \\\n", secretName)
	fmt.Println("      --set otelCollector.tls.path=/etc/otel/tls \\")
	fmt.Println("      ... (기존 옵션)")
	fmt.Println()

	if mode == "selfsigned" {
		fmt.Println("  Agent 설치 시 insecureSkipVerify 필요 (self-signed):")
		fmt.Println("    --set k-o11y-otel-agent.otelInsecure=false")
		fmt.Println("    --set k-o11y-otel-agent.insecureSkipVerify=true")
		fmt.Println()
	}

	fmt.Println("[OK] TLS 설정 완료!")
}
