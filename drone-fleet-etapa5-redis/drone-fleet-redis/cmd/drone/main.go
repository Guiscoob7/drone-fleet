// cmd/drone/main.go
// Drone com Relógios de Lamport, Vetorial, Exclusão Mútua, Eleição de Líder
// e integração Redis (Etapa 5).
//
// Mudanças em relação à Etapa 4:
//   - Heartbeats, alertas e emergências publicados via Redis Pub/Sub
//     (antes: enviados via socket UDP diretamente ao Orquestrador)
//   - Eleição também publicada via Redis Pub/Sub além do TCP
//   - Drone escuta canal "drone:leader" para saber o resultado da eleição
//     sem precisar de resposta TCP síncrona

package main

import (
	"fmt"
	"math/rand"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/Guiscoob7/drone-fleet/internal/clock"
	"github.com/Guiscoob7/drone-fleet/internal/config"
	"github.com/Guiscoob7/drone-fleet/internal/messages"
	"github.com/Guiscoob7/drone-fleet/internal/models"
	redisclient "github.com/Guiscoob7/drone-fleet/internal/redis"
	"github.com/Guiscoob7/drone-fleet/pkg/nameserver"
)

// Drone encapsula estado + relógios lógicos de um único drone.
type Drone struct {
	id      int
	name    string
	lamport *clock.LamportClock
	vector  *clock.VectorClock
	status  models.DroneStatus
	redis   *redisclient.Client
}

func newDrone(id int, rc *redisclient.Client) *Drone {
	name := fmt.Sprintf("drone-%d", id)
	return &Drone{
		id:      id,
		name:    name,
		lamport: clock.NewLamportClock(),
		vector:  clock.NewVectorClock(name),
		redis:   rc,
		status: models.DroneStatus{
			ID:        id,
			Battery:   100,
			PositionX: 0,
			PositionY: 0,
			State:     "IDLE",
		},
	}
}

// buildMsg cria mensagem com carimbos lógicos atualizados.
func (d *Drone) buildMsg(msgType messages.MessageType, payload map[string]interface{}) *messages.Message {
	l := d.lamport.Tick()
	v := d.vector.Tick()
	return messages.NewMessageWithClocks(msgType, d.name, l, v, payload)
}

// receive atualiza os relógios ao processar mensagem recebida.
func (d *Drone) receive(msg *messages.Message) {
	d.lamport.Update(msg.LamportTS)
	if msg.VectorClock != nil {
		d.vector.Update(msg.VectorClock)
	}
}

// publishToRedis publica mensagem em canal Redis.
func (d *Drone) publishToRedis(channel string, msgType messages.MessageType, payload map[string]interface{}) {
	msg := d.buildMsg(msgType, payload)
	if err := d.redis.Publish(channel, msg); err != nil {
		fmt.Printf("[Drone #%d] ⚠️  Erro ao publicar no Redis (%s): %v\n", d.id, channel, err)
	}
}

func main() {
	droneID := rand.Intn(100)
	if len(os.Args) > 1 {
		id, _ := strconv.Atoi(os.Args[1])
		droneID = id
	}

	fmt.Printf("\n╔═══════════════════════════════════════════╗\n")
	fmt.Printf("║   🚁 Iniciando Drone #%-3d  (Etapa 5)      ║\n", droneID)
	fmt.Printf("╚═══════════════════════════════════════════╝\n\n")

	// Conecta ao Redis
	rc, err := redisclient.NewClient(config.RedisAddr)
	if err != nil {
		fmt.Printf("⚠️  Redis indisponível: %v\n   Continuando sem Redis...\n\n", err)
		rc = nil
	}
	if rc != nil {
		defer rc.Close()
	}

	d := newDrone(droneID, rc)

	// 1. Descobre o orquestrador via Name Server
	nsClient := nameserver.NewClient()
	time.Sleep(2 * time.Second)

	fmt.Println("[1/4] 🔍 Buscando orquestrador no Name Server...")
	orchInfo, err := nsClient.Lookup("orchestrator")
	if err != nil {
		fmt.Printf("⚠️  Aviso: %v\n   Usando configuração padrão...\n\n", err)
	} else {
		fmt.Printf("✓ Orquestrador em: %s\n\n", orchInfo.Address)
	}

	// 2. Registra no Name Server
	nsClient.Register(messages.ServiceInfo{
		Name:    d.name,
		Type:    string(messages.ServiceDrone),
		Address: "localhost:0",
	})
	fmt.Printf("✓ Drone #%d registrado no Name Server\n\n", droneID)

	// 3. Registro no Orquestrador via TCP
	fmt.Println("[2/4] 📡 Registrando no Orquestrador (TCP)...")
	d.registerWithOrchestrator()

	// 4. Participa da eleição
	fmt.Println("[3/4] 🗳️  Participando da eleição de líder...")
	d.participateInElection()

	// 5. Loop principal: heartbeats via Redis Pub/Sub
	fmt.Println("[4/4] 💓 Iniciando loop de heartbeats (Redis Pub/Sub)...\n")
	fmt.Println("═══════════════════════════════════════════════════\n")
	d.startHeartbeatLoop()
}

