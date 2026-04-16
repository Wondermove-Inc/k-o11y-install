package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// agentStatusCmd는 에이전트 상태를 조회합니다.
var agentStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "에이전트 상태 조회",
	Long:  `k_o11y.agent_status 테이블에서 에이전트의 현재 상태를 조회합니다.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Phase 5(P2)에서 구현 예정
		fmt.Println("⚠️  agent status는 아직 구현되지 않았습니다. (PHASE 5에서 구현 예정)")
		return nil
	},
}
