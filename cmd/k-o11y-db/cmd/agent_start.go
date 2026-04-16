package cmd

import (
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/agent"
	"github.com/spf13/cobra"
)

// agentStartCmd는 에이전트 데몬을 foreground로 실행합니다.
var agentStartCmd = &cobra.Command{
	Use:   "start",
	Short: "에이전트 데몬을 foreground로 실행",
	Long: `ClickHouse VM에서 DB 폴링 기반으로 운영 작업을 자율 수행하는 데몬을 시작합니다.

systemd 서비스로 실행하거나 직접 실행할 수 있습니다.
SIGTERM/SIGINT 수신 시 진행 중인 작업 완료 후 graceful shutdown합니다.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if chPassword == "" {
			return fmt.Errorf("--clickhouse-password 플래그는 필수입니다")
		}

		interval, err := time.ParseDuration(pollInterval)
		if err != nil {
			return fmt.Errorf("잘못된 --poll-interval: %w", err)
		}

		cfg := &agent.Config{
			ClickHouseHost:     chHost,
			ClickHousePort:     chPort,
			ClickHousePassword: chPassword,
			EncryptionKey:      encryptionKey,
			PollInterval:       interval,
			HealthBind:         healthBind,
			LogLevel:           logLevel,
		}

		// SIGTERM, SIGINT 수신 시 context 취소
		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()

		return agent.Run(ctx, cfg)
	},
}
