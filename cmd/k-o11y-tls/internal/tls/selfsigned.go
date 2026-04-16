package tls

import (
	"fmt"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-tls/internal/logger"
)

// SetupSelfsigned는 cert-manager로 self-signed 인증서를 생성합니다.
func SetupSelfsigned(cfg *Config) error {
	if cfg.Domain == "" {
		return fmt.Errorf("--domain은 selfsigned 모드에서 필수입니다")
	}

	if err := EnsureNamespace(cfg); err != nil {
		return err
	}
	if err := InstallCertManager(cfg); err != nil {
		return err
	}

	data := map[string]string{
		"Namespace":  cfg.Namespace,
		"SecretName": cfg.SecretName,
		"Domain":     cfg.Domain,
	}

	logger.Info("Self-signed Issuer 생성 중...")
	if err := ApplyTemplate(cfg, "selfsigned-issuer.yaml", data); err != nil {
		return err
	}

	logger.Info("Certificate 리소스 생성 중 (도메인: %s)...", cfg.Domain)
	if err := ApplyTemplate(cfg, "selfsigned-certificate.yaml", data); err != nil {
		return err
	}

	if err := WaitForCertificate(cfg, "otel-collector-cert", "120s"); err != nil {
		return err
	}

	logger.OK("Self-signed 인증서 발급 완료")
	return nil
}
