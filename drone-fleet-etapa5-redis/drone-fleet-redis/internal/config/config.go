// internal/config/config.go

package config

const (
	// TCPPort é usada para comandos críticos (Registro, Mutex, Eleição)
	TCPPort = ":8080"
	// UDPPort mantida para compatibilidade (heartbeats agora vão via Redis Pub/Sub)
	UDPPort = ":8081"
	// NameServerPort é usada pelo servidor de nomes para descoberta de serviços
	NameServerPort = ":9000"
	// ServerHost define onde o Orquestrador roda
	ServerHost = "localhost"
	// RedisAddr endereço do servidor Redis
	RedisAddr = "localhost:6379"
)
