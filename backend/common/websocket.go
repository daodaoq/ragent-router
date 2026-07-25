package common

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ────────────────────────────────────────────────────────────
// WebSocket 实时推送
//
// 面试考点：
//  1. WebSocket vs SSE vs 长轮询？（全双工 vs 单向 vs 模拟双向）
//  2. WebSocket 握手过程？（HTTP Upgrade: websocket）
//  3. 心跳机制？（Ping/Pong 保活，检测死连接）
//  4. 断线重连？（客户端指数退避重连）
//  5. 广播 vs 点对点？（Hub 模式管理所有连接）
// ────────────────────────────────────────────────────────────

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // 开发环境允许所有来源
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// WSMessage WebSocket 消息。
type WSMessage struct {
	Type string      `json:"type"` // 消息类型
	Data interface{} `json:"data"` // 消息数据
	Time int64       `json:"time"` // 时间戳
}

// WSClient WebSocket 客户端连接。
type WSClient struct {
	conn   *websocket.Conn
	send   chan []byte
	hub    *WSHub
	id     string
	closed bool
	mu     sync.Mutex
}

// WSHub WebSocket 连接管理中心。
//
// 功能：
//   - 管理所有活跃连接
//   - 广播消息到所有客户端
//   - 点对点消息发送
//   - 心跳检测死连接
type WSHub struct {
	clients    map[*WSClient]bool
	broadcast  chan []byte
	register   chan *WSClient
	unregister chan *WSClient
	mu         sync.RWMutex
}

// NewWSHub 创建 WebSocket Hub。
func NewWSHub() *WSHub {
	return &WSHub{
		clients:    make(map[*WSClient]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *WSClient),
		unregister: make(chan *WSClient),
	}
}

// 全局 WebSocket Hub
var WSGlobal *WSHub

// InitWebSocket 初始化 WebSocket。
func InitWebSocket() {
	WSGlobal = NewWSHub()
	go WSGlobal.Run()
	log.Println("[WebSocket] 实时推送已启用")
}

// Run 启动 Hub 事件循环。
func (h *WSHub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			log.Printf("[WebSocket] 客户端连接: %s (总数: %d)", client.id, len(h.clients))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
			log.Printf("[WebSocket] 客户端断开: %s (总数: %d)", client.id, len(h.clients))

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					// 发送缓冲区满，关闭连接
					go func(c *WSClient) {
						h.unregister <- c
					}(client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Broadcast 广播消息到所有客户端。
func (h *WSHub) Broadcast(msgType string, data interface{}) {
	msg := WSMessage{
		Type: msgType,
		Data: data,
		Time: time.Now().UnixMilli(),
	}
	jsonData, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.broadcast <- jsonData
}

// ClientCount 返回当前连接数。
func (h *WSHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// HandleWebSocket WebSocket HTTP 处理器。
func (h *WSHub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WebSocket] 升级失败: %v", err)
		return
	}

	client := &WSClient{
		conn: conn,
		send: make(chan []byte, 256),
		hub:  h,
		id:   r.RemoteAddr,
	}

	h.register <- client

	// 启动读写 goroutine
	go client.writePump()
	go client.readPump()
}

// readPump 读取客户端消息（主要处理 Pong 帧）。
func (c *WSClient) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

// writePump 发送消息到客户端（包含心跳 Ping）。
func (c *WSClient) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			// 发送 Ping 心跳
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ────────────────────────────────────────────────────────────
// 预定义推送事件
// ────────────────────────────────────────────────────────────

const (
	WSEventMetrics      = "metrics"       // 指标更新
	WSEventRequest      = "request"       // 新请求
	WSEventCircuitState = "circuit_state" // 熔断器状态变更
	WSEventAlert        = "alert"         // 告警
)

// BroadcastMetrics 推送指标更新。
func BroadcastMetrics(data interface{}) {
	if WSGlobal != nil {
		WSGlobal.Broadcast(WSEventMetrics, data)
	}
}

// BroadcastRequest 推送新请求。
func BroadcastRequest(data interface{}) {
	if WSGlobal != nil {
		WSGlobal.Broadcast(WSEventRequest, data)
	}
}
