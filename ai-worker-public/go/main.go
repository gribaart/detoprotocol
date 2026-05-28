package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

type WSMessage struct {
	Event  string `json:"event"`   // "proxy_request" или "proxy_chunk"
	TaskID string `json:"task_id"` // Идентификатор задачи
	Data   string `json:"data"`    // Сырые байты/строки (промпт или токен от Олламы)
}

func main() {
	gatewayURL := os.Getenv("GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = "ws://localhost:8080/ws/worker"
	}
	ollamaURL := os.Getenv("OLLAMA_URL")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}

	log.Printf("🚀 Запуск Тонкого Воркера. Подключение к Гейтвею: %s", gatewayURL)
	log.Printf("📡 Ollama: %s", ollamaURL)

	for {
		conn, _, err := websocket.DefaultDialer.Dial(gatewayURL, nil)
		if err != nil {
			log.Println("❌ Гейтвей недоступен. Повтор через 5 секунд...")
			time.Sleep(5 * time.Second)
			continue
		}

		log.Println("✓ Успешно подключено к Гейтвею. Ожидание сырых потоков...")

		for {
			_, msgBytes, err := conn.ReadMessage()
			if err != nil {
				log.Println("⚠️ Связь с сервером потеряна.")
				break
			}

			var msg WSMessage
			json.Unmarshal(msgBytes, &msg)

			if msg.Event == "proxy_request" {
				// Запускаем слепую пересылку в Олламу в отдельном потоке (горутине)
				go func(taskID, rawBody, ollamaURL string, conn *websocket.Conn) {
					resp, err := http.Post(ollamaURL+"/api/generate", "application/json", bytes.NewBufferString(rawBody))
					if err != nil {
						log.Printf("❌ Локальная Ollama по адресу %s не отвечает! Error: %v", ollamaURL, err)
						return
					}
					defer resp.Body.Close()

					// Построчно читаем стрим от Олламы и вслепую выплевываем обратно в Гейтвей
					scanner := bufio.NewScanner(resp.Body)
					for scanner.Scan() {
						chunk, _ := json.Marshal(WSMessage{
							Event:  "proxy_chunk",
							TaskID: taskID,
							Data:   scanner.Text(),
						})
						if err := conn.WriteMessage(websocket.TextMessage, chunk); err != nil {
							log.Printf("⚠️ Ошибка отправки chunk для task %s: %v", taskID, err)
							return
						}
					}

					if err := scanner.Err(); err != nil {
						log.Printf("⚠️ Ошибка чтения стрима Ollama: %v", err)
					}
				}(msg.TaskID, msg.Data, ollamaURL, conn)
			}
		}
		conn.Close()
		log.Println("⏳ Переподключение через 5 секунд...")
		time.Sleep(5 * time.Second)
	}
}
