// pkg/nameserver/client.go

package nameserver

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/Guiscoob7/drone-fleet/internal/config"
	"github.com/Guiscoob7/drone-fleet/internal/messages"
)

// Client é um cliente para interagir com o Name Server
type Client struct {
	serverAddr string
}

func NewClient() *Client {
	return &Client{
		serverAddr: config.NameServerHost + config.NameServerPort,
	}
}

func (c *Client) Register(info messages.ServiceInfo) error {
	conn, err := net.Dial("tcp", c.serverAddr)
	if err != nil {
		return fmt.Errorf("falha ao conectar ao Name Server: %v", err)
	}
	defer conn.Close()

	data := map[string]interface{}{
		"action":  "REGISTER",
		"service": info,
	}
	jsonData, _ := json.Marshal(data)
	conn.Write(jsonData)

	buffer := make([]byte, 1024)
	n, err := conn.Read(buffer)
	if err != nil {
		return err
	}
	fmt.Printf("[NameServer Client] Resposta: %s\n", string(buffer[:n]))
	return nil
}

func (c *Client) Lookup(serviceName string) (*messages.ServiceInfo, error) {
	conn, err := net.Dial("tcp", c.serverAddr)
	if err != nil {
		return nil, fmt.Errorf("falha ao conectar ao Name Server: %v", err)
	}
	defer conn.Close()

	data := map[string]interface{}{
		"action": "LOOKUP",
		"name":   serviceName,
	}
	jsonData, _ := json.Marshal(data)
	conn.Write(jsonData)

	buffer := make([]byte, 1024)
	n, err := conn.Read(buffer)
	if err != nil {
		return nil, err
	}

	var response messages.ServiceInfo
	if err := json.Unmarshal(buffer[:n], &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) ListAll() ([]messages.ServiceInfo, error) {
	conn, err := net.Dial("tcp", c.serverAddr)
	if err != nil {
		return nil, fmt.Errorf("falha ao conectar ao Name Server: %v", err)
	}
	defer conn.Close()

	data := map[string]interface{}{"action": "LIST"}
	jsonData, _ := json.Marshal(data)
	conn.Write(jsonData)

	buffer := make([]byte, 4096)
	n, err := conn.Read(buffer)
	if err != nil {
		return nil, err
	}

	var services []messages.ServiceInfo
	json.Unmarshal(buffer[:n], &services)
	return services, nil
}
