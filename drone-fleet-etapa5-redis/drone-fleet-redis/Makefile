# Makefile — drone-fleet (Etapa 5 / Redis)

.PHONY: redis up down deps build nameserver orchestrator drone drone2 drone3 clean redis-cli

# ── Infraestrutura ────────────────────────────────────────────────────────
redis:
	docker-compose -f docker/docker-compose.yml up -d redis
	@echo "Redis disponível em localhost:6379"
	@echo "Redis Commander UI em http://localhost:8082"

up:
	docker-compose -f docker/docker-compose.yml up -d

down:
	docker-compose -f docker/docker-compose.yml down

# ── Dependências Go ──────────────────────────────────────────────────────
deps:
	go mod tidy
	go mod download

# ── Build ────────────────────────────────────────────────────────────────
build:
	go build -o bin/nameserver    ./cmd/nameserver
	go build -o bin/orchestrator  ./cmd/orchestrator
	go build -o bin/drone         ./cmd/drone
	@echo "Binários gerados em ./bin/"

# ── Execução (abra cada um em um terminal separado) ──────────────────────
nameserver:
	go run ./cmd/nameserver

orchestrator:
	go run ./cmd/orchestrator

drone:
	go run ./cmd/drone 10

drone2:
	go run ./cmd/drone 20

drone3:
	go run ./cmd/drone 30

# ── Utilitários Redis ────────────────────────────────────────────────────
redis-cli:
	docker exec -it drone-redis redis-cli

watch-redis:
	docker exec -it drone-redis redis-cli monitor

clean:
	rm -rf bin/
	docker-compose -f docker/docker-compose.yml down -v
