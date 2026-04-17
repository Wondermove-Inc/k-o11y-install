package tls

import (
	"bytes"
	"fmt"
	"text/template"

	embedpkg "github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-tls/internal/embed"
	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-tls/internal/kube"
	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-tls/internal/logger"
)

// Config holds all parameters required for TLS setup.
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

// EnsureNamespace checks whether the K8s namespace exists and creates it if needed.
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

// WaitForCertificate waits until the cert-manager Certificate becomes Ready.
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

// RenderTemplate renders an embedded YAML template.
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

// ApplyTemplate renders a YAML template and applies it with kubectl.
func ApplyTemplate(cfg *Config, templateName string, data interface{}) error {
	yaml, err := RenderTemplate(templateName, data)
	if err != nil {
		return err
	}
	return cfg.Kube.KubectlApplyStdin(yaml)
}
