package agent

import (
	"context"
	"database/sql"
	"fmt"
)

// DROP_TABLES는 백업 후 DROP PARTITION이 안전한 날짜 파티션 데이터 테이블입니다. (13개)
var DROP_TABLES = []string{
	// Logs (2)
	"signoz_logs.logs_v2",
	"signoz_logs.logs_v2_resource",
	// Metrics (4)
	"signoz_metrics.samples_v4",
	"signoz_metrics.samples_v4_agg_5m",
	"signoz_metrics.samples_v4_agg_30m",
	"signoz_metrics.exp_hist",
	// Traces (7)
	"signoz_traces.signoz_index_v3",
	"signoz_traces.traces_v3_resource",
	"signoz_traces.signoz_error_index_v2",
	"signoz_traces.usage_explorer",
	"signoz_traces.dependency_graph_minutes_v2",
	"signoz_traces.trace_summary",
	"signoz_traces.network_map_connections",
}

// BACKUP_ONLY_TABLES는 백업만 수행하고 DROP PARTITION은 절대 금지하는 테이블입니다. (8개)
// 메타데이터 성격으로 SigNoz UI가 의존합니다. 삭제하면 UI 장애 발생.
var BACKUP_ONLY_TABLES = []string{
	"signoz_logs.tag_attributes_v2",
	"signoz_traces.tag_attributes_v2",
	"signoz_metadata.attributes_metadata",
	"signoz_metrics.metadata",
	"signoz_metrics.time_series_v4",
	"signoz_metrics.time_series_v4_6hrs",
	"signoz_metrics.time_series_v4_1day",
	"signoz_metrics.time_series_v4_1week",
}

// PartitionManager는 DROP PARTITION을 관리합니다.
type PartitionManager struct {
	db     *sql.DB
	daemon *Daemon
}

// NewPartitionManager는 새 PartitionManager를 생성합니다.
func NewPartitionManager(db *sql.DB, daemon *Daemon) *PartitionManager {
	return &PartitionManager{db: db, daemon: daemon}
}

// DropPartitions는 DROP_TABLES에 대해 지정 날짜의 파티션을 삭제합니다.
// BACKUP_ONLY_TABLES는 절대 터치하지 않습니다.
// archiveDate 형식: "2006-01-02"
// 반환: (성공 수, 스킵 수)
func (pm *PartitionManager) DropPartitions(ctx context.Context, archiveDate string) (dropped, skipped int) {
	pm.daemon.logJSON("info", "DROP PARTITION starting", map[string]interface{}{
		"archive_date": archiveDate,
		"tables_count": len(DROP_TABLES),
	})

	for _, tbl := range DROP_TABLES {
		// 해당 테이블에 해당 파티션이 있는지 확인
		var partCount int
		err := pm.db.QueryRowContext(ctx,
			"SELECT count() FROM system.parts WHERE active AND concat(database, '.', table) = ? AND partition = ? AND rows > 0",
			tbl, archiveDate).Scan(&partCount)

		if err != nil {
			pm.daemon.logJSON("warn", "Partition check failed", map[string]interface{}{
				"table": tbl,
				"error": err.Error(),
			})
			skipped++
			continue
		}

		if partCount == 0 {
			skipped++
			continue
		}

		// DROP PARTITION 실행
		query := fmt.Sprintf("ALTER TABLE %s DROP PARTITION '%s'", tbl, archiveDate)
		if _, err := pm.db.ExecContext(ctx, query); err != nil {
			pm.daemon.logJSON("warn", "DROP PARTITION failed", map[string]interface{}{
				"table": tbl,
				"error": err.Error(),
			})
			skipped++
		} else {
			dropped++
			pm.daemon.logJSON("debug", "DROP PARTITION success", map[string]interface{}{
				"table": tbl,
			})
		}
	}

	return dropped, skipped
}
