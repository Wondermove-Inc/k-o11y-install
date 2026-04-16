package tls

import (
	"fmt"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-tls/internal/logger"
)

// SetupLetsEncrypt는 Let's Encrypt로 자동 인증서를 생성합니다.
func SetupLetsEncrypt(cfg *Config) error {
	if cfg.Domain == "" {
		return fmt.Errorf("--domain은 letsencrypt 모드에서 필수입니다")
	}
	if cfg.Email == "" {
		return fmt.Errorf("--email은 letsencrypt 모드에서 필수입니다")
	}
	if cfg.DNSProvider != "route53" && cfg.DNSProvider != "cloudflare" {
		return fmt.Errorf("지원하지 않는 DNS 제공자: %s (route53, cloudflare 지원)", cfg.DNSProvider)
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
		"Email":      cfg.Email,
	}

	// DNS 제공자별 ClusterIssuer 생성
	logger.Info("Let's Encrypt ClusterIssuer 생성 중...")
	var issuerTemplate string
	switch cfg.DNSProvider {
	case "route53":
		issuerTemplate = "letsencrypt-clusterissuer-route53.yaml"
	case "cloudflare":
		issuerTemplate = "letsencrypt-clusterissuer-cloudflare.yaml"
	}

	if err := ApplyTemplate(cfg, issuerTemplate, data); err != nil {
		return err
	}

	// DNS 제공자별 안내
	switch cfg.DNSProvider {
	case "route53":
		logger.Warn("Route53 IAM 권한 필요: cert-manager ServiceAccount에 Route53 접근 권한을 부여하세요.")
		logger.Warn("참고: https://cert-manager.io/docs/configuration/acme/dns01/route53/")
	case "cloudflare":
		logger.Warn("Cloudflare API Token Secret 필요:")
		logger.Warn("  kubectl -n cert-manager create secret generic cloudflare-api-token --from-literal=api-token=<YOUR_TOKEN>")
	}

	// Certificate 생성
	logger.Info("Certificate 리소스 생성 중 (도메인: %s)...", cfg.Domain)
	if err := ApplyTemplate(cfg, "letsencrypt-certificate.yaml", data); err != nil {
		return err
	}

	// 인증서 대기 (300초, DNS 검증에 시간 소요)
	logger.Info("인증서 발급 대기 중 (DNS 검증에 1-5분 소요)...")
	if err := WaitForCertificate(cfg, "otel-collector-cert", "300s"); err != nil {
		// Let's Encrypt는 soft failure (DNS 검증 지연)
		logger.Warn("인증서 발급이 아직 진행 중입니다. 상태 확인:")
		logger.Warn("  kubectl -n %s describe certificate otel-collector-cert", cfg.Namespace)
		logger.Warn("  kubectl -n %s describe certificaterequest", cfg.Namespace)
		logger.Warn("DNS 제공자 권한 설정을 확인하세요.")
		return nil // soft failure
	}

	logger.OK("Let's Encrypt 인증서 발급 완료")
	return nil
}
