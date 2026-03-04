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
	"sync"
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

// Orchestrator mantém o estado global do sistema.
type Orchestrator struct {
	drones  map[int]*models.DroneStatus
	mu      sync.RWMutex

	lamport *clock.LamportClock
	vector  *clock.VectorClock

	resMgr      *mutex.ResourceManager
	electionMgr *election.ElectionManager
	electionDone chan int

	// Redis — middleware central
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

	// Persiste no Redis com TTL (tolerância a falhas de memória)
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
}

func (o *Orchestrator) newReply(msgType messages.MessageType, payload map[string]interface{}) *messages.Message {
	l := o.lamport.Tick()
	v := o.vector.Tick()
	return messages.NewMessageWithClocks(msgType, "orchestrator", l, v, payload)
}

// =========================================================
// main
// =========================================================

func main() {
	fmt.Println("╔════════════════════════════════════════════╗")
	fmt.Println("║   🎯 Orquestrador Iniciado (Etapa 5/Redis) ║")
	fmt.Println("╚════════════════════════════════════════════╝")

	// Conecta ao Redis
	rc, err := redisclient.NewClient(config.RedisAddr)
	if err != nil {
		fmt.Printf("❌ Falha ao conectar ao Redis: %v\n", err)
		fmt.Println("   Certifique-se de que o Redis está rodando: docker-compose up -d redis")
		os.Exit(1)
	}
	defer rc.Close()

	orch := NewOrchestrator(rc)

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
		fmt.Printf("⚠️  Aviso: %v\n   Continuando sem registro...\n", err)
	} else {
		fmt.Println("✓ Orquestrador registrado no Name Server")
	}

	// Inicia consumidor Redis Pub/Sub em goroutine separada
	go startRedisPubSubConsumer(orch)

	// Servidor TCP para comandos críticos
	go startTCPServer(orch)

	// Servidor UDP mantido para compatibilidade com drones sem Redis
	startUDPServer(orch)
}

// =========================================================
// Redis Pub/Sub Consumer — substitui parte do servidor UDP
// =========================================================

func startRedisPubSubConsumer(orch *Orchestrator) {
	fmt.Println("✓ [Redis Pub/Sub] Aguardando mensagens nos canais drone:*")
	ch := orch.redis.Subscribe(
		redisclient.ChannelHeartbeat,
		redisclient.ChannelEmergency,
		redisclient.ChannelBatteryLow,
		redisclient.ChannelElection,
	)

	for redisMsg := range ch {
		msg, err := messages.FromJSON([]byte(redisMsg.Payload))
		if err != nil {
			fmt.Printf("[Redis Pub/Sub] ⚠️  JSON inválido no canal %s\n", redisMsg.Channel)
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

			fmt.Printf("[Redis 💓] Drone #%d | Bat:%d%% | Pos:(%.1f,%.1f) | %s | L=%d | V=%s\n",
				droneID, battery, posX, posY, state,
				msg.LamportTS, clock.FormatVector(msg.VectorClock))

		case redisclient.ChannelBatteryLow:
			droneID := int(msg.Payload["drone_id"].(float64))
			battery := int(msg.Payload["battery"].(float64))
			fmt.Printf("[Redis 🔋] ALERTA: Drone #%d bateria baixa (%d%%) L=%d\n",
				droneID, battery, msg.LamportTS)

		case redisclient.ChannelEmergency:
			droneID := int(msg.Payload["drone_id"].(float64))
			reason := msg.Payload["reason"].(string)
			fmt.Printf("[Redis 🚨] EMERGÊNCIA: Drone #%d — %s | L=%d\n",
				droneID, reason, msg.LamportTS)

		case redisclient.ChannelElection:
			droneID := int(msg.Payload["drone_id"].(float64))
			fmt.Printf("[Redis 🗳️] Candidato Drone #%d via Pub/Sub\n", droneID)
			if !orch.electionMgr.InProgress() {
				orch.electionMgr.StartElection()
				go func() {
					time.Sleep(3 * time.Second)
					leaderID := orch.electionMgr.Elect()
					orch.redis.SetLeader(leaderID)
					// Publica resultado no canal líder
					orch.redis.Publish(redisclient.ChannelLeader, map[string]interface{}{
						"leader_id": leaderID,
					})
					fmt.Printf("[Redis 👑] Líder #%d publicado no canal %s\n",
						leaderID, redisclient.ChannelLeader)
				}()
			}
			orch.electionMgr.AddCandidate(droneID)
		}
	}
}

