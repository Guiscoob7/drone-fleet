// internal/clock/lamport.go
// Implementação do Relógio de Lamport para ordenação causal de eventos.
//
// Regras:
//   - Evento interno:  L = L + 1
//   - Envio de msg:    L = L + 1  (antes de enviar)
//   - Recebimento:     L = max(L, L_remetente) + 1

package clock

import "sync"

// LamportClock mantém o contador lógico de um processo.
type LamportClock struct {
	mu    sync.Mutex
	value uint64
}

// NewLamportClock cria um relógio iniciado em zero.
func NewLamportClock() *LamportClock {
	return &LamportClock{}
}

// Tick incrementa o relógio (evento interno ou envio).
// Retorna o novo valor para ser anexado à mensagem.
func (l *LamportClock) Tick() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.value++
	return l.value
}

// Update sincroniza ao receber uma mensagem com carimbo remoto.
// Aplica: L = max(L, remote) + 1
func (l *LamportClock) Update(remote uint64) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if remote > l.value {
		l.value = remote
	}
	l.value++
	return l.value
}

// Value retorna o valor atual sem modificar.
func (l *LamportClock) Value() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.value
}
