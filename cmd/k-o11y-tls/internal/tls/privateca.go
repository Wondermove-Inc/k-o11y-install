package tls

import (
	"fmt"
	"os"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-tls/internal/logger"
)

// SetupPrivateCA는 사내 CA 인증서로 cert-manager 인증서를 생성합니다.
func SetupPrivateCA(cfg *Config) error {
	if cfg.Domain == "" {
		return fmt.Errorf("--domain은 private-ca 모드에서 필수입니다")
	}
	if cfg.CACertFile == "" {
		return fmt.Errorf("--ca-cert는 private-ca 모드에서 필수입니다")
	}
	if cfg.CAKeyFile == "" {
		return fmt.Errorf("--ca-key는 private-ca 모드에서 필수입니다")
	}
	if _, err := os.Stat(cfg.CACertFile); os.IsNotExist(err) {
		return fmt.Errorf("CA 인증서 파일을 찾을 수 없습니다: %s", cfg.CACertFile)
	}
	if _, err := os.Stat(cfg.CAKeyFile); os.IsNotExist(err) {
		return fmt.Errorf("CA 개인키 파일을 찾을 수 없습니다: %s", cfg.CAKeyFile)
	}

	if err := EnsureNamespace(cfg); err != nil {
		return err
	}
	if err := InstallCertManager(cfg); err != nil {
		return err
	}

	// CA 인증서를 K8s Secret으로 등록
	caSecretName := "otel-private-ca-keypair"
	logger.Info("사내 CA 인증서를 K8s Secret으로 등록 중...")
	cfg.Kube.Kubectl("-n", cfg.Namespace, "delete", "secret", caSecretName)
	_, err := cfg.Kube.Kubectl("-n", cfg.Namespace, "create", "secret", "tls", caSecretName,
		"--cert="+cfg.CACertFile, "--key="+cfg.CAKeyFile)
	if err != nil {
		return fmt.Errorf("CA Secret 생성 실패: %w", err)
	}

	data := map[string]string{
		"Namespace":  cfg.Namespace,
		"SecretName": cfg.SecretName,
		"Domain":     cfg.Domain,
	}

	logger.Info("사내 CA Issuer 생성 중...")
	if err := ApplyTemplate(cfg, "privateca-issuer.yaml", data); err != nil {
		return err
	}

	logger.Info("Certificate 리소스 생성 중 (도메인: %s)...", cfg.Domain)
	if err := ApplyTemplate(cfg, "privateca-certificate.yaml", data); err != nil {
		return err
	}

	if err := WaitForCertificate(cfg, "otel-collector-cert", "120s"); err != nil {
		return err
	}

	logger.OK("사내 CA 인증서 발급 완료")
	fmt.Println()
	logger.Warn("Agent에 사내 CA 인증서를 배포해야 합니다:")
	logger.Warn("  kubectl create secret generic otel-agent-tls --from-file=ca.pem=%s -n %s", cfg.CACertFile, cfg.Namespace)
	return nil
}
