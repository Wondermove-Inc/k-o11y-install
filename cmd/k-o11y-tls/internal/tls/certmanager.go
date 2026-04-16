package tls

import (
	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-tls/internal/logger"
)

// InstallCertManager는 cert-manager가 설치되어 있지 않으면 설치합니다.
// CRD 존재 여부로 판단하므로 다른 네임스페이스에 설치된 경우도 감지합니다.
func InstallCertManager(cfg *Config) error {
	// CRD 존재 여부로 cert-manager 설치 확인 (네임스페이스 무관)
	_, err := cfg.Kube.Kubectl("get", "crd", "certificates.cert-manager.io")
	if err == nil {
		logger.OK("cert-manager 이미 설치됨")
		return nil
	}

	logger.Info("cert-manager %s 설치 중...", cfg.CertManagerVersion)

	// helm repo 추가
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

	// Pod Ready 대기
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