// registerWithOrchestrator envia CMD_REGISTER via TCP.
func (d *Drone) registerWithOrchestrator() {
	conn, err := net.Dial("tcp", config.ServerHost+config.TCPPort)
	if err != nil {
		fmt.Printf("[Erro] Não foi possível conectar ao Orquestrador: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	msg := d.buildMsg(messages.CommandRegister, map[string]interface{}{
		"drone_id": d.id,
		"type":     "quadcopter",
		"version":  "2.0-redis",
	})
	data, _ := msg.ToJSON()
	conn.Write(data)

	buffer := make([]byte, 2048)
	n, _ := conn.Read(buffer)
	response, err := messages.FromJSON(buffer[:n])
	if err == nil {
		d.receive(response)
		fmt.Printf("✓ [TCP] Resposta: tipo=%s status=%v lamport=%d\n\n",
			response.Type, response.Payload["status"], response.LamportTS)
	} else {
		fmt.Printf("✓ [TCP] Resposta: %s\n\n", string(buffer[:n]))
	}
}

// participateInElection envia candidatura via TCP E via Redis Pub/Sub.
func (d *Drone) participateInElection() {
	// Via TCP (caminho principal)
	conn, err := net.Dial("tcp", config.ServerHost+config.TCPPort)
	if err != nil {
		fmt.Printf("[Eleição] ⚠️  Não foi possível conectar TCP: %v\n", err)
	} else {
		defer conn.Close()
		msg := d.buildMsg(messages.CommandElection, map[string]interface{}{"drone_id": d.id})
		data, _ := msg.ToJSON()
		conn.Write(data)

		buffer := make([]byte, 2048)
		n, _ := conn.Read(buffer)
		response, err := messages.FromJSON(buffer[:n])
		if err == nil {
			d.receive(response)
			if response.Type == messages.EventLeaderElected {
				leaderID := int(response.Payload["leader_id"].(float64))
				if leaderID == d.id {
					fmt.Printf("👑 [Drone #%d] SOU O LÍDER!\n\n", d.id)
				} else {
					fmt.Printf("[Drone #%d] Líder eleito: Drone #%d\n\n", d.id, leaderID)
				}
			}
		}
	}

	// Via Redis Pub/Sub (redundância — permite que o orquestrador saiba sem TCP)
	if d.redis != nil {
		d.publishToRedis(redisclient.ChannelElection, messages.CommandElection, map[string]interface{}{
			"drone_id": d.id,
		})
		fmt.Printf("[Drone #%d] ✓ Candidatura publicada no Redis\n\n", d.id)
	}
}

// requestMutexAccess envia CMD_MUTEX_REQUEST via TCP.
func (d *Drone) requestMutexAccess() bool {
	conn, err := net.Dial("tcp", config.ServerHost+config.TCPPort)
	if err != nil {
		fmt.Printf("[Mutex] ⚠️  Não foi possível conectar: %v\n", err)
		return false
	}
	defer conn.Close()

	msg := d.buildMsg(messages.CommandMutexRequest, map[string]interface{}{"drone_id": d.id})
	data, _ := msg.ToJSON()
	conn.Write(data)

	buffer := make([]byte, 2048)
	n, _ := conn.Read(buffer)
	response, err := messages.FromJSON(buffer[:n])
	if err == nil {
		d.receive(response)
		fmt.Printf("[Drone #%d] 🔑 Mutex resposta: %s (lamport=%d)\n",
			d.id, response.Type, response.LamportTS)
		return response.Type == messages.EventMutexGrant
	}
	return false
}

// releaseMutex envia CMD_MUTEX_RELEASE via TCP.
func (d *Drone) releaseMutex() {
	conn, err := net.Dial("tcp", config.ServerHost+config.TCPPort)
	if err != nil {
		return
	}
	defer conn.Close()

	msg := d.buildMsg(messages.CommandMutexRelease, map[string]interface{}{"drone_id": d.id})
	data, _ := msg.ToJSON()
	conn.Write(data)
	fmt.Printf("[Drone #%d] 🔓 Recurso liberado\n", d.id)
}

