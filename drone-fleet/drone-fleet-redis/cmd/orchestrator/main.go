// cmd/orchestrator/main.go
// Orquestrador com Redis como middleware central — Etapa 5.
//
// Integrações com Redis:
//   1. Pub/Sub  → recebe heartbeats/alertas via canal "drone:heartbeat"
//                 em vez de socket UDP direto — desacoplamento assíncrono
//   2. Hash     → persiste DroneStatus com TTL (tolerância a falha de memória)
//   3. Sorted Set → fila de mutex Ricart-Agrawala persiste além do processo
//   4. String atômica → relógio de Lamport global (INCR atômico do Redis)
//   5. String   → líder eleito persiste entre reinicializações

package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Guiscoob7/drone-fleet/internal/clock"
	"github.com/Guiscoob7/drone-fleet/internal/config"
	"github.com/Guiscoob7/drone-fleet/internal/election"
	"github.com/Guiscoob7/drone-fleet/internal/messages"
	"github.com/Guiscoob7/drone-fleet/internal/models"
	"github.com/Guiscoob7/drone-fleet/internal/mutex"
	redisclient "github.com/Guiscoob7/drone-fleet/internal/redis"
	"github.com/Guiscoob7/drone-fleet/pkg/nameserver"
)

// ── helpers de log ──────────────────────────────────────────────────────────

func logSection(label string) {
	fmt.Printf("\n┌─ %s\n", label)
}

func logInfo(icon, msg string) {
	fmt.Printf("│  %s  %s\n", icon, msg)
}

func logClose() {
	fmt.Println("└" + "─────────────────────────────────────────")
}

func logDivider() {
	fmt.Println()
}

// ── Orchestrator ────────────────────────────────────────────────────────────

type Orchestrator struct {
	drones map[int]*models.DroneStatus
	mu     sync.RWMutex

	lamport *clock.LamportClock
	vector  *clock.VectorClock

	resMgr       *mutex.ResourceManager
	electionMgr  *election.ElectionManager
	electionDone chan int

	redis *redisclient.Client
}

func NewOrchestrator(rc *redisclient.Client) *Orchestrator {
	return &Orchestrator{
		drones:       make(map[int]*models.DroneStatus),
		lamport:      clock.NewLamportClock(),
		vector:       clock.NewVectorClock("orchestrator"),
		resMgr:       mutex.NewResourceManager(rc),
		electionMgr:  election.NewElectionManager(),
		electionDone: make(chan int, 1),
		redis:        rc,
	}
}

func (o *Orchestrator) UpdateDrone(status *models.DroneStatus) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.drones[status.ID] = status
	o.resMgr.SetTotalDrones(len(o.drones))

	o.redis.SaveDroneStatus(status.ID, map[string]interface{}{
		"id":         status.ID,
		"battery":    status.Battery,
		"position_x": status.PositionX,
		"position_y": status.PositionY,
		"state":      status.State,
	})
}

func (o *Orchestrator) receive(msg *messages.Message) {
	o.lamport.Update(msg.LamportTS)
	if msg.VectorClock != nil {
		o.vector.Update(msg.VectorClock)
	}
	// Persiste valor atualizado no Redis (visível no Redis Commander)
	o.redis.SetLamportClock(o.lamport.Value())
}

func (o *Orchestrator) newReply(msgType messages.MessageType, payload map[string]interface{}) *messages.Message {
	l := o.lamport.Tick()
	v := o.vector.Tick()
	// Persiste após cada tick também
	o.redis.SetLamportClock(l)
	return messages.NewMessageWithClocks(msgType, "orchestrator", l, v, payload)
}

// ── main ────────────────────────────────────────────────────────────────────

