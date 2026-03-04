// internal/redis/client.go
// Wrapper sobre o cliente Redis — ponto central de integração de middleware.
//
// O Redis substitui/aprimora três partes críticas do sistema:
//   1. Pub/Sub  → substitui envio UDP direto de heartbeats (broadcast assíncrono)
//   2. Hashes   → persiste estado dos drones (antes: apenas em memória do orquestrador)
//   3. Sorted Set → fila de mutex com prioridade Lamport (antes: slice em memória)

package redisclient

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// Canais Pub/Sub
	ChannelHeartbeat  = "drone:heartbeat"
	ChannelEmergency  = "drone:emergency"
	ChannelBatteryLow = "drone:battery_low"
	ChannelElection   = "drone:election"
	ChannelLeader     = "drone:leader"

	// Chaves de estado
	KeyDronePrefix  = "drone:status:"  // HASH  drone:status:<id>
	KeyDroneSet     = "drone:active"   // SET   de IDs ativos
	KeyMutexQueue   = "mutex:queue"    // ZSET  score = LamportTS
	KeyMutexOwner   = "mutex:owner"    // STRING id do detentor atual
	KeyLeaderID     = "election:leader" // STRING ID do líder atual
	KeyLamportGlobal = "clock:lamport" // STRING contador global de Lamport

	// TTL para entradas de drone (considera drone morto se não atualizar)
	DroneTTL = 30 * time.Second
)

// Client encapsula o cliente Redis e oferece operações de alto nível.
type Client struct {
	rdb *redis.Client
	ctx context.Context
}

// NewClient cria a conexão com o Redis.
func NewClient(addr string) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: "",
		DB:       0,
	})

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("falha ao conectar ao Redis em %s: %w", addr, err)
	}

	fmt.Printf("[Redis] ✓ Conectado ao Redis em %s\n", addr)
	return &Client{rdb: rdb, ctx: ctx}, nil
}

// Close encerra a conexão.
func (c *Client) Close() {
	c.rdb.Close()
}

// ─────────────────────────────────────────────
// Pub/Sub
// ─────────────────────────────────────────────

// Publish publica uma mensagem em um canal.
func (c *Client) Publish(channel string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.rdb.Publish(c.ctx, channel, data).Err()
}

// Subscribe retorna um canal Go que recebe mensagens do canal Redis.
func (c *Client) Subscribe(channels ...string) <-chan *redis.Message {
	sub := c.rdb.Subscribe(c.ctx, channels...)
	ch := sub.Channel()
	return ch
}

// ─────────────────────────────────────────────
// Estado dos drones (HASH + TTL)
// ─────────────────────────────────────────────

// SaveDroneStatus persiste o status do drone no Redis com TTL.
func (c *Client) SaveDroneStatus(id int, fields map[string]interface{}) error {
	key := fmt.Sprintf("%s%d", KeyDronePrefix, id)
	pipe := c.rdb.Pipeline()
	pipe.HSet(c.ctx, key, fields)
	pipe.Expire(c.ctx, key, DroneTTL)
	pipe.SAdd(c.ctx, KeyDroneSet, id)
	_, err := pipe.Exec(c.ctx)
	return err
}

// GetDroneStatus lê o status de um drone do Redis.
func (c *Client) GetDroneStatus(id int) (map[string]string, error) {
	key := fmt.Sprintf("%s%d", KeyDronePrefix, id)
	return c.rdb.HGetAll(c.ctx, key).Result()
}

// GetActiveDrones retorna lista de IDs de drones ativos.
func (c *Client) GetActiveDrones() ([]string, error) {
	return c.rdb.SMembers(c.ctx, KeyDroneSet).Result()
}

// ─────────────────────────────────────────────
// Fila de Mutex (Sorted Set — score = LamportTS)
// ─────────────────────────────────────────────

