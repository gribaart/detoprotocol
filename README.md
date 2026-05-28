# DetoProtocol

Distributed AI Gateway with WebSocket-based Thin Worker Architecture (Pure Go)

## Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│                    AI Agents                             │
│              (Send HTTP POST /prompt)                    │
└────────────────────┬────────────────────────────────────┘
                     │ HTTP
                     ▼
┌─────────────────────────────────────────────────────────┐
│         ai-gateway-private (Go Server)                   │
│  - Receives HTTP requests with prompts                  │
│  - Manages pool of thin worker clients                  │
│  - Load balances via channel-based queue                │
│  - Forwards raw JSON payloads to workers via WebSocket  │
│  - Streams responses back via SSE (Server-Sent Events)  │
└────────────────────┬────────────────────────────────────┘
                     │ WebSocket
        ┌────────────┴────────────┐
        ▼                         ▼
┌──────────────────┐      ┌──────────────────┐
│  ai-worker-public│      │  ai-worker-public│
│   (Thin Go)      │      │   (Thin Go)      │
│  OllamaConnector │      │  OllamaConnector │
└────────┬─────────┘      └────────┬─────────┘
         │                         │
         └────────────┬────────────┘
                      │ HTTP
                      ▼
            ┌──────────────────┐
            │  Local Ollama    │
            │  (Port 11434)    │
            │  (per laptop)    │
            └──────────────────┘
```

## Project Structure

```
detoprotocol/
├── ai-gateway-private/     # Приватный сервер (Go)
│   ├── main.go             # Оркестратор потоков + SSE стриминг
│   ├── go.mod              # Зависимости
│   └── Dockerfile          # Контейнеризация
│
├── ai-worker-public/       # Публичный воркер (Pure Go)
│   ├── main.go             # Тонкий клиент с OllamaConnector
│   ├── go.mod              # Только gorilla/websocket
│   └── Dockerfile          # Минималистичный контейнер
│
├── docker-compose.yml      # Оркестрация: Gateway + Worker + Ollama
├── .env.example            # Переменные окружения
├── .gitignore              # Git-исключения
└── README.md               # Этот файл
```

## Components

### ai-gateway-private (Приватный сервер на Go)

**HTTP API для ИИ-агентов:**
```bash
POST /prompt
Content-Type: application/json

{
  "prompt": "What is artificial intelligence?",
  "model": "qwen2.5:1.5b"
}
```

**Ответ:**
```
HTTP/1.1 200 OK
Content-Type: text/event-stream

data: AI is
data:  a branch
data:  of computer
...
```

**Особенности:**
- Управляет пулом воркеров (до 128 одновременно свободных)
- Формирует сырой JSON запрос для Ollama (гейтвей сам решает о модели и стриминге)
- Streaming ответов через Server-Sent Events (SSE) 
- Health check: `GET /health`

### ai-worker-public (Публичный воркер на Go)

**Архитектура "тонкого клиента":**
- Подключается к gateway через WebSocket
- Слушает события `proxy_request` с сырым JSON-пакетом
- Пробрасывает байты в локальную Ollama (localhost:11434)
- Читает стрим ответов **построчно** (забота об экономии памяти)
- Отправляет каждую строку как `proxy_chunk` обратно в gateway

**Ключевое преимущество:**
- Worker **не знает** ни про модели, ни про структуру промптов
- Работает как "слепой прокси" — просто трубит байты
- Если вы завтра поменяете модель на llama3 в запросе — воркер не пересобирается
- Заменяемость: Вместо Ollama можно подключить llama.cpp, vLLM или любой другой движок на том же REST API

## Getting Started

### Using Docker Compose (Recommended for Testing)

```bash
# Clone repository
git clone https://github.com/gribaart/detoprotocol.git
cd detoprotocol

# Spin up the entire stack (gateway + worker + ollama)
docker-compose up -d

# Check logs
docker-compose logs -f worker
docker-compose logs -f gateway

# Send test prompt
curl -X POST http://localhost:8080/prompt \
  -H "Content-Type: application/json" \
  -d '{"prompt":"Hello, what is 2+2?","model":"qwen2.5:1.5b"}' \
  -N

