// internal/messages/message.go
// Estrutura base de mensagem com suporte a relógios lógicos.

package messages

import (
	"encoding/json"
	"time"
)

// Message é a estrutura base para todas as mensagens do sistema.
type Message struct {
	Type        MessageType            `json:"type"`
	SenderID    string                 `json:"sender_id"`
	Timestamp   time.Time              `json:"timestamp"`
	LamportTS   uint64                 `json:"lamport_ts"`
	VectorClock map[string]uint64      `json:"vector_clock"`
	Payload     map[string]interface{} `json:"payload"`
}

// NewMessage cria uma mensagem sem carimbos lógicos.
func NewMessage(msgType MessageType, senderID string, payload map[string]interface{}) *Message {
	return &Message{
		Type:      msgType,
		SenderID:  senderID,
		Timestamp: time.Now(),
		Payload:   payload,
	}
}

// NewMessageWithClocks cria uma mensagem com carimbos lógicos.
func NewMessageWithClocks(
	msgType MessageType,
	senderID string,
	lamport uint64,
	vector map[string]uint64,
	payload map[string]interface{},
) *Message {
	return &Message{
		Type:        msgType,
		SenderID:    senderID,
		Timestamp:   time.Now(),
		LamportTS:   lamport,
		VectorClock: vector,
		Payload:     payload,
	}
}

// ToJSON serializa a mensagem para JSON
func (m *Message) ToJSON() ([]byte, error) {
	return json.Marshal(m)
}

// FromJSON desserializa JSON para Message
func FromJSON(data []byte) (*Message, error) {
	var msg Message
	err := json.Unmarshal(data, &msg)
	return &msg, err
}

// GetPayloadValue retorna um valor específico do payload
func (m *Message) GetPayloadValue(key string) (interface{}, bool) {
	val, exists := m.Payload[key]
	return val, exists
}