func main() {
	fmt.Println()
	fmt.Println("╔════════════════════════════════════════════╗")
	fmt.Println("║   🎯  Orquestrador Iniciado  (Etapa 5)     ║")
	fmt.Println("║        Middleware: Redis                    ║")
	fmt.Println("╚════════════════════════════════════════════╝")
	fmt.Println()

	// ── Redis ──────────────────────────────────────────────────────────────
	rc, err := redisclient.NewClient(config.RedisAddr)
	if err != nil {
		fmt.Printf("❌  Falha ao conectar ao Redis: %v\n", err)
		fmt.Println("    👉  Suba o Redis: docker-compose up -d redis")
		fmt.Println()
		os.Exit(1)
	}
	defer rc.Close()
	fmt.Println("✅  Redis conectado em", config.RedisAddr)
	fmt.Println()

	orch := NewOrchestrator(rc)

	// ── Name Server ────────────────────────────────────────────────────────
	nsClient := nameserver.NewClient()
	time.Sleep(1 * time.Second)

	serviceInfo := messages.ServiceInfo{
		Name:    "orchestrator",
		Type:    string(messages.ServiceOrchestrator),
		Address: config.ServerHost + config.TCPPort,
		TCPPort: config.TCPPort,
		UDPPort: config.UDPPort,
	}

	if err := nsClient.Register(serviceInfo); err != nil {
		fmt.Printf("⚠️   Name Server: %v\n    Continuando sem registro...\n\n", err)
	} else {
		fmt.Println("✅  Registrado no Name Server")
		fmt.Println()
	}

	// ── Servidores ─────────────────────────────────────────────────────────
	fmt.Println("🚀  Iniciando servidores...")
	fmt.Println()

	go startRedisPubSubConsumer(orch)
	go startTCPServer(orch)
	go startUDPServer(orch)

	// Mantém o processo vivo — todos os servidores rodam em goroutines independentes.
	// Ctrl+C (SIGINT) ou SIGTERM encerram normalmente.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	fmt.Println("\n🛑  Orquestrador encerrado.")
}

// ── Redis Pub/Sub Consumer ──────────────────────────────────────────────────

func startRedisPubSubConsumer(orch *Orchestrator) {
	fmt.Println("📡  [Pub/Sub] Aguardando mensagens nos canais drone:*")
	fmt.Println()

	ch := orch.redis.Subscribe(
		redisclient.ChannelHeartbeat,
		redisclient.ChannelEmergency,
		redisclient.ChannelBatteryLow,
		redisclient.ChannelElection,
	)

	for redisMsg := range ch {
		msg, err := messages.FromJSON([]byte(redisMsg.Payload))
		if err != nil {
			fmt.Printf("⚠️   [Pub/Sub] JSON inválido — canal: %s\n\n", redisMsg.Channel)
			continue
		}

		orch.receive(msg)

		switch redisMsg.Channel {

		case redisclient.ChannelHeartbeat:
			droneID := int(msg.Payload["drone_id"].(float64))
			battery := int(msg.Payload["battery"].(float64))
			posX := msg.Payload["position_x"].(float64)
			posY := msg.Payload["position_y"].(float64)
			state := msg.Payload["state"].(string)

			status := &models.DroneStatus{
				ID: droneID, Battery: battery,
				PositionX: posX, PositionY: posY, State: state,
			}
			orch.UpdateDrone(status)

			logSection("💓 Heartbeat")
			logInfo("🤖", fmt.Sprintf("Drone #%d", droneID))
			logInfo("🔋", fmt.Sprintf("Bateria: %d%%", battery))
			logInfo("📍", fmt.Sprintf("Posição: (%.1f, %.1f)", posX, posY))
			logInfo("⚙️ ", fmt.Sprintf("Estado:  %s", state))
			logInfo("🕒", fmt.Sprintf("Lamport: %d  |  Vector: %s", msg.LamportTS, clock.FormatVector(msg.VectorClock)))
			logClose()
			logDivider()

		case redisclient.ChannelBatteryLow:
			droneID := int(msg.Payload["drone_id"].(float64))
			battery := int(msg.Payload["battery"].(float64))

			logSection("🔋 Bateria Baixa")
			logInfo("🤖", fmt.Sprintf("Drone #%d", droneID))
			logInfo("⚡", fmt.Sprintf("Nível:   %d%%  ← ATENÇÃO", battery))
			logInfo("🕒", fmt.Sprintf("Lamport: %d", msg.LamportTS))
			logClose()
			logDivider()

		case redisclient.ChannelEmergency:
			droneID := int(msg.Payload["drone_id"].(float64))
			reason := msg.Payload["reason"].(string)

			logSection("🚨 EMERGÊNCIA")
			logInfo("🤖", fmt.Sprintf("Drone #%d", droneID))
			logInfo("⛔", fmt.Sprintf("Motivo:  %s", reason))
			logInfo("🕒", fmt.Sprintf("Lamport: %d", msg.LamportTS))
			logClose()
			logDivider()

		case redisclient.ChannelElection:
			droneID := int(msg.Payload["drone_id"].(float64))

			logSection("🗳️  Eleição — candidato via Pub/Sub")
			logInfo("🤖", fmt.Sprintf("Drone #%d se candidatou", droneID))

			if !orch.electionMgr.InProgress() {
				orch.electionMgr.StartElection()
				go func() {
					time.Sleep(3 * time.Second)
					leaderID := orch.electionMgr.Elect()
					orch.redis.SetLeader(leaderID)
					orch.redis.Publish(redisclient.ChannelLeader, map[string]interface{}{
						"leader_id": leaderID,
					})
					logInfo("👑", fmt.Sprintf("Líder eleito: Drone #%d  (publicado em %s)",
						leaderID, redisclient.ChannelLeader))
					logClose()
					logDivider()
				}()
			}
			orch.electionMgr.AddCandidate(droneID)
		}
	}
}

