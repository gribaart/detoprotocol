package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	// Пул свободных воркеров (ноутбуков)
	workerPool   = make(chan *websocket.Conn, 128)
	workersMutex sync.Mutex

	// Каналы для стриминга ответов: TaskID -> канал с токенами
	activeStreams = make(map[string]chan string)
	streamsMutex  sync.RWMutex
)

// Универсальный конверт для веб-сокета "Тонкого клиента"
type WSMessage struct {
	Event  string `json:"event"`   // "proxy_request" или "proxy_chunk"
	TaskID string `json:"task_id"` // Идентификатор задачи
	Data   string `json:"data"`    // Сырые байты/строки (промпт или токен от Олламы)
}

func main() {
	port := os.Getenv("GATEWAY_PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/ws/worker", HandleWorkerConnect)
	http.HandleFunc("/prompt", HandlePrompt) // Эндпоинт для ИИ-агентов
	http.HandleFunc("/health", HandleHealth)

	log.Printf("🔒 Тонкий Гейтвей запущен на порту :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Ошибка сервера: %v", err)
	}
}

func HandleWorkerConnect(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Ошибка WS-апгрейда: %v", err)
		return
	}

	// Помещаем воркера в пул доступных
	workerPool <- conn
	log.Println("💻 Новый тонкий воркер подключился к пулу")

	// Бесконечно слушаем сокет воркера и маршрутизируем сырые токены клиентам
	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			log.Println("💻 Воркер отключился от сети")
			break
		}

		var msg WSMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			continue
		}

		if msg.Event == "proxy_chunk" {
			streamsMutex.RLock()
			ch, exists := activeStreams[msg.TaskID]
			streamsMutex.RUnlock()

			if exists {
				select {
				case ch <- msg.Data: // Передаем токен в HTTP-поток конкретного клиента
				default:
					log.Printf("⚠️ Канал переполнен для task %s", msg.TaskID)
				}
			}
		}
	}
	conn.Close()
}

func HandlePrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Парсим входящий промпт от ИИ-агента
	var req struct {
		Prompt string `json:"prompt"`
		Model  string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request JSON", http.StatusBadRequest)
		return
	}
	if req.Model == "" {
		req.Model = "qwen2.5:1.5b" // Дефолтная легкая модель для MVP
	}

	// Забираем свободный ноутбук из пула
	var worker *websocket.Conn
	select {
	case worker = <-workerPool:
		// Воркер успешно выделен, после задачи вернем его обратно в пул
		defer func() { workerPool <- worker }()
	case <-time.After(10 * time.Second):
		http.Error(w, "🤖 Все ИИ-ноды сейчас заняты, очередь переполнена", http.StatusServiceUnavailable)
		return
	}

	taskID := fmt.Sprintf("task-%d", time.Now().UnixNano())
	tokenChan := make(chan string, 100)

	// Регистрируем наш стрим в глобальной карте
	streamsMutex.Lock()
	activeStreams[taskID] = tokenChan
	streamsMutex.Unlock()

	defer func() {
		streamsMutex.Lock()
		delete(activeStreams, taskID)
		streamsMutex.Unlock()
		close(tokenChan)
	}()

	// Гейтвей САМ формирует сырой JSON-запрос для локальной Олламы воркера (Стриминг ВКЛЮЧЕН)
	ollamaPayload, _ := json.Marshal(map[string]interface{}{
		"model":  req.Model,
		"prompt": req.Prompt,
		"stream": true,
	})

	// Отправляем приказ воркеру
	cmd, _ := json.Marshal(WSMessage{
		Event:  "proxy_request",
		TaskID: taskID,
		Data:   string(ollamaPayload),
	})

	if err := worker.WriteMessage(websocket.TextMessage, cmd); err != nil {
		http.Error(w, "Воркер оборвал связь при старте", http.StatusInternalServerError)
		return
	}

	// Включаем режим Server-Sent Events (SSE) стриминга для ИИ-агента
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	// Читаем токены из канала и выталкиваем их в HTTP-ответ в реальном времени
	for tokenBytes := range tokenChan {
		// Парсим минимальный кусок ответа Олламы, чтобы извлечь чистый текст
		var chunk struct {
			Response string `json:"response"`
			Done     bool   `json:"done"`
		}
		_ = json.Unmarshal([]byte(tokenBytes), &chunk)

		// Форматируем под стандарт SSE (data: текст)
		fmt.Fprintf(w, "data: %s\n\n", chunk.Response)
		flusher.Flush()

		if chunk.Done {
			break
		}
	}
}

func HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"healthy","workers_available":%d}`, len(workerPool))
}
