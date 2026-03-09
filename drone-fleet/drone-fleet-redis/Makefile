# Makefile — drone-fleet (Etapa 5 / Redis)

.PHONY: redis up down deps build nameserver orchestrator drone drone2 drone3 clean redis-cli

# Variáveis para rodar LOCAL (fora do Docker)
LOCAL_ENV = REDIS_ADDR=localhost:6379 ORCHESTRATOR_HOST=localhost NAMESERVER_HOST=localhost

# ── Infraestrutura ────────────────────────────────────────────────────────
redis:
	docker-compose up -d redis redis-commander
	@echo "Redis disponível em localhost:6379"
	@echo "Redis Commander UI em http://localhost:8082"

up:
	docker-compose up -d

down:
	docker-compose down

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

# ── Execução LOCAL ────────────────────────────────────────────────────────
nameserver:
	$(LOCAL_ENV) go run ./cmd/nameserver

orchestrator:
	$(LOCAL_ENV) go run ./cmd/orchestrator

drone:
	$(LOCAL_ENV) go run ./cmd/drone 10

drone2:
	$(LOCAL_ENV) go run ./cmd/drone 20

drone3:
	$(LOCAL_ENV) go run ./cmd/drone 30

# ── Utilitários Redis ────────────────────────────────────────────────────
redis-cli:
	docker exec -it drone-redis redis-cli

watch-redis:
	docker exec -it drone-redis redis-cli monitor

clean:
	if exist bin\ (rmdir /s /q bin\) else (rm -rf bin/ 2>/dev/null || true)
	docker-compose down -v