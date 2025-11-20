package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// BackendConnection управляет соединением с бэкендом и связанными горутинами
type BackendConnection struct {
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
}

func newBackendConnection(conn *websocket.Conn) *BackendConnection {
	ctx, cancel := context.WithCancel(context.Background())
	return &BackendConnection{
		conn:   conn,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (bc *BackendConnection) getConn() *websocket.Conn {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.conn
}

func (bc *BackendConnection) close() {
	bc.closeWithCode(websocket.CloseNormalClosure, "")
}

func (bc *BackendConnection) closeWithCode(code int, text string) {
	bc.cancel()
	bc.mu.Lock()
	defer bc.mu.Unlock()
	if bc.conn != nil {
		// Отправляем Close frame с минимальным таймаутом для быстрого закрытия
		msg := websocket.FormatCloseMessage(code, text)
		deadline := time.Now().Add(100 * time.Millisecond)
		_ = bc.conn.WriteControl(websocket.CloseMessage, msg, deadline)
		_ = bc.conn.Close()
		bc.conn = nil
	}
	// Убрано избыточное логирование закрытий для снижения нагрузки
}

func (bc *BackendConnection) replace(conn *websocket.Conn) {
	bc.mu.Lock()
	oldConn := bc.conn
	bc.conn = conn
	bc.mu.Unlock()

	bc.cancel()
	bc.ctx, bc.cancel = context.WithCancel(context.Background())

	if oldConn != nil {
		_ = oldConn.Close()
	}
}

// dialBackend устанавливает соединение с бэкендом
func dialBackend(backendURL string, timeout time.Duration, r *http.Request) (*websocket.Conn, error) {
	dialer := websocket.Dialer{
		HandshakeTimeout: timeout,
		NetDial: func(network, addr string) (net.Conn, error) {
			return net.DialTimeout(network, addr, timeout)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, resp, err := dialer.DialContext(ctx, backendURL, copyHeaders(r))
	if err != nil {
		// Логируем только ошибки, успешные подключения не логируем для снижения нагрузки
		if resp != nil {
			log.Printf("backend dial failed: %s %v", resp.Status, err)
		} else {
			log.Printf("backend dial failed: %v", err)
		}
		return nil, err
	}
	// Убрано избыточное логирование успешных подключений
	return conn, nil
}

// reconnectToBackend пытается переподключиться к бэкенду
func reconnectToBackend(r *http.Request, deadline time.Time) (*websocket.Conn, error) {
	// Логируем только первую попытку переподключения
	reconnectLogged := false
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		conn, err := dialBackend(backendURL, remaining, r)
		if err == nil {
			if !reconnectLogged {
				log.Printf("reconnected to backend")
			}
			return conn, nil
		}
		if !reconnectLogged {
			log.Printf("reconnecting to backend...")
			reconnectLogged = true
		}
		time.Sleep(retryBackoff)
	}
	return nil, fmt.Errorf("reconnection timeout")
}