// ── TCP ─────────────────────────────────────────────────────────────────────

func startTCPServer(orch *Orchestrator) {
	listener, err := net.Listen("tcp", config.TCPPort)
	if err != nil {
		fmt.Printf("❌  TCP: %v\n\n", err)
		os.Exit(1)
	}
	defer listener.Close()
	fmt.Printf("✅  [TCP] Escutando na porta %s\n\n", config.TCPPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("⚠️   [TCP] Erro na conexão:", err)
			continue
		}
		go handleTCPConnection(conn, orch)
	}
}

func handleTCPConnection(conn net.Conn, orch *Orchestrator) {
	defer conn.Close()
	buffer := make([]byte, 4096)
	n, err := conn.Read(buffer)
	if err != nil {
		return
	}

	msg, err := messages.FromJSON(buffer[:n])
	if err != nil {
		conn.Write([]byte("ERROR: JSON inválido"))
		return
	}

	orch.receive(msg)

	logSection(fmt.Sprintf("📨 TCP — %s", msg.Type))
	logInfo("📤", fmt.Sprintf("Origem:  %s", msg.SenderID))
	logInfo("🕒", fmt.Sprintf("Lamport: %d  |  Vector: %s", msg.LamportTS, clock.FormatVector(msg.VectorClock)))

	switch msg.Type {

	case messages.CommandRegister:
		droneID := int(msg.Payload["drone_id"].(float64))
		logInfo("🤖", fmt.Sprintf("Registrando Drone #%d...", droneID))

		reply := orch.newReply(messages.EventRegistered, map[string]interface{}{
			"drone_id": droneID,
			"status":   "registered",
			"message":  "Drone registrado com sucesso",
		})
		jsonReply, _ := reply.ToJSON()
		conn.Write(jsonReply)

		logInfo("✅", fmt.Sprintf("Drone #%d registrado  (L=%d)", droneID, orch.lamport.Value()))

	case messages.CommandAssignTask:
		droneID := int(msg.Payload["drone_id"].(float64))
		taskDesc := msg.Payload["task"].(string)
		logInfo("📋", fmt.Sprintf("Tarefa para Drone #%d: %s", droneID, taskDesc))

		reply := orch.newReply(messages.EventTaskCompleted, map[string]interface{}{
			"drone_id": droneID,
			"task":     taskDesc,
			"status":   "assigned",
		})
		jsonReply, _ := reply.ToJSON()
		conn.Write(jsonReply)

	// ── Exclusão Mútua ────────────────────────────────────────────────────
	case messages.CommandMutexPoll:
		droneID := int(msg.Payload["drone_id"].(float64))
		granted := orch.resMgr.PollAccess(droneID)
		logInfo("🔍", fmt.Sprintf("Drone #%d fez poll → concedido: %v", droneID, granted))

		var replyType messages.MessageType
		if granted {
			replyType = messages.EventMutexGrant
		} else {
			replyType = messages.EventMutexQueue
		}
		reply := orch.newReply(replyType, map[string]interface{}{
			"drone_id": droneID,
			"granted":  granted,
			"queue":    orch.resMgr.QueueStatus(),
		})
		jsonReply, _ := reply.ToJSON()
		conn.Write(jsonReply)

	case messages.CommandMutexRequest:
		droneID := int(msg.Payload["drone_id"].(float64))
		req := &mutex.MutexRequest{
			DroneID:     droneID,
			LamportTS:   msg.LamportTS,
			VectorClock: msg.VectorClock,
		}

		granted := orch.resMgr.RequestAccess(req)
		logInfo("🔐", fmt.Sprintf("Drone #%d solicitou mutex → concedido: %v", droneID, granted))
		logInfo("📊", fmt.Sprintf("Fila: %s", orch.resMgr.QueueStatus()))

		var replyType messages.MessageType
		if granted {
			replyType = messages.EventMutexGrant
		} else {
			replyType = messages.EventMutexQueue
		}

		reply := orch.newReply(replyType, map[string]interface{}{
			"drone_id": droneID,
			"granted":  granted,
			"queue":    orch.resMgr.QueueStatus(),
		})
		jsonReply, _ := reply.ToJSON()
		conn.Write(jsonReply)

	case messages.CommandMutexRelease:
		droneID := int(msg.Payload["drone_id"].(float64))
		nextID := orch.resMgr.ReleaseAccess(droneID)
		logInfo("🔓", fmt.Sprintf("Drone #%d liberou mutex → próximo: #%d", droneID, nextID))

		reply := orch.newReply(messages.EventMutexGrant, map[string]interface{}{
			"drone_id": droneID,
			"next_id":  nextID,
			"queue":    orch.resMgr.QueueStatus(),
		})
		jsonReply, _ := reply.ToJSON()
		conn.Write(jsonReply)

	// ── Eleição de Líder ──────────────────────────────────────────────────
	case messages.CommandElection:
		droneID := int(msg.Payload["drone_id"].(float64))
		logInfo("🗳️ ", fmt.Sprintf("Drone #%d iniciou eleição", droneID))

		if !orch.electionMgr.InProgress() {
			orch.electionMgr.StartElection()
			go func() {
				time.Sleep(3 * time.Second)
				leaderID := orch.electionMgr.Elect()
				orch.redis.SetLeader(leaderID)
				select {
				case orch.electionDone <- leaderID:
				default:
				}
			}()
		}
		orch.electionMgr.AddCandidate(droneID)

		var leaderID int
		select {
		case leaderID = <-orch.electionDone:
		case <-time.After(5 * time.Second):
			leaderID = droneID
		}

		logInfo("👑", fmt.Sprintf("Líder eleito: Drone #%d  (persistido no Redis)", leaderID))

		reply := orch.newReply(messages.EventLeaderElected, map[string]interface{}{
			"leader_id": leaderID,
		})
		jsonReply, _ := reply.ToJSON()
		conn.Write(jsonReply)

	default:
		logInfo("⚠️ ", fmt.Sprintf("Tipo desconhecido: %s", msg.Type))
		conn.Write([]byte("ERROR: Tipo desconhecido"))
	}

	logClose()
	logDivider()
}

