// cmd/nameserver/main.go

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/Guiscoob7/drone-fleet/internal/config"
	"github.com/Guiscoob7/drone-fleet/internal/messages"
)

type NameServer struct {
	services map[string]messages.ServiceInfo
	mu       sync.RWMutex
}

func NewNameServer() *NameServer {
	return &NameServer{services: make(map[string]messages.ServiceInfo)}
}

func (ns *NameServer) Register(info messages.ServiceInfo) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.services[info.Name] = info
	fmt.Printf("[NameServer] ✓ Serviço registrado: %s (%s) em %s\n", info.Name, info.Type, info.Address)
}

func (ns *NameServer) Lookup(name string) (*messages.ServiceInfo, bool) {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	service, exists := ns.services[name]
	return &service, exists
}

func (ns *NameServer) ListAll() []messages.ServiceInfo {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	list := make([]messages.ServiceInfo, 0, len(ns.services))
	for _, svc := range ns.services {
		list = append(list, svc)
	}
	return list
}

func main() {
	fmt.Println("╔═══════════════════════════════════════╗")
	fmt.Println("║   🌐 Name Server Iniciado              ║")
	fmt.Println("╚═══════════════════════════════════════╝")

	ns := NewNameServer()

	listener, err := net.Listen("tcp", config.NameServerPort)
	if err != nil {
		fmt.Printf("Erro ao iniciar Name Server: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()

	fmt.Printf("[NameServer] Escutando na porta %s\n", config.NameServerPort)
	fmt.Println("[NameServer] Aguardando registros de serviços...\n")

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("[NameServer] Erro na conexão:", err)
			continue
		}
		go handleConnection(conn, ns)
	}
}

func handleConnection(conn net.Conn, ns *NameServer) {
	defer conn.Close()
	buffer := make([]byte, 4096)
	n, err := conn.Read(buffer)
	if err != nil {
		return
	}

	var request map[string]interface{}
	if err := json.Unmarshal(buffer[:n], &request); err != nil {
		conn.Write([]byte("ERROR: Invalid JSON"))
		return
	}

	action, _ := request["action"].(string)
	switch action {
	case "REGISTER":
		serviceData, _ := json.Marshal(request["service"])
		var info messages.ServiceInfo
		json.Unmarshal(serviceData, &info)
		ns.Register(info)
		conn.Write([]byte(fmt.Sprintf("OK: Serviço '%s' registrado com sucesso", info.Name)))
	case "LOOKUP":
		name, _ := request["name"].(string)
		service, exists := ns.Lookup(name)
		if exists {
			data, _ := json.Marshal(service)
			conn.Write(data)
		} else {
			conn.Write([]byte("ERROR: Serviço não encontrado"))
		}
	case "LIST":
		services := ns.ListAll()
		data, _ := json.Marshal(services)
		conn.Write(data)
	default:
		conn.Write([]byte("ERROR: Ação desconhecida"))
	}
}
