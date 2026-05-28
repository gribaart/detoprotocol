package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

// --- ПРОТОКОЛ СВЯЗИ ---

type WSMessage struct {
	Event  string `json:"event"`   // "proxy_request" или "proxy_chunk"
	TaskID string `json:"task_id"` // ID задачи
	Data   string `json:"data"`    // Сырой JSON-пакет для Ollama или строка ответа
}

// --- МОДУЛЬ: КОННЕКТОР К ЛОКАЛЬНОЙ ЛЛМ ---

type OllamaConnector struct {
	BaseURL    string       // Например, "http://localhost:11434"
	HTTPClient *http.Client // Переиспользуемый пул HTTP-соединений
}

func NewOllamaConnector(url string) *OllamaConnector {
	return &OllamaConnector{
		BaseURL: url,
		HTTPClient: &http.Client{
			Timeout: 0, // Отключаем таймаут, так как ИИ-генерация может идти долго
		},
	}
}

// StreamProxy принимает сырые байты запроса от Гейтвея, передает их локальной LLM
// и построчно отправляет ответы в предоставленный callback-канал
func (oc *OllamaConnector) StreamProxy(ctx context.Context, rawPayload string, onToken func(string)) error {
	url := fmt.Sprintf("%s/api/generate", oc.BaseURL)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBufferString(rawPayload))
	if err != nil {
		return fmt.Errorf("ошибка создания запроса к LLM: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := oc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("локальная LLM не отвечает: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("LLM вернула статус %d: %s", resp.StatusCode, string(body))
	}

	// Построчно читаем сырой стрим от Олламы
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Передаем сырую JSON-строку от Олламы наверх
			onToken(scanner.Text())
		}
	}

	return scanner.Err()
}

// --- ОСНОВНОЙ ЦИКЛ ВОРКЕРА ---

func main() {
	gatewayURL := os.Getenv("GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = "ws://localhost:8080/ws/worker"
	}
	ollamaURL := os.Getenv("OLLAMA_URL")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}

	log.Printf("🚀 Запуск Тонкого Воркера на Go")
	log.Printf("📡 Подключение к Гейтвею: %s", gatewayURL)

	// Инициализируем наш коннектор к локальной LLM
	llm := NewOllamaConnector(ollamaURL)

	// Проверяем доступность локальной Олламы при старте
	log.Println("🔍 Проверка коннектора локальной LLM...")
	if resp, err := http.Get(ollamaURL + "/api/tags"); err != nil {
		log.Printf("⚠️ Внимание: Локальная Ollama не обнаружена по адресу %s. Убедитесь, что она запущена.", ollamaURL)
	} else {
		resp.Body.Close()
		log.Println("✓ Коннектор LLM успешно инициализирован")
	}

	// Цикл бесконечного переподключения к Гейтвею
	for {
		conn, _, err := websocket.DefaultDialer.Dial(gatewayURL, nil)
		if err != nil {
			log.Println("❌ Гейтвей недоступен. Повтор через 5 секунд...")
			time.Sleep(5 * time.Second)
			continue
		}

		log.Println("✓ Успешно подключено к Гейтвею. Ожидаем задачи...")

		for {
			_, msgBytes, err := conn.ReadMessage()
			if err != nil {
				log.Println("⚠️ Связь с Гейтвеем потеряна.")
				break
			}

			var msg WSMessage
			if err := json.Unmarshal(msgBytes, &msg); err != nil {
				continue
			}

			if msg.Event == "proxy_request" {
				// Запускаем асинхронный стрим через коннектор, чтобы не блокировать сокет
				go func(taskID, payload string, ws *websocket.Conn) {
					err := llm.StreamProxy(context.Background(), payload, func(rawLine string) {
						// Callback: прилетела строчка от LLM -> упаковываем и мгновенно пушим в сокет сервера
						chunk, _ := json.Marshal(WSMessage{
							Event:  "proxy_chunk",
							TaskID: taskID,
							Data:   rawLine,
						})
						_ = ws.WriteMessage(websocket.TextMessage, chunk)
					})

					if err != nil {
						log.Printf("❌ Ошибка инференса для задачи %s: %v", taskID, err)
					}
				}(msg.TaskID, msg.Data, conn)
			}
		}

		conn.Close()
		time.Sleep(5 * time.Second)
	}
}
