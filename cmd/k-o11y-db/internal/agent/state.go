package agent

import (
	"fmt"
	"sync"
)

// State는 에이전트 상태 머신의 상태를 나타냅니다.
type State int

const (
	StateIdle          State = iota // 대기 중
	StatePolling                    // DB 폴링 중
	StateActionPending              // 액션 대기 중
	StateExecuting                  // 액션 실행 중
	StateError                      // 에러 발생
)

// String은 상태를 문자열로 반환합니다.
func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StatePolling:
		return "polling"
	case StateActionPending:
		return "action_pending"
	case StateExecuting:
		return "executing"
	case StateError:
		return "error"
	default:
		return "unknown"
	}
}

// ActionType은 에이전트가 수행할 수 있는 액션 유형입니다.
type ActionType int

const (
	ActionS3Activate   ActionType = iota // S3 스토리지 활성화
	ActionS3Deactivate                   // S3 스토리지 비활성화
	ActionBackupStart                    // Cold Backup 스케줄 시작
	ActionBackupStop                     // Cold Backup 스케줄 중지
	ActionBackupRun                      // Cold Backup 즉시 실행
	ActionTTLUpdate                      // TTL 재적용
)

// String은 액션 유형을 문자열로 반환합니다.
func (a ActionType) String() string {
	switch a {
	case ActionS3Activate:
		return "s3_activate"
	case ActionS3Deactivate:
		return "s3_deactivate"
	case ActionBackupStart:
		return "backup_start"
	case ActionBackupStop:
		return "backup_stop"
	case ActionBackupRun:
		return "backup_run"
	case ActionTTLUpdate:
		return "ttl_update"
	default:
		return "unknown"
	}
}

// Action은 디스패치할 액션을 나타냅니다.
type Action struct {
	Type    ActionType
	Payload interface{}
}

// StateMachine은 에이전트의 상태 전이를 관리합니다.
// EXECUTING 중에는 새 액션을 큐잉하고, 완료 후 처리합니다.
type StateMachine struct {
	mu      sync.Mutex
	current State
	queue   []Action
}

// NewStateMachine은 IDLE 상태로 초기화된 상태 머신을 생성합니다.
func NewStateMachine() *StateMachine {
	return &StateMachine{
		current: StateIdle,
		queue:   make([]Action, 0),
	}
}

// Current는 현재 상태를 반환합니다.
func (sm *StateMachine) Current() State {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.current
}

// Transition은 상태를 전이합니다.
// 유효하지 않은 전이는 에러를 반환합니다.
func (sm *StateMachine) Transition(to State) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if !sm.isValidTransition(sm.current, to) {
		return fmt.Errorf("invalid transition: %s → %s", sm.current, to)
	}
	sm.current = to
	return nil
}

// Dispatch는 액션을 큐에 추가합니다.
// 큐에 추가된 액션은 processQueue()에서 순차 실행됩니다.
func (sm *StateMachine) Dispatch(action Action) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.queue = append(sm.queue, action)
	return true
}

// ForceState는 상태를 강제 전이합니다. processQueue 전용.
func (sm *StateMachine) ForceState(to State) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.current = to
}

// DequeueAction은 큐에서 다음 액션을 꺼냅니다.
// 큐가 비어있으면 nil을 반환합니다.
func (sm *StateMachine) DequeueAction() *Action {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if len(sm.queue) == 0 {
		return nil
	}

	action := sm.queue[0]
	sm.queue = sm.queue[1:]
	return &action
}

// QueueLen은 현재 큐에 대기 중인 액션 수를 반환합니다.
func (sm *StateMachine) QueueLen() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return len(sm.queue)
}

// isValidTransition은 상태 전이가 유효한지 검사합니다.
func (sm *StateMachine) isValidTransition(from, to State) bool {
	valid := map[State][]State{
		StateIdle:          {StatePolling},
		StatePolling:       {StateIdle, StateActionPending},
		StateActionPending: {StateExecuting},
		StateExecuting:     {StateIdle, StateError},
		StateError:         {StateIdle},
	}

	targets, ok := valid[from]
	if !ok {
		return false
	}
	for _, t := range targets {
		if t == to {
			return true
		}
	}
	return false
}
