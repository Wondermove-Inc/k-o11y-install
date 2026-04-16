package cmd

import (
	"github.com/spf13/cobra"
)

var (
	// agent 전용 플래그
	chHost        string
	chPort        int
	chPassword    string
	encryptionKey string
	pollInterval  string
	healthBind    string
	logLevel      string
)

// agentCmd는 agent 서브커맨드의 루트입니다.
var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "CH VM 경량 에이전트 관리",
	Long: `ClickHouse VM에서 상주하며 DB 폴링 기반으로 운영 작업을 자율 수행합니다.

서브커맨드:
  start    에이전트 데몬을 foreground로 실행
  status   에이전트 상태 조회 (agent_status 테이블)`,
}

func init() {
	// agent 공통 플래그
	agentCmd.PersistentFlags().StringVar(&chHost, "clickhouse-host", "localhost", "ClickHouse 호스트")
	agentCmd.PersistentFlags().IntVar(&chPort, "clickhouse-port", 9000, "ClickHouse native 포트")
	agentCmd.PersistentFlags().StringVar(&chPassword, "clickhouse-password", "", "ClickHouse default 사용자 비밀번호")
	agentCmd.PersistentFlags().StringVar(&encryptionKey, "encryption-key", "", "K_O11Y_ENCRYPTION_KEY (AES-256-GCM)")
	agentCmd.PersistentFlags().StringVar(&pollInterval, "poll-interval", "30s", "DB 폴링 주기 (예: 30s, 1m)")
	agentCmd.PersistentFlags().StringVar(&healthBind, "health-bind", "127.0.0.1:8099", "/health 엔드포인트 바인드 주소")
	agentCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "로그 레벨 (debug|info|warn|error)")

	// 서브커맨드 등록
	agentCmd.AddCommand(agentStartCmd)
	agentCmd.AddCommand(agentStatusCmd)
}
