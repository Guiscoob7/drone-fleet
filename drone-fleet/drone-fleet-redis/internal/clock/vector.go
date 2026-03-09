// internal/clock/vector.go
// Implementação de Relógio Vetorial para rastreamento de causalidade exata.

package clock

import (
	"fmt"
	"sync"
)

// VectorClock mantém o vetor de tempo de um processo.
type VectorClock struct {
	mu      sync.Mutex
	selfID  string
	entries map[string]uint64
}

// NewVectorClock cria um relógio vetorial para processID.
func NewVectorClock(processID string) *VectorClock {
	return &VectorClock{
		selfID:  processID,
		entries: map[string]uint64{processID: 0},
	}
}

// Tick incrementa a entrada própria (evento interno ou envio).
func (v *VectorClock) Tick() map[string]uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.entries[v.selfID]++
	return v.snapshot()
}

// Update sincroniza ao receber um vetor remoto W.
func (v *VectorClock) Update(remote map[string]uint64) map[string]uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	for id, t := range remote {
		if t > v.entries[id] {
			v.entries[id] = t
		}
	}
	v.entries[v.selfID]++
	return v.snapshot()
}

// Snapshot retorna uma cópia atual (thread-safe).
func (v *VectorClock) Snapshot() map[string]uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.snapshot()
}

func (v *VectorClock) snapshot() map[string]uint64 {
	cp := make(map[string]uint64, len(v.entries))
	for k, val := range v.entries {
		cp[k] = val
	}
	return cp
}

// HappensBefore retorna true se A → B.
func HappensBefore(a, b map[string]uint64) bool {
	lessOrEqual := true
	strictlyLess := false
	keys := allKeys(a, b)
	for _, k := range keys {
		av, bv := a[k], b[k]
		if av > bv {
			lessOrEqual = false
			break
		}
		if av < bv {
			strictlyLess = true
		}
	}
	return lessOrEqual && strictlyLess
}

// Concurrent retorna true se A e B são simultâneos.
func Concurrent(a, b map[string]uint64) bool {
	return !HappensBefore(a, b) && !HappensBefore(b, a)
}

// FormatVector formata o vetor para log.
func FormatVector(v map[string]uint64) string {
	s := "{"
	first := true
	for k, val := range v {
		if !first {
			s += ", "
		}
		s += fmt.Sprintf("%s:%d", k, val)
		first = false
	}
	return s + "}"
}

func allKeys(a, b map[string]uint64) []string {
	seen := make(map[string]bool)
	var keys []string
	for k := range a {
		if !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	for k := range b {
		if !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	return keys
}
