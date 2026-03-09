// internal/config/config.go

package config

import "os"

const (
	TCPPort        = ":8080"
	UDPPort        = ":8081"
	NameServerPort = ":9000"
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

var NameServerHost = getEnv("NAMESERVER_HOST", "drone-nameserver")
var ServerHost = getEnv("ORCHESTRATOR_HOST", "orchestrator")
var RedisAddr = getEnv("REDIS_ADDR", "redis:6379")
