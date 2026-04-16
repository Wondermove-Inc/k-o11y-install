package agent

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"
)

// S3Config는 k_o11y.s3_config 테이블의 한 행을 나타냅니다.
type S3Config struct {
	ConfigID           string
	AuthMode           string
	Bucket             string
	Region             string
	Endpoint           string
	AccessKeyID        string // 암호화된 상태
	SecretAccessKey    string // 암호화된 상태
	S3Enabled          uint8
	ActivateRequested  uint8  // 1: Activate 요청됨 (에이전트가 처리 후 0으로 리셋)
	UpdatedAt          time.Time
}

// LifecycleConfig는 k_o11y.data_lifecycle_config 테이블의 한 행을 나타냅니다.
type LifecycleConfig struct {
	SignalType           string
	HotDays              int32
	WarmDays             int32
	GlacierEnabled       uint8
	GlacierRetentionDays int32
	BackupFrequencyHours int32
	LastBackupStatus     string
	LastBackupAt         time.Time
	LastBackupError      string
	UpdatedAt            time.Time
}

// Poller는 CH 메타데이터 테이블을 주기적으로 폴링합니다.
type Poller struct {
	db          *sql.DB
	state       *StateMachine
	daemon      *Daemon
	lastChecked time.Time

	// 마지막으로 감지된 설정 (변경 비교용)
	lastS3Config        *S3Config
	lastLifecycleConfig *LifecycleConfig
}

// NewPoller는 새 Poller를 생성합니다.
// 초기 lastChecked는 now() - 60s (에이전트 재시작 시 최근 변경 재처리).
func NewPoller(db *sql.DB, state *StateMachine, daemon *Daemon) *Poller {
	return &Poller{
		db:          db,
		state:       state,
		daemon:      daemon,
		lastChecked: time.Now().UTC().Add(-60 * time.Second),
	}
}

// Poll은 한 번의 폴링 주기를 수행합니다.
// 1. s3_config 변경 감지
// 2. data_lifecycle_config 변경 감지
// 3. heartbeat INSERT
func (p *Poller) Poll(ctx context.Context) {
	// s3_config 폴링
	s3Cfg, err := p.pollS3Config(ctx)
	if err != nil {
		p.daemon.logJSON("error", "s3_config poll failed", map[string]interface{}{
			"error": err.Error(),
		})
	} else if s3Cfg != nil {
		p.handleS3ConfigChange(s3Cfg)
	}

	// data_lifecycle_config 폴링
	lcCfg, err := p.pollLifecycleConfig(ctx)
	if err != nil {
		p.daemon.logJSON("error", "data_lifecycle_config poll failed", map[string]interface{}{
			"error": err.Error(),
		})
	} else if lcCfg != nil {
		p.handleLifecycleConfigChange(lcCfg)
	}

	// heartbeat INSERT
	if err := p.insertHeartbeat(ctx); err != nil {
		p.daemon.logJSON("warn", "Heartbeat insert failed", map[string]interface{}{
			"error": err.Error(),
		})
	}

	// last_checked 갱신
	p.lastChecked = time.Now().UTC()
}

// pollS3Config는 s3_config 테이블에서 최신 행을 조회합니다.
// ReplacingMergeTree이므로 FINAL을 사용하여 최신 버전만 조회합니다.
func (p *Poller) pollS3Config(ctx context.Context) (*S3Config, error) {
	query := `SELECT config_id, auth_mode, bucket, region, endpoint,
	                 access_key_id, secret_access_key, s3_enabled, activate_requested, updated_at
	          FROM k_o11y.s3_config FINAL
	          WHERE config_id = 'warm'
	          LIMIT 1`

	row := p.db.QueryRowContext(ctx, query)

	var cfg S3Config
	err := row.Scan(
		&cfg.ConfigID, &cfg.AuthMode, &cfg.Bucket, &cfg.Region, &cfg.Endpoint,
		&cfg.AccessKeyID, &cfg.SecretAccessKey, &cfg.S3Enabled, &cfg.ActivateRequested, &cfg.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan s3_config: %w", err)
	}

	return &cfg, nil
}

// pollLifecycleConfig는 data_lifecycle_config에서 최신 행을 조회합니다.
func (p *Poller) pollLifecycleConfig(ctx context.Context) (*LifecycleConfig, error) {
	query := `SELECT signal_type, hot_days, warm_days,
	                 glacier_enabled, glacier_retention_days, backup_frequency_hours,
	                 last_backup_status, last_backup_at, last_backup_error, updated_at
	          FROM k_o11y.data_lifecycle_config FINAL
	          WHERE signal_type = 'global'
	          LIMIT 1`

	row := p.db.QueryRowContext(ctx, query)

	var cfg LifecycleConfig
	err := row.Scan(
		&cfg.SignalType, &cfg.HotDays, &cfg.WarmDays,
		&cfg.GlacierEnabled, &cfg.GlacierRetentionDays, &cfg.BackupFrequencyHours,
		&cfg.LastBackupStatus, &cfg.LastBackupAt, &cfg.LastBackupError, &cfg.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan data_lifecycle_config: %w", err)
	}

	return &cfg, nil
}

