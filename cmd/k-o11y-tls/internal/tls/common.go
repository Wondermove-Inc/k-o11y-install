package tls

import (
	"bytes"
	"fmt"
	"text/template"

	embedpkg "github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-tls/internal/embed"
	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-tls/internal/kube"
	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-tls/internal/logger"
)

// Config는 TLS 설정에 필요한 모든 파라미터를 담습니다.
type Config struct {
	Mode               string
	Domain             string
	Email              string
	CertFile           string
	KeyFile            string
	CAFile             string
	CACertFile         string
	CAKeyFile          string
	Namespace          string
	SecretName         string
	DNSProvider        string
	CertManagerVersion string
	Kube               *kube.KubeRunner
}

// EnsureNamespace는 K8s 네임스페이스가 존재하는지 확인하고, 없으면 생성합니다.
func EnsureNamespace(cfg *Config) error {
	logger.Info("네임스페이스 확인: %s", cfg.Namespace)

	_, err := cfg.Kube.Kubectl("get", "namespace", cfg.Namespace)
	if err != nil {
		logger.Info("네임스페이스 생성: %s", cfg.Namespace)
		if _, err := cfg.Kube.Kubectl("create", "namespace", cfg.Namespace); err != nil {
			return fmt.Errorf("네임스페이스 생성 실패: %w", err)
		}
	}

	logger.OK("네임스페이스 준비 완료")
	return nil
}

// WaitForCertificate는 cert-manager Certificate가 Ready 상태가 될 때까지 대기합니다.
func WaitForCertificate(cfg *Config, certName, timeout string) error {
	logger.Info("인증서 발급 대기 중...")

	_, err := cfg.Kube.Kubectl("-n", cfg.Namespace, "wait",
		"--for=condition=ready", "certificate", certName,
		"--timeout="+timeout)
	if err != nil {
		return fmt.Errorf("인증서 발급 대기 실패: %w", err)
	}

	return nil
}

// RenderTemplate는 embed된 YAML 템플릿을 렌더링합니다.
func RenderTemplate(name string, data interface{}) (string, error) {
	raw, err := embedpkg.ReadTemplate(name)
	if err != nil {
		return "", err
	}

	tmpl, err := template.New(name).Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("템플릿 파싱 실패 (%s): %w", name, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("템플릿 렌더링 실패 (%s): %w", name, err)
	}

	return buf.String(), nil
}

// ApplyTemplate는 YAML 템플릿을 렌더링하고 kubectl apply합니다.
func ApplyTemplate(cfg *Config, templateName string, data interface{}) error {
	yaml, err := RenderTemplate(templateName, data)
	if err != nil {
		return err
	}
	return cfg.Kube.KubectlApplyStdin(yaml)
}