# Stop everything
docker-compose down
```

### Local Development (No Docker)

**Prerequisites:**
- Go 1.21+
- Ollama running locally (`ollama serve`)

**Gateway:**
```bash
cd ai-gateway-private
go mod download
go run main.go
# Listens on http://localhost:8080
```

**Worker:**
```bash
cd ai-worker-public
go mod download
go run main.go
# Connects to ws://localhost:8080/ws/worker
# Forwards requests to http://localhost:11434
```

**Test:**
```bash
# In a new terminal
curl -X POST http://localhost:8080/prompt \
  -H "Content-Type: application/json" \
  -d '{"prompt":"Tell me a joke","model":"llama2"}' \
  -N
```

## Distribution: Cross-Compilation

The **magic** of Go: compile once, distribute everywhere without installing Go on client machines.

```bash
cd ai-worker-public

# Linux (servers, cloud, WSL)
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o worker-linux-amd64 main.go

# macOS (Apple Silicon: M1, M2, M3, M4)
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o worker-mac-arm64 main.go

# macOS (Intel)
GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o worker-mac-amd64 main.go

# Windows
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o worker-windows-amd64.exe main.go
```

**Binary sizes:** ~5-6 MB each (stripped of debug symbols)

### User Experience (End-User Perspective)

1. User downloads `worker-mac-arm64` from your Releases page
2. User runs in terminal:
   ```bash
   chmod +x worker-mac-arm64
   GATEWAY_URL="ws://api.yourproject.com/ws/worker" ./worker-mac-arm64
   ```
3. **Instant connection**, zero installation, zero dependencies

Perfect for distributed mining/computing networks.

## Configuration

### Environment Variables

**Gateway:**
- `GATEWAY_PORT`: HTTP port (default: 8080)

**Worker:**
- `GATEWAY_URL`: WebSocket address of gateway (default: ws://localhost:8080/ws/worker)
- `OLLAMA_URL`: HTTP address of local Ollama (default: http://localhost:11434)

### Example .env File

```bash
# Gateway
GATEWAY_PORT=8080

# Worker
GATEWAY_URL=ws://localhost:8080/ws/worker
OLLAMA_URL=http://localhost:11434
```

## Performance & Scalability

- **Workers:** Stateless, independent, can run hundreds on one machine
- **Gateway:** Channel-based load balancing, O(1) task dispatch
- **Memory:** No buffer accumulation (streaming approach), safe for GB-sized outputs
- **Latency:** Worker starts streaming to client within milliseconds

## Testing

```bash
# Check gateway health
curl http://localhost:8080/health

# Verify Ollama availability
curl http://localhost:11434/api/tags

# Test complete flow
curl -X POST http://localhost:8080/prompt \
  -H "Content-Type: application/json" \
  -d '{"prompt":"1+1=","model":"neural-chat"}' \
  -N
```

## Troubleshooting

| Problem | Solution |
|---------|----------|
| Worker can't connect to gateway | Check `GATEWAY_URL` env var, ensure gateway is running (`http://localhost:8080/health`) |
| Ollama not responding | Ensure Ollama is running (`ollama serve`), check `OLLAMA_URL` env var |
| Binary won't execute (Mac/Linux) | Run `chmod +x worker-mac-arm64` |
| Docker fails to build | Ensure you're in the correct directory, check `docker --version` |

## Advanced Topics

### Custom LLM Backends

The worker uses Ollama's `/api/generate` endpoint. To use a different backend (llama.cpp, vLLM, etc.):

1. Ensure your backend listens on localhost:port
2. Set `OLLAMA_URL=http://localhost:YOUR_PORT`
3. Worker automatically proxies the request

The payload format is flexible—gateway sends whatever you want, worker just pipes it.

### Monitoring & Observability

Gateway logs:
- Worker connections/disconnections
- Task distribution
- Response streaming status

Worker logs:
- Gateway connection status
- Ollama request/response status
- Errors with task IDs for debugging

### Scaling

**Vertical:** Run multiple workers on same machine (each connects independently to gateway)

**Horizontal:** Deploy workers across multiple laptops/servers, all connecting to single gateway via internet

## License

MIT

## Contributing

PRs welcome! The architecture is minimal and well-defined, making it easy to extend.

---

**Built with:** Pure Go, WebSockets, Server-Sent Events, Ollama API

**Philosophy:** Thin clients, reverse proxies, zero assumptions about payload structure