// handleS3ConfigChange는 s3_config 변경을 감지하고 적절한 액션을 디스패치합니다.
// activate_requested=1일 때만 S3 Activate를 수행합니다.
// Save와 Activate는 별개 행위:
//   Save: s3_enabled=1 + activate_requested=0 (설정 저장만)
//   Activate: s3_enabled=1 + activate_requested=1 (에이전트에게 실행 요청)
func (p *Poller) handleS3ConfigChange(cfg *S3Config) {
	if p.lastS3Config == nil {
		p.lastS3Config = cfg
		p.daemon.logJSON("info", "s3_config initial state loaded", map[string]interface{}{
			"s3_enabled":         cfg.S3Enabled,
			"activate_requested": cfg.ActivateRequested,
			"bucket":             cfg.Bucket,
		})

		// 초기 로드 시 activate_requested=1이면 Activate 수행 (에이전트 재시작 시 미처리 요청 처리)
		if cfg.ActivateRequested == 1 && cfg.Bucket != "" {
			p.daemon.logJSON("info", "Pending activate_requested found, dispatching S3 Activate", nil)
			p.daemon.enqueueAction(Action{Type: ActionS3Activate, Payload: cfg})
		}
		return
	}

	// activate_requested: 0→1 변경 감지 → S3 Activate 수행
	if cfg.ActivateRequested == 1 && p.lastS3Config.ActivateRequested == 0 {
		p.daemon.logJSON("info", "activate_requested=1 detected, dispatching S3 Activate", map[string]interface{}{
			"bucket": cfg.Bucket,
			"region": cfg.Region,
		})
		p.daemon.enqueueAction(Action{Type: ActionS3Activate, Payload: cfg})
	}

	// s3_enabled: 1→0 변경 감지 → S3 Deactivate
	if cfg.S3Enabled == 0 && p.lastS3Config.S3Enabled == 1 {
		p.daemon.logJSON("info", "s3_enabled 1→0, dispatching S3 Deactivate", nil)
		p.daemon.enqueueAction(Action{Type: ActionS3Deactivate, Payload: cfg})
	}

	p.lastS3Config = cfg
}

// handleLifecycleConfigChange는 data_lifecycle_config 변경을 감지하고 액션을 디스패치합니다.
func (p *Poller) handleLifecycleConfigChange(cfg *LifecycleConfig) {
	if p.lastLifecycleConfig == nil {
		p.lastLifecycleConfig = cfg
		p.daemon.logJSON("info", "data_lifecycle_config initial state loaded", map[string]interface{}{
			"hot_days":        cfg.HotDays,
			"warm_days":       cfg.WarmDays,
			"glacier_enabled": cfg.GlacierEnabled,
		})
		// 초기 로드 시 glacier가 이미 활성화되어 있으면 스케줄러 시작
		if cfg.GlacierEnabled == 1 {
			p.daemon.logJSON("info", "glacier already enabled on startup, dispatching Backup Start", map[string]interface{}{
				"frequency_hours": cfg.BackupFrequencyHours,
			})
			p.daemon.enqueueAction(Action{Type: ActionBackupStart, Payload: cfg})
		}
		return
	}

	// glacier_enabled 변경 감지
	if cfg.GlacierEnabled != p.lastLifecycleConfig.GlacierEnabled {
		if cfg.GlacierEnabled == 1 {
			p.daemon.logJSON("info", "glacier_enabled 0→1, dispatching Backup Start", map[string]interface{}{
				"retention_days":   cfg.GlacierRetentionDays,
				"frequency_hours":  cfg.BackupFrequencyHours,
			})
			p.daemon.enqueueAction(Action{Type: ActionBackupStart, Payload: cfg})
		} else {
			p.daemon.logJSON("info", "glacier_enabled 1→0, dispatching Backup Stop", nil)
			p.daemon.enqueueAction(Action{Type: ActionBackupStop, Payload: cfg})
		}
	}

	// hot_days/warm_days 변경 감지
	if cfg.HotDays != p.lastLifecycleConfig.HotDays || cfg.WarmDays != p.lastLifecycleConfig.WarmDays {
		p.daemon.logJSON("info", "TTL config changed, dispatching TTL Update", map[string]interface{}{
			"hot_days":  cfg.HotDays,
			"warm_days": cfg.WarmDays,
		})
		p.daemon.enqueueAction(Action{Type: ActionTTLUpdate, Payload: cfg})
	}

	p.lastLifecycleConfig = cfg
}

// insertHeartbeat는 agent_status 테이블에 heartbeat를 기록합니다.
func (p *Poller) insertHeartbeat(ctx context.Context) error {
	hostname, _ := os.Hostname()

	query := `INSERT INTO k_o11y.agent_status
	          (hostname, version, last_heartbeat, status, last_action, last_action_at, last_error)
	          VALUES (?, ?, now64(3), ?, ?, now64(3), ?)`

	_, err := p.db.ExecContext(ctx, query,
		hostname,
		Version,
		p.state.Current().String(),
		"", // last_action — Phase 3+에서 업데이트
		"", // last_error
	)
	return err
}

// isS3DiskActive는 system.disks에 S3 타입 디스크가 있는지 확인합니다.
func (p *Poller) isS3DiskActive() bool {
	var count int
	err := p.db.QueryRow("SELECT count() FROM system.disks WHERE type='s3'").Scan(&count)
	return err == nil && count > 0
}

// Version은 빌드 시 -ldflags로 주입되는 에이전트 버전입니다.
var Version = "dev"
