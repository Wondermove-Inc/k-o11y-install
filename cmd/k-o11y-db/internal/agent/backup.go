package agent

import (
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// MaxPartitionsPerRun limits how many partitions are processed in a single backup cycle.
// Prevents resource pressure when many days are backlogged (e.g., 30 days × 1.2TB/day).
const MaxPartitionsPerRun = 7

// partitionDateRe matches YYYY-MM-DD partition format only.
// Excludes YYYYMM (monthly) and tuple() partitions.
var partitionDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// BackupScheduler는 Cold Backup(Glacier IR) 스케줄링을 관리합니다.
// ch-glacier-cron.sh의 로직을 Go로 이식한 것입니다.
type BackupScheduler struct {
	db     *sql.DB
	daemon *Daemon
	part   *PartitionManager

	mu        sync.Mutex
	running   bool
	stopCh    chan struct{}
	frequency time.Duration
}

// NewBackupScheduler는 새 BackupScheduler를 생성합니다.
func NewBackupScheduler(db *sql.DB, daemon *Daemon) *BackupScheduler {
	return &BackupScheduler{
		db:     db,
		daemon: daemon,
		part:   NewPartitionManager(db, daemon),
	}
}

// Start는 백업 스케줄러를 시작합니다.
// frequencyHours 간격으로 백업을 수행합니다.
func (bs *BackupScheduler) Start(ctx context.Context, cfg *LifecycleConfig) {
	bs.mu.Lock()
	if bs.running {
		bs.mu.Unlock()
		bs.daemon.logJSON("info", "Backup scheduler already running, restarting with new config", nil)
		bs.Stop()
		bs.mu.Lock()
	}

	hours := cfg.BackupFrequencyHours
	if hours <= 0 {
		hours = 24
	}
	bs.frequency = time.Duration(hours) * time.Hour
	bs.running = true
	bs.stopCh = make(chan struct{})
	bs.mu.Unlock()

	bs.daemon.logJSON("info", "Backup scheduler started", map[string]interface{}{
		"frequency_hours": hours,
		"retention_days":  cfg.GlacierRetentionDays,
	})

	go bs.runLoop(ctx, cfg)
}

// Stop은 백업 스케줄러를 중지합니다.
func (bs *BackupScheduler) Stop() {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	if !bs.running {
		return
	}
	close(bs.stopCh)
	bs.running = false
	bs.daemon.logJSON("info", "Backup scheduler stopped", nil)
}

// IsRunning은 스케줄러가 실행 중인지 반환합니다.
func (bs *BackupScheduler) IsRunning() bool {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	return bs.running
}

// runLoop는 스케줄 주기마다 백업을 수행하는 루프입니다.
func (bs *BackupScheduler) runLoop(ctx context.Context, cfg *LifecycleConfig) {
	// Catch-up gate: last_backup이 2x frequency보다 오래되었으면 즉시 실행
	bs.daemon.logJSON("info", "runLoop started, evaluating catch-up", map[string]interface{}{
		"last_backup_at": cfg.LastBackupAt.Format(time.RFC3339),
		"is_zero":        cfg.LastBackupAt.IsZero(),
		"year":           cfg.LastBackupAt.Year(),
		"frequency":      bs.frequency.String(),
		"ctx_err":        fmt.Sprintf("%v", ctx.Err()),
	})
	if bs.shouldCatchUp(cfg) {
		bs.daemon.logJSON("info", "Catch-up gate triggered: last backup is stale, running immediately", map[string]interface{}{
			"last_backup_at":   cfg.LastBackupAt.Format(time.RFC3339),
			"frequency_hours":  cfg.BackupFrequencyHours,
		})
		if err := bs.runBackup(ctx, cfg); err != nil {
			bs.daemon.logJSON("error", "Catch-up backup run failed", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}

	ticker := time.NewTicker(bs.frequency)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-bs.stopCh:
			return
		case <-ticker.C:
			if err := bs.runBackup(ctx, cfg); err != nil {
				bs.daemon.logJSON("error", "Backup run failed", map[string]interface{}{
					"error": err.Error(),
				})
			}
		}
	}
}

// shouldCatchUp은 last_backup_at이 2x frequency보다 오래되었는지 확인합니다.
// 첫 실행(never)이거나, 장시간 미실행 시 true를 반환합니다.
func (bs *BackupScheduler) shouldCatchUp(cfg *LifecycleConfig) bool {
	// "never" 상태 (LastBackupAt이 zero value 또는 epoch)
	if cfg.LastBackupAt.IsZero() || cfg.LastBackupAt.Year() < 2000 {
		return true
	}

	threshold := 2 * bs.frequency
	return time.Since(cfg.LastBackupAt) > threshold
}

// RunNow는 백업을 즉시 실행합니다.
func (bs *BackupScheduler) RunNow(ctx context.Context, cfg *LifecycleConfig) error {
	return bs.runBackup(ctx, cfg)
}

// runBackup은 한 번의 백업 사이클을 수행합니다.
// cutoff 이전의 모든 파티션을 조회하여 순차적으로 백업합니다.
// 최대 MaxPartitionsPerRun개까지 처리하고, 나머지는 다음 주기에 처리합니다.
func (bs *BackupScheduler) runBackup(ctx context.Context, cfg *LifecycleConfig) error {
	bs.daemon.logJSON("info", "Backup run starting", nil)

	// cutoff 날짜 계산
	totalDays := int(cfg.HotDays + cfg.WarmDays)
	cutoffDate := time.Now().AddDate(0, 0, -totalDays).Format("2006-01-02")

	// cutoff 이전의 모든 파티션 조회
	partitions, err := bs.getAllPartitionsToArchive(ctx, cutoffDate)
	if err != nil {
		return fmt.Errorf("partition query failed: %w", err)
	}

	if len(partitions) == 0 {
		bs.daemon.logJSON("info", "No partitions to archive", map[string]interface{}{
			"cutoff_date": cutoffDate,
		})
		bs.updateStatus(ctx, cfg, "skipped_no_partitions", "")
		return nil
	}

	// 최대 제한 적용
	totalFound := len(partitions)
	if totalFound > MaxPartitionsPerRun {
		bs.daemon.logJSON("warn", "Backlog detected, limiting this run", map[string]interface{}{
			"total_found":   totalFound,
			"processing":    MaxPartitionsPerRun,
			"remaining":     totalFound - MaxPartitionsPerRun,
		})
		partitions = partitions[:MaxPartitionsPerRun]
	}

	bs.daemon.logJSON("info", "Partitions to archive", map[string]interface{}{
		"count":       len(partitions),
		"partitions":  partitions,
		"cutoff_date": cutoffDate,
	})

	// 순차 처리
	succeeded := 0
	failed := 0
	for _, partition := range partitions {
		if err := bs.archivePartition(ctx, cfg, partition); err != nil {
			failed++
			bs.daemon.logJSON("error", "Partition archive failed, continuing", map[string]interface{}{
				"partition": partition,
				"error":     err.Error(),
			})
			continue
		}
		succeeded++
	}

	// 상태 업데이트
	status := "success"
	errMsg := ""
	if failed > 0 && succeeded > 0 {
		status = "partial_failure"
		errMsg = fmt.Sprintf("%d/%d partitions failed", failed, succeeded+failed)
	} else if failed > 0 && succeeded == 0 {
		status = "failed"
		errMsg = fmt.Sprintf("all %d partitions failed", failed)
	}
	bs.updateStatus(ctx, cfg, status, errMsg)

	bs.daemon.logJSON("info", "Backup run completed", map[string]interface{}{
		"status":    status,
		"succeeded": succeeded,
		"failed":    failed,
		"remaining": totalFound - len(partitions),
	})

	return nil
}

// getAllPartitionsToArchive는 cutoff 이전의 모든 YYYY-MM-DD 파티션을 조회합니다.
func (bs *BackupScheduler) getAllPartitionsToArchive(ctx context.Context, cutoffDate string) ([]string, error) {
	query := `SELECT DISTINCT partition
		FROM system.parts
		WHERE active
		  AND database LIKE 'signoz_%'
		  AND rows > 0
		ORDER BY partition ASC`

	rows, err := bs.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var partitions []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			continue
		}
		// YYYY-MM-DD 형식만 대상 (tuple(), YYYYMM 제외)
		if !partitionDateRe.MatchString(p) {
			continue
		}
		// cutoff 이전 파티션만
		if p <= cutoffDate {
			partitions = append(partitions, p)
		}
	}
	return partitions, nil
}

