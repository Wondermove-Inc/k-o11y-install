package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime"
	"time"

	_ "github.com/ClickHouse/clickhouse-go/v2"
)

// Config는 에이전트 실행에 필요한 설정입니다.
type Config struct {
	ClickHouseHost     string
	ClickHousePort     int
	ClickHousePassword string
	EncryptionKey      string
	PollInterval       time.Duration
	HealthBind         string
	LogLevel           string
}

// Daemon은 에이전트 데몬의 핵심 구조체입니다.
type Daemon struct {
	cfg             *Config
	db              *sql.DB
	state           *StateMachine
	poller          *Poller
	s3Activator     *S3Activator
	backupScheduler *BackupScheduler
	healthServer    *HealthServer
	startTime       time.Time
	logger          *log.Logger
}

// New는 새 Daemon 인스턴스를 생성합니다.
func New(cfg *Config) *Daemon {
	return &Daemon{
		cfg:    cfg,
		state:  NewStateMachine(),
		logger: log.New(os.Stdout, "", 0),
	}
}

// logJSON은 구조화된 JSON 로그를 출력합니다.
func (d *Daemon) logJSON(level, msg string, fields map[string]interface{}) {
	entry := map[string]interface{}{
		"ts":    time.Now().UTC().Format(time.RFC3339Nano),
		"level": level,
		"msg":   msg,
		"state": d.state.Current().String(),
	}
	for k, v := range fields {
		entry[k] = v
	}
	data, _ := json.Marshal(entry)
	d.logger.Println(string(data))
}

// Run은 에이전트 메인 루프를 실행합니다.
// context가 취소되면 graceful shutdown을 수행합니다.
func Run(ctx context.Context, cfg *Config) error {
	if runtime.GOOS != "linux" {
		fmt.Println("⚠️  agent start는 Linux에서만 실행할 수 있습니다.")
		fmt.Println("   현재 OS:", runtime.GOOS)
		fmt.Println("   에이전트는 ClickHouse VM(Linux)에 배포하여 사용하세요.")
		return fmt.Errorf("agent start is linux-only (current: %s)", runtime.GOOS)
	}

	d := New(cfg)
	d.startTime = time.Now()

	d.logJSON("info", "Agent starting", map[string]interface{}{
		"clickhouse_host": cfg.ClickHouseHost,
		"poll_interval":   cfg.PollInterval.String(),
		"health_bind":     cfg.HealthBind,
	})

	// CH 연결 초기화
	if err := d.connectCH(); err != nil {
		return fmt.Errorf("ClickHouse 연결 실패: %w", err)
	}
	defer d.db.Close()

	d.logJSON("info", "ClickHouse connected", nil)

	// 컴포넌트 초기화
	d.poller = NewPoller(d.db, d.state, d)
	d.s3Activator = NewS3Activator(d.db, d)
	d.backupScheduler = NewBackupScheduler(d.db, d)
	d.healthServer = NewHealthServer(d)

	// Health HTTP 서버 시작
	d.healthServer.Start(cfg.HealthBind)

	// 폴링 루프
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	d.logJSON("info", "Agent started", map[string]interface{}{
		"pid": os.Getpid(),
	})

	// 첫 폴링 즉시 실행 (초기 상태 로드)
	d.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			d.logJSON("info", "Shutdown signal received", nil)
			return d.shutdown()
		case <-ticker.C:
			d.poll(ctx)
		}
	}
}

// connectCH는 ClickHouse에 연결합니다.
func (d *Daemon) connectCH() error {
	dsn := fmt.Sprintf("clickhouse://%s:%d?username=default&password=%s",
		d.cfg.ClickHouseHost, d.cfg.ClickHousePort, d.cfg.ClickHousePassword)

	db, err := sql.Open("clickhouse", dsn)
	if err != nil {
		return err
	}

	// 연결 풀 설정
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)

	// 연결 테스트
	if err := db.PingContext(context.Background()); err != nil {
		return fmt.Errorf("ping 실패: %w", err)
	}

	d.db = db
	return nil
}