// EnqueueMutex adiciona drone à fila de mutex com prioridade Lamport.
// Score menor = maior prioridade (FIFO causal).
func (c *Client) EnqueueMutex(droneID int, lamportTS uint64) error {
	member := fmt.Sprintf("%d", droneID)
	return c.rdb.ZAdd(c.ctx, KeyMutexQueue, redis.Z{
		Score:  float64(lamportTS),
		Member: member,
	}).Err()
}

// PeekMutexQueue retorna o drone com maior prioridade (sem remover).
// Retorna droneID e seu LamportTS, ou -1 se a fila estiver vazia.
func (c *Client) PeekMutexQueue() (int, float64, error) {
	results, err := c.rdb.ZRangeWithScores(c.ctx, KeyMutexQueue, 0, 0).Result()
	if err != nil || len(results) == 0 {
		return -1, 0, err
	}
	var droneID int
	fmt.Sscanf(results[0].Member.(string), "%d", &droneID)
	return droneID, results[0].Score, nil
}

// DequeueMutex remove o drone da fila de mutex.
func (c *Client) DequeueMutex(droneID int) error {
	member := fmt.Sprintf("%d", droneID)
	return c.rdb.ZRem(c.ctx, KeyMutexQueue, member).Err()
}

// MutexQueueLen retorna o tamanho da fila.
func (c *Client) MutexQueueLen() (int64, error) {
	return c.rdb.ZCard(c.ctx, KeyMutexQueue).Result()
}

// GetMutexOwner retorna o detentor atual do mutex ("" = livre).
func (c *Client) GetMutexOwner() (string, error) {
	val, err := c.rdb.Get(c.ctx, KeyMutexOwner).Result()
	if err == redis.Nil {
		return "", nil
	}
	return val, err
}

// SetMutexOwner define o detentor do mutex (com TTL de segurança de 10s).
func (c *Client) SetMutexOwner(droneID int) error {
	val := fmt.Sprintf("%d", droneID)
	return c.rdb.Set(c.ctx, KeyMutexOwner, val, 10*time.Second).Err()
}

// ClearMutexOwner libera o mutex.
func (c *Client) ClearMutexOwner() error {
	return c.rdb.Del(c.ctx, KeyMutexOwner).Err()
}

// MutexQueueSnapshot retorna snapshot formatado da fila.
func (c *Client) MutexQueueSnapshot() string {
	results, err := c.rdb.ZRangeWithScores(c.ctx, KeyMutexQueue, 0, -1).Result()
	if err != nil || len(results) == 0 {
		return "(vazia)"
	}
	s := ""
	for i, z := range results {
		if i > 0 {
			s += " → "
		}
		s += fmt.Sprintf("Drone#%s(L=%.0f)", z.Member, z.Score)
	}
	return s
}

// ─────────────────────────────────────────────
// Eleição de Líder
// ─────────────────────────────────────────────

// SetLeader persiste o ID do líder eleito.
func (c *Client) SetLeader(droneID int) error {
	return c.rdb.Set(c.ctx, KeyLeaderID, droneID, 0).Err()
}

// GetLeader retorna o ID do líder atual (-1 se não houver).
func (c *Client) GetLeader() (int, error) {
	val, err := c.rdb.Get(c.ctx, KeyLeaderID).Result()
	if err == redis.Nil {
		return -1, nil
	}
	if err != nil {
		return -1, err
	}
	var id int
	fmt.Sscanf(val, "%d", &id)
	return id, nil
}

// ─────────────────────────────────────────────
// Relógio de Lamport Global (Redis INCR — atômico)
// ─────────────────────────────────────────────

// GlobalLamportTick incrementa atomicamente o contador global de Lamport.
// Usado pelo orquestrador para garantir timestamps únicos no cluster.
func (c *Client) GlobalLamportTick() (int64, error) {
	return c.rdb.Incr(c.ctx, KeyLamportGlobal).Result()
}

// GetGlobalLamport lê o valor atual do relógio global.
func (c *Client) GetGlobalLamport() (int64, error) {
	val, err := c.rdb.Get(c.ctx, KeyLamportGlobal).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var ts int64
	fmt.Sscanf(val, "%d", &ts)
	return ts, nil
}
