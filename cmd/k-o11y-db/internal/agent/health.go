package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// HealthResponse는 /health 엔드포인트의 JSON 응답입니다.
type HealthResponse struct {
	Status          string `json:"status"`
	Uptime          string `json:"uptime"`
	LastPoll        string `json:"last_poll"`
	CHConnected     bool   `json:"ch_connected"`
	ActionsInFlight int    `json:"actions_in_flight"`
	QueueLength     int    `json:"queue_length"`
	Version         string `json:"version"`
}

// HealthServer는 /health HTTP 엔드포인트를 제공합니다.
type HealthServer struct {
	daemon *Daemon
	server *http.Server
}

// NewHealthServer는 새 HealthServer를 생성합니다.
func NewHealthServer(daemon *Daemon) *HealthServer {
	return &HealthServer{daemon: daemon}
}

// Start는 /health 엔드포인트를 비동기로 시작합니다.
func (hs *HealthServer) Start(bind string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", hs.handleHealth)

	hs.server = &http.Server{
		Addr:         bind,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		hs.daemon.logJSON("info", "Health server started", map[string]interface{}{
			"bind": bind,
		})
		if err := hs.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			hs.daemon.logJSON("error", "Health server error", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}()
}

// Stop은 Health 서버를 종료합니다.
func (hs *HealthServer) Stop() {
	if hs.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		hs.server.Shutdown(ctx)
	}
}

// handleHealth는 /health 요청을 처리합니다.
func (hs *HealthServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	// CH 연결 확인
	chConnected := false
	if hs.daemon.db != nil {
		if err := hs.daemon.db.Ping(); err == nil {
			chConnected = true
		}
	}

	// 실행 중인 액션 수
	actionsInFlight := 0
	if hs.daemon.state.Current() == StateExecuting {
		actionsInFlight = 1
	}

	// 마지막 폴링 시각
	lastPoll := "never"
	if hs.daemon.poller != nil {
		elapsed := time.Since(hs.daemon.poller.lastChecked).Round(time.Second)
		lastPoll = fmt.Sprintf("%s ago", elapsed)
	}

	status := "ok"
	if !chConnected {
		status = "degraded"
	}
	if hs.daemon.state.Current() == StateError {
		status = "error"
	}

	resp := HealthResponse{
		Status:          status,
		Uptime:          time.Since(hs.daemon.startTime).Round(time.Second).String(),
		LastPoll:        lastPoll,
		CHConnected:     chConnected,
		ActionsInFlight: actionsInFlight,
		QueueLength:     hs.daemon.state.QueueLen(),
		Version:         Version,
	}

	w.Header().Set("Content-Type", "application/json")
	if !chConnected {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(resp)
}
