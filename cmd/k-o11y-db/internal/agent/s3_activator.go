package agent

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"time"
)

const (
	storageXMLPath    = "/etc/clickhouse-server/config.d/storage.xml"
	s3EnvPath         = "/etc/clickhouse-server/s3.env"
	backupConfigPath  = "/etc/clickhouse-backup/config.yml"
	maxRestartWait    = 180 * time.Second
	restartPollPeriod = 5 * time.Second
	maxRetries        = 3
)

// storageXMLTemplate는 S3 storage.xml의 from_env 패턴 템플릿입니다.
// activate.sh와 동일한 구조를 유지합니다.
const storageXMLTemplate = `<?xml version="1.0"?>
<clickhouse>
    <storage_configuration>
        <disks>
            <s3>
                <type>s3</type>
                <endpoint from_env="S3_ENDPOINT"/>
                <access_key_id from_env="AWS_ACCESS_KEY_ID"/>
                <secret_access_key from_env="AWS_SECRET_ACCESS_KEY"/>
            </s3>
        </disks>
        <policies>
            <tiered>
                <volumes>
                    <default><disk>default</disk></default>
                    <s3><disk>s3</disk></s3>
                </volumes>
            </tiered>
        </policies>
    </storage_configuration>
</clickhouse>
`

// backupConfigTemplate는 clickhouse-backup config.yml 템플릿입니다.
const backupConfigTemplate = `general:
  remote_storage: s3
  disable_progress_bar: true

clickhouse:
  host: localhost
  port: 9000
  password: "{{.CHPassword}}"

s3:
  bucket: "{{.Bucket}}"
  region: "{{.Region}}"
  endpoint: "https://s3.{{.Region}}.amazonaws.com"
  force_path_style: true
  path: "backup/"
  storage_class: GLACIER_IR
  object_disk_path: "data/"
  access_key: "{{.AccessKey}}"
  secret_key: "{{.SecretKey}}"
`

// S3Credentials는 복호화된 S3 인증 정보입니다.
type S3Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	Region          string
	Endpoint        string
}

// S3Activator는 S3 스토리지 활성화/비활성화를 수행합니다.
type S3Activator struct {
	db     *sql.DB
	daemon *Daemon
}

// NewS3Activator는 새 S3Activator를 생성합니다.
func NewS3Activator(db *sql.DB, daemon *Daemon) *S3Activator {
	return &S3Activator{db: db, daemon: daemon}
}

// Activate는 S3 스토리지를 활성화합니다.
// 1. DB에서 S3 credential 조회 + 복호화
// 2. 기존 config 백업 (.bak)
// 3. storage.xml 생성
// 4. s3.env 생성
// 5. CH 재시작
// 6. S3 disk 확인
// 7. clickhouse-backup config 업데이트
// 실패 시 .bak에서 롤백, 최대 3회 재시도.
func (a *S3Activator) Activate(ctx context.Context, cfg *S3Config) error {
	// 이미 S3 active이면 스킵 (불필요한 CH restart 방지)
	if a.isS3Active(ctx) {
		a.daemon.logJSON("info", "S3 already active, skipping activate", map[string]interface{}{
			"bucket": cfg.Bucket,
		})
		return nil
	}

	a.daemon.logJSON("info", "S3 Activate starting", map[string]interface{}{
		"bucket": cfg.Bucket,
		"region": cfg.Region,
	})

	// 1. credential 복호화
	creds, err := a.decryptCredentials(cfg)
	if err != nil {
		return fmt.Errorf("credential 복호화 실패: %w", err)
	}

	// 재시도 루프
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			a.daemon.logJSON("info", "S3 Activate retry", map[string]interface{}{
				"attempt": attempt,
			})
		}

		if err := a.doActivate(ctx, creds); err != nil {
			lastErr = err
			a.daemon.logJSON("error", "S3 Activate attempt failed", map[string]interface{}{
				"attempt": attempt,
				"error":   err.Error(),
			})

			// 롤백
			if rollbackErr := a.rollback(); rollbackErr != nil {
				a.daemon.logJSON("error", "Rollback failed", map[string]interface{}{
					"error": rollbackErr.Error(),
				})
			}
			continue
		}

		// 성공 — activate_requested=0으로 리셋
		a.resetActivateRequested(ctx, cfg)
		a.daemon.logJSON("info", "S3 Activate completed successfully", nil)
		return nil
	}

	return fmt.Errorf("S3 Activate failed after %d attempts: %w", maxRetries, lastErr)
}

// Deactivate는 S3 스토리지를 비활성화합니다.
// .bak 파일에서 복원하거나, storage.xml을 제거합니다.
func (a *S3Activator) Deactivate(ctx context.Context) error {
	a.daemon.logJSON("info", "S3 Deactivate starting", nil)

	if err := a.rollback(); err != nil {
		return fmt.Errorf("S3 Deactivate(rollback) 실패: %w", err)
	}

	// CH 재시작
	if err := a.restartClickHouse(ctx); err != nil {
		return fmt.Errorf("S3 Deactivate: CH 재시작 실패: %w", err)
	}

	a.daemon.logJSON("info", "S3 Deactivate completed", nil)
	return nil
}

