package tls

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-tls/internal/logger"
)

// SetupExisting은 고객이 직접 준비한 인증서로 K8s Secret을 생성합니다.
func SetupExisting(cfg *Config) error {
	// 검증
	if cfg.CertFile == "" {
		return fmt.Errorf("--cert는 existing 모드에서 필수입니다")
	}
	if cfg.KeyFile == "" {
		return fmt.Errorf("--key는 existing 모드에서 필수입니다")
	}
	if _, err := os.Stat(cfg.CertFile); os.IsNotExist(err) {
		return fmt.Errorf("인증서 파일을 찾을 수 없습니다: %s", cfg.CertFile)
	}
	if _, err := os.Stat(cfg.KeyFile); os.IsNotExist(err) {
		return fmt.Errorf("개인키 파일을 찾을 수 없습니다: %s", cfg.KeyFile)
	}

	if err := EnsureNamespace(cfg); err != nil {
		return err
	}

	logger.Info("고객 인증서로 Secret 생성: %s", cfg.SecretName)

	// 기존 Secret 삭제 (있으면)
	cfg.Kube.Kubectl("-n", cfg.Namespace, "delete", "secret", cfg.SecretName)

	// TLS Secret 생성
	_, err := cfg.Kube.Kubectl("-n", cfg.Namespace, "create", "secret", "tls", cfg.SecretName,
		"--cert="+cfg.CertFile, "--key="+cfg.KeyFile)
	if err != nil {
		return fmt.Errorf("Secret 생성 실패: %w", err)
	}

	// CA 인증서 추가 (선택)
	if cfg.CAFile != "" {
		if _, err := os.Stat(cfg.CAFile); os.IsNotExist(err) {
			return fmt.Errorf("CA 인증서 파일을 찾을 수 없습니다: %s", cfg.CAFile)
		}

		logger.Info("CA 인증서 추가 중...")
		caData, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return fmt.Errorf("CA 파일 읽기 실패: %w", err)
		}
		caB64 := base64.StdEncoding.EncodeToString(caData)
		patchJSON := fmt.Sprintf(`{"data":{"ca.crt":"%s"}}`, caB64)

		_, err = cfg.Kube.Kubectl("-n", cfg.Namespace, "patch", "secret", cfg.SecretName, "-p", patchJSON)
		if err != nil {
			return fmt.Errorf("CA 인증서 추가 실패: %w", err)
		}
		logger.OK("CA 인증서 추가 완료")
	}

	logger.OK("Secret 생성 완료: %s", cfg.SecretName)
	return nil
}
