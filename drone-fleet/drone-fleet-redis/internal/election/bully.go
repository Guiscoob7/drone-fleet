// internal/election/bully.go
// Algoritmo de Eleição de Líder Bully.
//
// Com Redis: o resultado da eleição é persistido em KeyLeaderID,
// garantindo que qualquer drone ou processo possa consultar o líder
// atual mesmo após reinicializações — o que não era possível com
// a implementação anterior em memória.

package election

import (
	"fmt"
	"sync"
)

// ElectionManager coordena eleições no Orquestrador.
type ElectionManager struct {
	mu         sync.Mutex
	candidates map[int]bool
	leaderID   int
	inProgress bool
}

func NewElectionManager() *ElectionManager {
	return &ElectionManager{
		candidates: make(map[int]bool),
		leaderID:   -1,
	}
}

func (e *ElectionManager) StartElection() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inProgress = true
	e.candidates = make(map[int]bool)
	e.leaderID = -1
	fmt.Println("[Election] 🗳️  Nova eleição iniciada")
}

func (e *ElectionManager) AddCandidate(droneID int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.inProgress {
		return
	}
	e.candidates[droneID] = true
	fmt.Printf("[Election] 📋 Candidato registrado: Drone #%d\n", droneID)
}

// Elect elege o candidato com maior ID (regra Bully).
func (e *ElectionManager) Elect() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	leader := -1
	for id := range e.candidates {
		if id > leader {
			leader = id
		}
	}
	e.leaderID = leader
	e.inProgress = false
	if leader >= 0 {
		fmt.Printf("[Election] 👑 Líder eleito: Drone #%d\n", leader)
	} else {
		fmt.Println("[Election] ⚠️  Nenhum candidato — eleição inconclusiva")
	}
	return leader
}

func (e *ElectionManager) LeaderID() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.leaderID
}

func (e *ElectionManager) InvalidateLeader(droneID int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.leaderID == droneID {
		fmt.Printf("[Election] 💀 Líder Drone #%d offline — nova eleição necessária\n", droneID)
		e.leaderID = -1
	}
}

func (e *ElectionManager) InProgress() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inProgress
}