// doActivate는 한 번의 activate 시도를 수행합니다.
func (a *S3Activator) doActivate(ctx context.Context, creds *S3Credentials) error {
	// 2. 기존 config 백업
	if err := backupFile(storageXMLPath); err != nil {
		a.daemon.logJSON("debug", "storage.xml backup skipped (may not exist)", map[string]interface{}{
			"error": err.Error(),
		})
	}
	if err := backupFile(s3EnvPath); err != nil {
		a.daemon.logJSON("debug", "s3.env backup skipped (may not exist)", map[string]interface{}{
			"error": err.Error(),
		})
	}

	// 3. storage.xml 생성
	if err := writeFileSecure(storageXMLPath, []byte(storageXMLTemplate), 0644, "clickhouse"); err != nil {
		return fmt.Errorf("storage.xml 생성 실패: %w", err)
	}
	a.daemon.logJSON("info", "storage.xml created", nil)

	// 4. s3.env 생성
	s3EnvContent := fmt.Sprintf("AWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\nS3_ENDPOINT=%s\n",
		creds.AccessKeyID, creds.SecretAccessKey, creds.Endpoint)
	if err := writeFileSecure(s3EnvPath, []byte(s3EnvContent), 0600, "clickhouse"); err != nil {
		return fmt.Errorf("s3.env 생성 실패: %w", err)
	}
	a.daemon.logJSON("info", "s3.env created", nil)

	// 5. CH 재시작
	if err := a.restartClickHouse(ctx); err != nil {
		return fmt.Errorf("CH 재시작 실패: %w", err)
	}

	// 6. S3 disk 확인
	if err := a.verifyS3Disk(ctx); err != nil {
		return fmt.Errorf("S3 disk 확인 실패: %w", err)
	}

	// 7. clickhouse-backup config 업데이트
	if err := a.updateBackupConfig(creds); err != nil {
		a.daemon.logJSON("warn", "clickhouse-backup config update failed (non-fatal)", map[string]interface{}{
			"error": err.Error(),
		})
	}

	return nil
}

// resetActivateRequested는 activate_requested=0으로 리셋합니다.
// Activate 완료 후 호출하여 재처리를 방지합니다.
func (a *S3Activator) resetActivateRequested(ctx context.Context, cfg *S3Config) {
	query := `INSERT INTO k_o11y.s3_config
		(config_id, auth_mode, bucket, region, endpoint, access_key_id, secret_access_key,
		 s3_enabled, activate_requested, updated_by, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 'agent', now(), ?)`

	_, err := a.db.ExecContext(ctx, query,
		cfg.ConfigID, cfg.AuthMode, cfg.Bucket, cfg.Region, cfg.Endpoint,
		cfg.AccessKeyID, cfg.SecretAccessKey, cfg.S3Enabled,
		time.Now().Unix())
	if err != nil {
		a.daemon.logJSON("warn", "Failed to reset activate_requested", map[string]interface{}{
			"error": err.Error(),
		})
	} else {
		a.daemon.logJSON("info", "activate_requested reset to 0", nil)
	}
}

// isS3Active는 system.disks에 S3 타입 디스크가 등록되어 있는지 확인합니다.
func (a *S3Activator) isS3Active(ctx context.Context) bool {
	var count int
	err := a.db.QueryRowContext(ctx, "SELECT count() FROM system.disks WHERE type='s3'").Scan(&count)
	return err == nil && count > 0
}

// decryptCredentials는 DB의 암호화된 S3 credential을 복호화합니다.
func (a *S3Activator) decryptCredentials(cfg *S3Config) (*S3Credentials, error) {
	if a.daemon.cfg.EncryptionKey == "" {
		return nil, fmt.Errorf("K_O11Y_ENCRYPTION_KEY가 설정되지 않았습니다")
	}

	accessKey, err := DecryptAESGCM(cfg.AccessKeyID, a.daemon.cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("access_key_id 복호화 실패: %w", err)
	}

	secretKey, err := DecryptAESGCM(cfg.SecretAccessKey, a.daemon.cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("secret_access_key 복호화 실패: %w", err)
	}

	// endpoint 자동 생성 (비어있으면)
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://%s.s3.%s.amazonaws.com/data/", cfg.Bucket, cfg.Region)
	}

	return &S3Credentials{
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
		Bucket:          cfg.Bucket,
		Region:          cfg.Region,
		Endpoint:        endpoint,
	}, nil
}

