# AI Worker Public

Thin client worker that connects to the AI Gateway and processes prompts via local Ollama.

## Overview

This worker is a minimal "thin client" that:
1. Connects to the gateway via WebSocket
2. Listens for incoming raw prompts
3. Forwards them to a local Ollama instance (port 11434)
4. Returns results back to the gateway in real-time (streaming)

Pure Go implementation with **zero dependencies** except `gorilla/websocket`.

## Prerequisites

### Ollama
You need to have Ollama running locally:
```bash
# Install from https://ollama.ai
ollama run llama2  # or any other model
```

Check that Ollama is running on `http://localhost:11434`

## Building from Source

```bash
cd ai-worker-public
go mod download
go build -o worker main.go
```

## Cross-Compilation for Distribution

The power of Go: compile once on any machine, distribute to all platforms without dependencies.

### For Linux (64-bit)
```bash
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o worker-linux-amd64 main.go
```

### For macOS (Apple Silicon: M1, M2, M3, M4)
```bash
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o worker-mac-arm64 main.go
```

### For macOS (Intel processors)
```bash
GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o worker-mac-amd64 main.go
```

### For Windows
```bash
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o worker-windows-amd64.exe main.go
```

The `-ldflags="-s -w"` flag strips debug symbols, reducing binary size to ~5-6MB while maintaining full performance.

## Running the Worker

### Local Development
```bash
# Default: connects to localhost:8080, Ollama at localhost:11434
./worker

# Or with custom gateway
GATEWAY_URL=ws://192.168.1.100:8080/ws/worker ./worker

# Or with custom Ollama location
OLLAMA_URL=http://192.168.1.200:11434 ./worker
```

### Docker
```bash
docker build -t detoprotocol-worker .
docker run -e GATEWAY_URL=ws://gateway:8080/ws/worker \
           -e OLLAMA_URL=http://ollama:11434 \
           detoprotocol-worker
```

## Environment Variables

- `GATEWAY_URL`: WebSocket URL of the gateway (default: `ws://localhost:8080/ws/worker`)
- `OLLAMA_URL`: URL of local Ollama instance (default: `http://localhost:11434`)

## Architecture

```
┌──────────────────────┐
│   AI Gateway         │
│  (ws://...8080)      │
└──────────┬───────────┘
           │ WebSocket
           │
┌──────────▼───────────┐
│   Thin Worker        │
│   (this binary)      │
│  OllamaConnector     │
└──────────┬───────────┘
           │ HTTP
           │
┌──────────▼───────────┐
│  Local Ollama        │
│  (localhost:11434)   │
└──────────────────────┘
```

## Message Protocol

### Incoming request from gateway:
```json
{
  "event": "proxy_request",
  "task_id": "task-1234567890",
  "data": "{\"model\": \"llama2\", \"prompt\": \"What is AI?\", \"stream\": true}"
}
```

### Outgoing response chunks:
```json
{
  "event": "proxy_chunk",
  "task_id": "task-1234567890",
  "data": "{\"response\": \"AI is...\", \"done\": false}"
}
```

## Key Features

- **Thin Client Philosophy**: Worker has zero logic about models, prompts, or business rules
- **Streaming**: Real-time token streaming from Ollama to gateway to clients
- **Blind Proxy**: Worker doesn't parse the payload—it just pipes bytes
- **Memory Safe**: No buffer accumulation, works with massive texts
- **Auto-Reconnect**: Automatically reconnects if gateway goes down
- **Cross-Platform**: Single codebase, compiled for all OSes

## Troubleshooting

### Connection refused
- Make sure gateway is running: `curl http://localhost:8080/health`
- Check `GATEWAY_URL` environment variable

### Ollama not responding
- Make sure Ollama is running: `curl http://localhost:11434/api/tags`
- Check that a model is installed: `ollama list`
- Verify `OLLAMA_URL` environment variable

### Logs show "LLM not found"
- Ensure local Ollama instance is available at the configured URL
- Worker will start anyway and retry connections

## Performance Notes

- Workers are stateless and independent
- Each worker processes one request at a time
- Ollama response times depend on your hardware and model size
- Gateway handles load balancing across workers
- Network overhead is minimal (just binary pipes)

## License

MIT
