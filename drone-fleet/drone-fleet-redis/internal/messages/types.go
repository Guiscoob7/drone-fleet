// internal/messages/types.go

package messages

// MessageType define os tipos de mensagens do sistema
type MessageType string

const (
	// === COMANDOS ===
	CommandRegister      MessageType = "CMD_REGISTER"
	CommandAssignTask    MessageType = "CMD_ASSIGN_TASK"
	CommandReturnToBase  MessageType = "CMD_RETURN_BASE"
	CommandStartCharging MessageType = "CMD_START_CHARGING"
	CommandShutdown      MessageType = "CMD_SHUTDOWN"

	// === EVENTOS ===
	EventRegistered     MessageType = "EVT_REGISTERED"
	EventHeartbeat      MessageType = "EVT_HEARTBEAT"
	EventTaskCompleted  MessageType = "EVT_TASK_COMPLETED"
	EventBatteryLow     MessageType = "EVT_BATTERY_LOW"
	EventEmergency      MessageType = "EVT_EMERGENCY"
	EventPositionUpdate MessageType = "EVT_POSITION_UPDATE"

	// === EXCLUSÃO MÚTUA ===
	CommandMutexRequest MessageType = "CMD_MUTEX_REQUEST"
	CommandMutexRelease MessageType = "CMD_MUTEX_RELEASE"
	CommandMutexPoll    MessageType = "CMD_MUTEX_POLL"
	EventMutexGrant     MessageType = "EVT_MUTEX_GRANT"
	EventMutexQueue     MessageType = "EVT_MUTEX_QUEUE"
	EventMutexDenied    MessageType = "EVT_MUTEX_DENIED"

	// === ELEIÇÃO DE LÍDER ===
	CommandElection    MessageType = "CMD_ELECTION"
	EventLeaderElected MessageType = "EVT_LEADER_ELECTED"

	// === SERVIÇOS ===
	ServiceOrchestrator MessageType = "SVC_ORCHESTRATOR"
	ServiceDrone        MessageType = "SVC_DRONE"
	ServiceNameServer   MessageType = "SVC_NAMESERVER"
)

// ServiceInfo armazena informações sobre um serviço registrado
type ServiceInfo struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Address string `json:"address"`
	TCPPort string `json:"tcp_port"`
	UDPPort string `json:"udp_port"`
}
