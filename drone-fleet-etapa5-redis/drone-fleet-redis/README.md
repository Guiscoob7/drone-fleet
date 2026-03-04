# drone-fleet — Etapa 5: Integração Redis como Middleware

## Visão Geral

Este projeto implementa um sistema distribuído de frota de drones com integração do **Redis** como middleware central na Etapa 5.

---

## Estrutura do Projeto

```
drone-fleet/
├── cmd/
│   ├── drone/          # Processo drone (múltiplas instâncias)
│   │   └── main.go
│   ├── nameserver/     # Servidor de nomes (descoberta de serviços)
│   │   └── main.go
│   └── orchestrator/   # Orquestrador central
│       └── main.go
├── internal/
│   ├── clock/
│   │   ├── lamport.go  # Relógio de Lamport
│   │   └── vector.go   # Relógio Vetorial
│   ├── config/
│   │   └── config.go   # Portas e endereços (inclui Redis)
│   ├── election/
│   │   └── bully.go    # Algoritmo Bully de eleição de líder
│   ├── messages/
│   │   ├── message.go  # Estrutura base com carimbos lógicos
│   │   └── types.go    # Tipos de mensagens e constantes
│   ├── models/
│   │   └── drone_data.go
│   ├── mutex/
│   │   └── ricart_agrawala.go  # Exclusão mútua (agora com Redis)
│   └── redis/
│       └── client.go   # ★ NOVO: Wrapper do cliente Redis
├── pkg/
│   └── nameserver/
│       └── client.go
├── docker/
│   └── docker-compose.yml  # Redis + Redis Commander UI
├── Makefile
├── go.mod
└── README.md
```

---

## Middleware Escolhido: Redis

### Por que Redis?

O Redis foi integrado para resolver três limitações críticas da implementação anterior (Etapa 4):

| Problema (Etapa 4) | Solução com Redis |
|---|---|
| Heartbeats via UDP direto → acoplamento forte | **Pub/Sub** → drones publicam em canais, orquestrador assina |
| Estado dos drones apenas em memória → perdido ao reiniciar | **Hashes com TTL** → estado persiste, drone "morto" expira automaticamente |
| Fila de mutex em slice Go → não sobrevive a falhas | **Sorted Set** (score = LamportTS) → fila persiste e é consultável |
| Líder eleito em memória → perdido ao reiniciar | **String Redis** → líder persiste além do processo |
| Sem contador de Lamport global | **INCR atômico** → timestamps únicos no cluster |

---

## Como Executar

### Pré-requisitos

- Go 1.21+
- Docker + Docker Compose

### Passo 1 — Subir o Redis

```bash
make redis
# ou manualmente:
docker-compose -f docker/docker-compose.yml up -d redis
```

O Redis estará disponível em `localhost:6379`.  
A UI Redis Commander estará em `http://localhost:8082`.

### Passo 2 — Instalar dependências Go

```bash
make deps
# ou:
go mod tidy && go mod download
```

### Passo 3 — Abrir 5 terminais e executar na ordem:

**Terminal 1 — Name Server:**
```bash
make nameserver
# ou: go run ./cmd/nameserver
```

**Terminal 2 — Orquestrador:**
```bash
make orchestrator
# ou: go run ./cmd/orchestrator
```

**Terminal 3 — Drone #10:**
```bash
make drone
# ou: go run ./cmd/drone 10
```

**Terminal 4 — Drone #20:**
```bash
make drone2
# ou: go run ./cmd/drone 20
```

**Terminal 5 — Drone #30:**
```bash
make drone3
# ou: go run ./cmd/drone 30
```

### Monitorar o Redis em tempo real

```bash
make watch-redis
# ou: docker exec -it drone-redis redis-cli monitor
```

### Ver estado no Redis Commander

Acesse `http://localhost:8082` no navegador para visualizar:
- `drone:status:10`, `drone:status:20`, `drone:status:30` — estado dos drones
- `mutex:queue` — fila de exclusão mútua (Sorted Set)
- `mutex:owner` — detentor atual do mutex
- `election:leader` — ID do líder eleito
- `clock:lamport` — contador global de Lamport
- `drone:active` — set de drones ativos

---

## Conceitos de Sistemas Distribuídos — Como o Redis os Aborda

### Consistência
O Redis usa **consistência eventual** por padrão. Para este projeto, o TTL nos Hashes de drones garante que dados obsoletos expirem automaticamente, evitando leituras de estados inválidos.

### Replicação
O Redis suporta replicação primária/réplica nativa. Em produção, cada comando de escrita seria replicado assincronamente para réplicas, garantindo durabilidade.

### Tolerância a Falhas
- O TTL de 30s nos hashes de drones detecta falhas automaticamente (drone sem heartbeat expira).
- O mutex com TTL de 10s evita deadlock caso o drone detentor falhe sem liberar.
- O `appendonly yes` no Docker garante persistência em disco (AOF).

### Segurança
- Redis suporta autenticação via senha (`requirepass`).
- TLS disponível para criptografia em trânsito.
- Isolamento por DB (números 0-15).

---

## Arquitetura com Redis

```
┌──────────┐    TCP(8080)    ┌──────────────────┐
│ Drone #N │ ─────────────► │   Orquestrador   │
│          │                 │                  │
│          │  Redis Pub/Sub  │  ┌────────────┐  │
│          │ ──────────────► │  │   Redis    │  │
│          │  (heartbeat,    │  │  Middleware│  │
│          │   emergency,    │  │            │  │
│          │   election)     │  │ • Pub/Sub  │  │
└──────────┘                 │  │ • Hashes   │  │
                             │  │ • ZSet     │  │
┌──────────┐  TCP(9000)      │  │ • Strings  │  │
│NameServer│ ◄──────────────►│  └────────────┘  │
└──────────┘                 └──────────────────┘
```

---

## Parar o sistema

```bash
make down
# Para remover volumes também:
make clean
```