// ── UDP (fallback) ───────────────────────────────────────────────────────────

func startUDPServer(orch *Orchestrator) {
	addr, _ := net.ResolveUDPAddr("udp", config.UDPPort)
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		fmt.Printf("❌  UDP: %v\n\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Printf("✅  [UDP] Escutando na porta %s  (fallback/retrocompat.)\n\n", config.UDPPort)

	buffer := make([]byte, 4096)
	for {
		n, _, err := conn.ReadFromUDP(buffer)
		if err != nil {
			continue
		}

		msg, err := messages.FromJSON(buffer[:n])
		if err != nil {
			if status, err2 := models.FromJSON(buffer[:n]); err2 == nil {
				orch.UpdateDrone(status)
				logSection("📡 UDP fallback")
				logInfo("🤖", fmt.Sprintf("Drone #%d | Bateria: %d%%", status.ID, status.Battery))
				logClose()
				logDivider()
			}
			continue
		}

		orch.receive(msg)

		logSection(fmt.Sprintf("📡 UDP fallback — %s", msg.Type))
		logInfo("📤", fmt.Sprintf("Origem: %s  |  L=%d", msg.SenderID, msg.LamportTS))

		if msg.Type == messages.EventHeartbeat {
			droneID := int(msg.Payload["drone_id"].(float64))
			battery := int(msg.Payload["battery"].(float64))
			posX := msg.Payload["position_x"].(float64)
			posY := msg.Payload["position_y"].(float64)
			state := msg.Payload["state"].(string)
			orch.UpdateDrone(&models.DroneStatus{
				ID: droneID, Battery: battery,
				PositionX: posX, PositionY: posY, State: state,
			})
			logInfo("🤖", fmt.Sprintf("Drone #%d | Bat: %d%% | Pos: (%.1f, %.1f) | %s",
				droneID, battery, posX, posY, state))
		}

		logClose()
		logDivider()
	}
}