// restartClickHouse는 systemctl로 CH를 재시작하고 활성 상태를 기다립니다.
func (a *S3Activator) restartClickHouse(ctx context.Context) error {
	a.daemon.logJSON("info", "Restarting ClickHouse", nil)

	cmd := exec.CommandContext(ctx, "sudo", "systemctl", "restart", "clickhouse-server")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl restart 실패: %s (output: %s)", err, string(out))
	}

	// 활성 상태 대기 (5초 간격, 최대 180초)
	deadline := time.After(maxRestartWait)
	ticker := time.NewTicker(restartPollPeriod)
	defer ticker.Stop()

	// 첫 10초 대기 (restart 직후 바로 폴링하면 false positive)
	time.Sleep(10 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during CH restart wait")
		case <-deadline:
			return fmt.Errorf("CH 재시작 타임아웃 (180초)")
		case <-ticker.C:
			out, err := exec.Command("systemctl", "is-active", "clickhouse-server").Output()
			status := strings.TrimSpace(string(out))
			a.daemon.logJSON("debug", "CH restart poll", map[string]interface{}{
				"status": status,
			})
			if err == nil && status == "active" {
				a.daemon.logJSON("info", "ClickHouse restarted successfully", nil)
				return nil
			}
		}
	}
}

// verifyS3Disk는 system.disks에서 S3 타입 디스크 존재를 확인합니다.
func (a *S3Activator) verifyS3Disk(ctx context.Context) error {
	// CH 재시작 직후이므로 잠시 대기
	time.Sleep(3 * time.Second)

	for i := 0; i < 6; i++ {
		var count int
		err := a.db.QueryRowContext(ctx, "SELECT count() FROM system.disks WHERE type='s3'").Scan(&count)
		if err == nil && count > 0 {
			a.daemon.logJSON("info", "S3 disk verified", nil)
			return nil
		}
		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("S3 disk not detected after 30s")
}

// updateBackupConfig는 clickhouse-backup config.yml을 업데이트합니다.
func (a *S3Activator) updateBackupConfig(creds *S3Credentials) error {
	tmpl, err := template.New("backup").Parse(backupConfigTemplate)
	if err != nil {
		return err
	}

	data := struct {
		CHPassword string
		Bucket     string
		Region     string
		AccessKey  string
		SecretKey  string
	}{
		CHPassword: a.daemon.cfg.ClickHousePassword,
		Bucket:     creds.Bucket,
		Region:     creds.Region,
		AccessKey:  creds.AccessKeyID,
		SecretKey:  creds.SecretAccessKey,
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}

	// /etc/clickhouse-backup/ 디렉토리 확인
	os.MkdirAll("/etc/clickhouse-backup", 0755)

	return writeFileSecure(backupConfigPath, []byte(buf.String()), 0600, "")
}

// rollback은 .bak 파일에서 원래 config를 복원합니다.
func (a *S3Activator) rollback() error {
	a.daemon.logJSON("info", "Rolling back configs", nil)

	// storage.xml 롤백
	if _, err := os.Stat(storageXMLPath + ".bak"); err == nil {
		if err := sudoCopy(storageXMLPath+".bak", storageXMLPath); err != nil {
			return fmt.Errorf("storage.xml 롤백 실패: %w", err)
		}
	} else {
		// .bak 없으면 storage.xml 삭제 (원래 없었던 파일)
		exec.Command("sudo", "rm", "-f", storageXMLPath).Run()
	}

	// s3.env 롤백
	if _, err := os.Stat(s3EnvPath + ".bak"); err == nil {
		if err := sudoCopy(s3EnvPath+".bak", s3EnvPath); err != nil {
			return fmt.Errorf("s3.env 롤백 실패: %w", err)
		}
	} else {
		// .bak 없으면 s3.env 비우기
		exec.Command("sudo", "truncate", "-s", "0", s3EnvPath).Run()
	}

	a.daemon.logJSON("info", "Rollback complete", nil)
	return nil
}

// backupFile은 파일을 .bak으로 백업합니다.
func backupFile(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("file does not exist: %s", path)
	}
	return sudoCopy(path, path+".bak")
}

// sudoCopy는 sudo cp로 파일을 복사합니다.
func sudoCopy(src, dst string) error {
	cmd := exec.Command("sudo", "cp", src, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sudo cp %s %s: %s (output: %s)", src, dst, err, string(out))
	}
	return nil
}

// writeFileSecure는 파일을 지정된 권한과 소유자로 생성합니다.
// sudo를 사용하여 /etc 하위에 쓰기 가능합니다.
func writeFileSecure(path string, data []byte, perm os.FileMode, owner string) error {
	// 임시 파일에 쓰고 sudo mv
	tmpFile := fmt.Sprintf("/tmp/k-o11y-agent-%d", time.Now().UnixNano())
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return err
	}
	defer os.Remove(tmpFile)

	if out, err := exec.Command("sudo", "mv", tmpFile, path).CombinedOutput(); err != nil {
		return fmt.Errorf("sudo mv: %s (output: %s)", err, string(out))
	}

	if out, err := exec.Command("sudo", "chmod", fmt.Sprintf("%o", perm), path).CombinedOutput(); err != nil {
		return fmt.Errorf("sudo chmod: %s (output: %s)", err, string(out))
	}

	if owner != "" {
		if out, err := exec.Command("sudo", "chown", owner+":"+owner, path).CombinedOutput(); err != nil {
			return fmt.Errorf("sudo chown: %s (output: %s)", err, string(out))
		}
	}

	return nil
}
