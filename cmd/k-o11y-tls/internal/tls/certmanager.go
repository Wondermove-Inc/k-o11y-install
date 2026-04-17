package tls

import (
	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-tls/internal/logger"
)

// InstallCertManager installs cert-manager if it is not already present.
// It checks for the CRD, so it also detects installations in other namespaces.
func InstallCertManager(cfg *Config) error {
	// Check whether cert-manager is installed by looking for the CRD
	_, err := cfg.Kube.Kubectl("get", "crd", "certificates.cert-manager.io")
	if err == nil {
		logger.OK("cert-manager 이미 설치됨")
		return nil
	}

	logger.Info("cert-manager %s 설치 중...", cfg.CertManagerVersion)

	// Add the Helm repository
	cfg.Kube.Helm("repo", "add", "jetstack", "https://charts.jetstack.io")
	cfg.Kube.Helm("repo", "update", "jetstack")

	// helm install
	_, err = cfg.Kube.Helm("install", "cert-manager", "jetstack/cert-manager",
		"--namespace", "cert-manager",
		"--create-namespace",
		"--version", cfg.CertManagerVersion,
		"--set", "crds.enabled=true",
		"--wait", "--timeout", "5m")
	if err != nil {
		return err
	}

	logger.OK("cert-manager 설치 완료")

	// Wait for the Pods to become Ready
	logger.Info("cert-manager Pod 준비 대기 중...")
	_, err = cfg.Kube.Kubectl("-n", "cert-manager", "wait",
		"--for=condition=ready", "pod",
		"-l", "app.kubernetes.io/instance=cert-manager",
		"--timeout=120s")
	if err != nil {
		return err
	}

	logger.OK("cert-manager 준비 완료")
	return nil
}