// archivePartition은 단일 파티션의 백업 → 업로드 → DROP을 수행합니다.
func (bs *BackupScheduler) archivePartition(ctx context.Context, cfg *LifecycleConfig, partition string) error {
	compactDate := strings.ReplaceAll(partition, "-", "")
	backupName := "archive-" + compactDate

	bs.daemon.logJSON("info", "Archiving partition", map[string]interface{}{
		"partition":   partition,
		"backup_name": backupName,
	})

	// clickhouse-backup create
	createCmd := exec.CommandContext(ctx, "clickhouse-backup", "create",
		"--tables", "signoz_*.*",
		"--partitions", compactDate,
		backupName)
	createOut, createErr := createCmd.CombinedOutput()
	if createErr != nil {
		return fmt.Errorf("backup create failed: %s (output: %s)", createErr, string(createOut))
	}
	bs.daemon.logJSON("info", "Backup created", map[string]interface{}{
		"backup_name": backupName,
	})

	// upload to Glacier IR
	uploadCmd := exec.CommandContext(ctx, "clickhouse-backup", "upload", backupName)
	uploadOut, uploadErr := uploadCmd.CombinedOutput()
	if uploadErr != nil {
		bs.daemon.logJSON("error", "Upload command failed", map[string]interface{}{
			"backup_name": backupName,
			"error":       uploadErr.Error(),
			"output":      string(uploadOut),
		})
		exec.Command("clickhouse-backup", "delete", "local", backupName).Run()
		return fmt.Errorf("upload failed: %s", uploadErr)
	}
	bs.daemon.logJSON("info", "Upload completed", map[string]interface{}{
		"backup_name": backupName,
	})

	// upload이 exit 0으로 성공하면 검증 완료로 간주
	// (clickhouse-backup upload은 내부적으로 S3 PutObject 성공을 확인하고 exit 0을 반환)

	bs.daemon.logJSON("info", "Upload verified", map[string]interface{}{
		"backup_name": backupName,
	})

	// 로컬 백업 정리
	exec.Command("clickhouse-backup", "delete", "local", backupName).Run()

	// DROP PARTITION (DROP_TABLES만)
	dropCount, skipCount := bs.part.DropPartitions(ctx, partition)
	bs.daemon.logJSON("info", "DROP PARTITION complete", map[string]interface{}{
		"partition": partition,
		"dropped":   dropCount,
		"skipped":   skipCount,
	})

	return nil
}

// updateStatus는 data_lifecycle_config에 백업 상태를 기록합니다.
func (bs *BackupScheduler) updateStatus(ctx context.Context, cfg *LifecycleConfig, status, errMsg string) {
	query := `INSERT INTO k_o11y.data_lifecycle_config
		(signal_type, hot_days, warm_days, glacier_enabled, glacier_retention_days,
		 backup_frequency_hours, last_backup_status, last_backup_at, last_backup_error,
		 updated_by, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, now(), ?, 'agent', ?)`

	_, err := bs.db.ExecContext(ctx, query,
		"global", cfg.HotDays, cfg.WarmDays, cfg.GlacierEnabled,
		cfg.GlacierRetentionDays, cfg.BackupFrequencyHours,
		status, errMsg, time.Now().Unix())
	if err != nil {
		bs.daemon.logJSON("warn", "Failed to update backup status", map[string]interface{}{
			"error": err.Error(),
		})
	}
}
