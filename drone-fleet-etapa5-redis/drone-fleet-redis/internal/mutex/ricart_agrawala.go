// internal/mutex/ricart_agrawala.go
// Exclusão mútua distribuída — Ricart-Agrawala com Redis Sorted Set.
//
// Melhoria em relação à Etapa 4:
//   Antes: fila em memória (slice) no Orquestrador → perdida ao reiniciar.
//   Agora: fila persistida no Redis como Sorted Set (score = LamportTS).
//         → Resiliente a falhas; qualquer instância pode consultar a fila.
//         → ZADD é atômico → sem race conditions entre goroutines.
//
// A semântica de Ricart-Agrawala é preservada:
//   - Menor timestamp Lamport = maior prioridade (FIFO causal)
//   - Empate desfeito por menor DroneID

package mutex

import (
	"fmt"
	"sync"

	redisclient "github.com/Guiscoob7/drone-fleet/internal/redis"
)

// MutexRequest representa um pedido de acesso ao recurso.
type MutexRequest struct {
	DroneID     int
	LamportTS   uint64
	VectorClock map[string]uint64
}

// ResourceManager gerencia exclusão mútua usando Redis como backend.
type ResourceManager struct {
	mu          sync.Mutex
	redisClient *redisclient.Client
	totalDrones int
}

// NewResourceManager cria o gerenciador com suporte a Redis.
func NewResourceManager(rc *redisclient.Client) *ResourceManager {
	return &ResourceManager{
		redisClient: rc,
	}
}

func (rm *ResourceManager) SetTotalDrones(n int) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.totalDrones = n
}

// RequestAccess registra pedido no Redis Sorted Set e verifica se pode conceder.
// Retorna true se o acesso é concedido imediatamente.
func (rm *ResourceManager) RequestAccess(req *MutexRequest) bool {
	fmt.Printf("[Mutex] 🔑 REQUEST de Drone #%d  (Lamport=%d)\n",
		req.DroneID, req.LamportTS)

	// Adiciona à fila Redis (atômico via ZADD)
	if err := rm.redisClient.EnqueueMutex(req.DroneID, req.LamportTS); err != nil {
		fmt.Printf("[Mutex] ⚠️  Erro ao enfileirar no Redis: %v\n", err)
		return false
	}

	// Verifica dono atual
	owner, _ := rm.redisClient.GetMutexOwner()
	if owner != "" {
		fmt.Printf("[Mutex] ⏳ Drone #%d aguarda — recurso com Drone #%s\n",
			req.DroneID, owner)
		return false
	}

	// Verifica se este pedido é o de maior prioridade na fila
	topID, _, err := rm.redisClient.PeekMutexQueue()
	if err != nil || topID != req.DroneID {
		fmt.Printf("[Mutex] ⏳ Drone #%d aguarda na fila\n", req.DroneID)
		return false
	}

	// Concede acesso — persiste no Redis
	if err := rm.redisClient.SetMutexOwner(req.DroneID); err != nil {
		fmt.Printf("[Mutex] ⚠️  Erro ao definir dono no Redis: %v\n", err)
		return false
	}

	fmt.Printf("[Mutex] ✅ GRANT para Drone #%d (recurso livre, maior prioridade)\n",
		req.DroneID)
	return true
}

// ReleaseAccess libera o recurso e promove o próximo da fila.
func (rm *ResourceManager) ReleaseAccess(droneID int) int {
	fmt.Printf("[Mutex] 🔓 Drone #%d liberou o recurso\n", droneID)

	// Remove da fila Redis
	rm.redisClient.DequeueMutex(droneID)
	rm.redisClient.ClearMutexOwner()

	// Verifica próximo da fila
	nextID, _, err := rm.redisClient.PeekMutexQueue()
	if err != nil || nextID == -1 {
		fmt.Println("[Mutex] Fila vazia — recurso livre")
		return -1
	}

	// Concede ao próximo
	rm.redisClient.SetMutexOwner(nextID)
	fmt.Printf("[Mutex] ✅ GRANT automático para Drone #%d (próximo da fila)\n", nextID)
	return nextID
}

// QueueStatus retorna snapshot da fila para logging.
func (rm *ResourceManager) QueueStatus() string {
	return rm.redisClient.MutexQueueSnapshot()
}

// CurrentOwner retorna o detentor atual do recurso.
func (rm *ResourceManager) CurrentOwner() string {
	owner, _ := rm.redisClient.GetMutexOwner()
	if owner == "" {
		return "(livre)"
	}
	return owner
}