// poll은 한 번의 폴링 주기를 수행합니다.
func (d *Daemon) poll(ctx context.Context) {
	if err := d.state.Transition(StatePolling); err != nil {
		// EXECUTING 중이면 폴링 스킵 (CH restart 등 진행 중)
		if d.state.Current() == StateExecuting {
			d.logJSON("debug", "Skipping poll (action executing)", nil)
			return
		}
		d.logJSON("warn", "State transition failed", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	// CH 연결 확인
	if err := d.db.PingContext(ctx); err != nil {
		d.logJSON("error", "ClickHouse ping failed", map[string]interface{}{
			"error": err.Error(),
		})
		d.state.Transition(StateIdle)
		return
	}

	// 메타데이터 폴링 + heartbeat
	d.poller.Poll(ctx)

	d.state.Transition(StateIdle)

	d.logJSON("debug", "Poll completed", map[string]interface{}{
		"uptime":    time.Since(d.startTime).Round(time.Second).String(),
		"queue_len": d.state.QueueLen(),
	})

	// 큐에 대기 중인 액션 처리
	d.processQueue(ctx)
}

// enqueueAction은 액션을 큐에 추가합니다. Poller에서 호출합니다.
func (d *Daemon) enqueueAction(action Action) {
	d.state.Dispatch(action)
	d.logJSON("debug", "Action enqueued", map[string]interface{}{
		"action":    action.Type.String(),
		"queue_len": d.state.QueueLen(),
	})
}

// processQueue는 큐에서 액션을 꺼내 순차 실행합니다.
func (d *Daemon) processQueue(ctx context.Context) {
	for {
		action := d.state.DequeueAction()
		if action == nil {
			return
		}

		// IDLE → EXECUTING 직접 전이 (큐 처리 전용)
		d.state.ForceState(StateExecuting)

	d.logJSON("info", "Executing action", map[string]interface{}{
		"action": action.Type.String(),
	})

	var err error
	switch action.Type {
	case ActionS3Activate:
		if cfg, ok := action.Payload.(*S3Config); ok {
			err = d.s3Activator.Activate(ctx, cfg)
		}
	case ActionS3Deactivate:
		err = d.s3Activator.Deactivate(ctx)
	case ActionBackupStart:
		if cfg, ok := action.Payload.(*LifecycleConfig); ok {
			d.backupScheduler.Start(ctx, cfg)
		}
	case ActionBackupStop:
		d.backupScheduler.Stop()
	case ActionBackupRun:
		if cfg, ok := action.Payload.(*LifecycleConfig); ok {
			err = d.backupScheduler.RunNow(ctx, cfg)
		}
	case ActionTTLUpdate:
		// TTL 재적용은 SigNoz Backend가 처리 (materialize_ttl_after_modify=0)
		// 에이전트는 로그만 기록
		d.logJSON("info", "TTL config change detected, SigNoz backend will reapply TTL", nil)
	}

	if err != nil {
		d.logJSON("error", "Action failed", map[string]interface{}{
			"action": action.Type.String(),
			"error":  err.Error(),
		})
	}

		d.state.ForceState(StateIdle)
	}
}

// shutdown은 graceful shutdown을 수행합니다.
// EXECUTING 중이면 완료를 기다립니다 (최대 180초).
func (d *Daemon) shutdown() error {
	d.logJSON("info", "Graceful shutdown starting", nil)

	// 컴포넌트 정리
	if d.backupScheduler != nil {
		d.backupScheduler.Stop()
	}
	if d.healthServer != nil {
		d.healthServer.Stop()
	}

	// EXECUTING 중이면 완료 대기
	if d.state.Current() == StateExecuting {
		d.logJSON("info", "Waiting for in-flight action to complete", nil)
		deadline := time.After(180 * time.Second)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-deadline:
				d.logJSON("warn", "Shutdown timeout, forcing exit", nil)
				return nil
			case <-ticker.C:
				if d.state.Current() != StateExecuting {
					break
				}
			}
		}
	}

	d.logJSON("info", "Agent stopped", nil)
	return nil
}