// =========================================================
// TCP — comandos críticos
// =========================================================

func startTCPServer(orch *Orchestrator) {
	listener, err := net.Listen("tcp", config.TCPPort)
	if err != nil {
		fmt.Printf("Erro ao iniciar TCP: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()
	fmt.Printf("✓ [TCP] Escutando na porta %s\n\n", config.TCPPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("[TCP] Erro na conexão:", err)
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
	fmt.Printf("\n[TCP] 📨 %s de %s | Lamport=%d | Vector=%s\n",
		msg.Type, msg.SenderID, msg.LamportTS, clock.FormatVector(msg.VectorClock))

	switch msg.Type {

	case messages.CommandRegister:
		droneID := int(msg.Payload["drone_id"].(float64))
		fmt.Printf("  → Drone #%d solicitou registro\n", droneID)

		reply := orch.newReply(messages.EventRegistered, map[string]interface{}{
			"drone_id": droneID,
			"status":   "registered",
			"message":  "Drone registrado com sucesso",
		})
		jsonReply, _ := reply.ToJSON()
		conn.Write(jsonReply)
		fmt.Printf("  ✓ Registro confirmado para Drone #%d (L=%d)\n", droneID, orch.lamport.Value())

	case messages.CommandAssignTask:
		droneID := int(msg.Payload["drone_id"].(float64))
		taskDesc := msg.Payload["task"].(string)

		reply := orch.newReply(messages.EventTaskCompleted, map[string]interface{}{
			"drone_id": droneID,
			"task":     taskDesc,
			"status":   "assigned",
		})
		jsonReply, _ := reply.ToJSON()
		conn.Write(jsonReply)

	// ── Exclusão Mútua (Redis Sorted Set) ─────────────────────────────────
	case messages.CommandMutexRequest:
		droneID := int(msg.Payload["drone_id"].(float64))
		req := &mutex.MutexRequest{
			DroneID:     droneID,
			LamportTS:   msg.LamportTS,
			VectorClock: msg.VectorClock,
		}

		granted := orch.resMgr.RequestAccess(req)
		fmt.Printf("  [Mutex/Redis] Fila: %s\n", orch.resMgr.QueueStatus())

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

		reply := orch.newReply(messages.EventMutexGrant, map[string]interface{}{
			"drone_id": droneID,
			"next_id":  nextID,
			"queue":    orch.resMgr.QueueStatus(),
		})
		jsonReply, _ := reply.ToJSON()
		conn.Write(jsonReply)

	// ── Eleição de Líder (resultado persiste no Redis) ─────────────────────
	case messages.CommandElection:
		droneID := int(msg.Payload["drone_id"].(float64))

		if !orch.electionMgr.InProgress() {
			orch.electionMgr.StartElection()
			go func() {
				time.Sleep(3 * time.Second)
				leaderID := orch.electionMgr.Elect()
				// Persiste líder no Redis — sobrevive a reinicializações
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

		reply := orch.newReply(messages.EventLeaderElected, map[string]interface{}{
			"leader_id": leaderID,
		})
		jsonReply, _ := reply.ToJSON()
		conn.Write(jsonReply)
		fmt.Printf("  [Eleição] 👑 Líder anunciado: Drone #%d (persistido no Redis)\n", leaderID)

	default:
		fmt.Printf("  ⚠️  Tipo desconhecido: %s\n", msg.Type)
		conn.Write([]byte("ERROR: Tipo desconhecido"))
	}
}

// =========================================================
// UDP — fallback para drones sem Redis (retrocompatibilidade)
// =========================================================

func startUDPServer(orch *Orchestrator) {
	addr, _ := net.ResolveUDPAddr("udp", config.UDPPort)
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		fmt.Printf("Erro ao iniciar UDP: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Printf("✓ [UDP] Escutando na porta %s (fallback)\n", config.UDPPort)

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
				fmt.Printf("[UDP fallback] Drone #%d | Bat:%d%%\n", status.ID, status.Battery)
			}
			continue
		}

		orch.receive(msg)
		fmt.Printf("[UDP fallback] %s de %s | L=%d\n", msg.Type, msg.SenderID, msg.LamportTS)

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
		}
	}
}