// startHeartbeatLoop publica telemetria via Redis Pub/Sub (nova implementação).
// Fallback: continua suportando UDP se Redis indisponível.
func (d *Drone) startHeartbeatLoop() {
	// UDP fallback se Redis indisponível
	var udpConn *net.UDPConn
	if d.redis == nil {
		serverAddr, _ := net.ResolveUDPAddr("udp", config.ServerHost+config.UDPPort)
		udpConn, _ = net.DialUDP("udp", nil, serverAddr)
		if udpConn != nil {
			defer udpConn.Close()
			fmt.Println("[Drone] Usando UDP fallback (Redis indisponível)")
		}
	} else {
		fmt.Println("[Drone] Usando Redis Pub/Sub para telemetria ✓")
	}

	alertSent := false
	emergencySent := false
	tick := 0

	for d.status.Battery > 0 {
		tick++
		d.status.Battery -= 1
		d.status.PositionX += 1.5
		d.status.PositionY += 0.5

		// Atualiza estado e envia alertas
		switch {
		case d.status.Battery <= 10 && !emergencySent:
			d.status.State = "EMERGENCY"
			emergencyPayload := map[string]interface{}{
				"drone_id": d.id,
				"battery":  d.status.Battery,
				"reason":   "Bateria criticamente baixa",
			}
			if d.redis != nil {
				d.publishToRedis(redisclient.ChannelEmergency, messages.EventEmergency, emergencyPayload)
			} else if udpConn != nil {
				msg := d.buildMsg(messages.EventEmergency, emergencyPayload)
				data, _ := msg.ToJSON()
				udpConn.Write(data)
			}
			fmt.Printf("🚨 [Drone #%d] EMERGÊNCIA enviada! (lamport=%d)\n", d.id, d.lamport.Value())
			emergencySent = true

		case d.status.Battery <= 20 && !alertSent:
			d.status.State = "LOW_BATTERY"
			lowPayload := map[string]interface{}{
				"drone_id": d.id,
				"battery":  d.status.Battery,
			}
			if d.redis != nil {
				d.publishToRedis(redisclient.ChannelBatteryLow, messages.EventBatteryLow, lowPayload)
			} else if udpConn != nil {
				msg := d.buildMsg(messages.EventBatteryLow, lowPayload)
				data, _ := msg.ToJSON()
				udpConn.Write(data)
			}
			fmt.Printf("🔋 [Drone #%d] Alerta bateria baixa (lamport=%d | vector=%s)\n",
				d.id, d.lamport.Value(), clock.FormatVector(d.vector.Snapshot()))
			alertSent = true

		case d.status.Battery > 20:
			d.status.State = "BUSY"
		}

		// A cada 10 heartbeats tenta acessar recurso compartilhado (mutex Redis)
		if tick%10 == 0 && d.status.Battery > 15 {
			fmt.Printf("\n[Drone #%d] 🔑 Tentando acessar recurso compartilhado...\n", d.id)
			granted := d.requestMutexAccess()
			if granted {
				fmt.Printf("[Drone #%d] ✅ Acessando recurso (seção crítica)\n", d.id)
				time.Sleep(1 * time.Second)
				d.releaseMutex()
			}
			fmt.Println()
		}

		// Heartbeat via Redis Pub/Sub (substitui UDP direto)
		heartbeatPayload := map[string]interface{}{
			"drone_id":   d.id,
			"battery":    d.status.Battery,
			"position_x": d.status.PositionX,
			"position_y": d.status.PositionY,
			"state":      d.status.State,
		}

		if d.redis != nil {
			d.publishToRedis(redisclient.ChannelHeartbeat, messages.EventHeartbeat, heartbeatPayload)
		} else if udpConn != nil {
			msg := d.buildMsg(messages.EventHeartbeat, heartbeatPayload)
			data, _ := msg.ToJSON()
			udpConn.Write(data)
		}

		icon := "✓"
		if d.status.Battery <= 20 {
			icon = "⚠️ "
		}
		if d.status.Battery <= 10 {
			icon = "🚨"
		}

		transport := "Redis"
		if d.redis == nil {
			transport = "UDP"
		}
		fmt.Printf("%s [Drone #%d] Heartbeat(%s) | Bat:%d%% Pos:(%.1f,%.1f) L=%d V=%s %s\n",
			icon, d.id, transport, d.status.Battery,
			d.status.PositionX, d.status.PositionY,
			d.lamport.Value(),
			clock.FormatVector(d.vector.Snapshot()),
			d.status.State)

		time.Sleep(2 * time.Second)
	}

	fmt.Printf("\n❌ [Drone #%d] Bateria esgotada. Desligando...\n", d.id)
}
