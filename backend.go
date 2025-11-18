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

func (bc *BackendConnection) setConn(conn *websocket.Conn) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.conn = conn
}

func (bc *BackendConnection) close() {
	bc.cancel()
	bc.mu.Lock()
	if bc.conn != nil {
		_ = bc.conn.Close()
		bc.conn = nil
	}
	bc.mu.Unlock()
}

func (bc *BackendConnection) replace(conn *websocket.Conn) {
	bc.mu.Lock()
	oldConn := bc.conn
	// Отменяем старый контекст, чтобы сигнализировать горутинам о необходимости остановиться
	bc.cancel()
	bc.mu.Unlock()
	
	// Даем время горутинам для остановки перед закрытием соединения
	time.Sleep(goroutineShutdownDelay)
	
	bc.mu.Lock()
	// Создаем новый контекст для нового соединения
	bc.ctx, bc.cancel = context.WithCancel(context.Background())
	bc.conn = conn
	bc.mu.Unlock()
	
	// Закрываем старое соединение после того, как горутины должны были остановиться
	if oldConn != nil {
		_ = oldConn.Close()
	}
}

// dialBackend устанавливает соединение с бэкендом
func dialBackend(backendURL string, timeout time.Duration, r *http.Request) (*websocket.Conn, error) {
	header := copyHeaders(r)
	dialer := websocket.Dialer{
		HandshakeTimeout: timeout,
		NetDial: func(network, addr string) (net.Conn, error) {
			return net.DialTimeout(network, addr, timeout)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, resp, err := dialer.DialContext(ctx, backendURL, header)
	if err != nil {
		if resp != nil {
			log.Printf("backend dial failed: %s %v", resp.Status, err)
		} else {
			log.Printf("backend dial failed: %v", err)
		}
		return nil, err
	}
	return conn, nil
}

// reconnectToBackend пытается переподключиться к бэкенду
func reconnectToBackend(r *http.Request, deadline time.Time) (*websocket.Conn, error) {
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		conn, err := dialBackend(backendURL, remaining, r)
		if err == nil {
			return conn, nil
		}
		time.Sleep(retryBackoff)
	}
	return nil, fmt.Errorf("reconnection timeout")
}

