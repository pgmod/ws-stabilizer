package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// readFromBackendNoWait читает сообщения из бэкенда без WaitGroup
func readFromBackendNoWait(backendConn *BackendConnection, clientConn *websocket.Conn, clientCtx context.Context, errCh chan<- error) {
	defer func() {
		if r := recover(); r != nil {
			safeSendError(errCh, fmt.Errorf("backend read panic: %v", r), backendConn.ctx, clientCtx)
		}
	}()

	for {
		if isContextDone(backendConn.ctx, clientCtx) {
			return
		}

		conn := backendConn.getConn()
		if conn == nil {
			return
		}

		// Защищаем чтение от паники
		mt, msg, err := readMessageSafe(conn)
		if err != nil {
			if isContextDone(backendConn.ctx, clientCtx) {
				return
			}
			if isTimeoutError(err) {
				continue
			}
			safeSendError(errCh, fmt.Errorf("backend read: %w", err), backendConn.ctx, clientCtx)
			return
		}

		if err := writeMessageSafe(clientConn, mt, msg); err != nil {
			if isContextDone(backendConn.ctx, clientCtx) || isCloseError(err) {
				return
			}
			safeSendError(errCh, fmt.Errorf("client write: %w", err), backendConn.ctx, clientCtx)
			return
		}
	}
}

// readFromClientNoWait читает сообщения от клиента без WaitGroup
// Возвращает true, если клиент отключился
func readFromClientNoWait(clientConn *websocket.Conn, backendConn *BackendConnection, clientCtx context.Context, errCh chan<- error) bool {
	defer func() {
		if r := recover(); r != nil {
			safeSendError(errCh, fmt.Errorf("client read panic: %v", r), backendConn.ctx, clientCtx)
		}
	}()

	for {
		if isContextDone(backendConn.ctx, clientCtx) {
			return false
		}

		// Защищаем чтение от паники
		mt, msg, err := readMessageSafe(clientConn)
		if err != nil {
			log.Printf("client read: %v, %v, %v", mt, msg, err)
			if isContextDone(backendConn.ctx, clientCtx) {
				return false
			}
			if isTimeoutError(err) {
				continue
			}
			if isCloseError(err) {
				return true // Клиент отключился
			}
			safeSendError(errCh, fmt.Errorf("client read: %w", err), backendConn.ctx, clientCtx)
			return false
		}

		conn := backendConn.getConn()
		if conn == nil {
			return false
		}

		if err := writeMessageSafe(conn, mt, msg); err != nil {
			if isContextDone(backendConn.ctx, clientCtx) {
				return false
			}
			safeSendError(errCh, fmt.Errorf("backend write: %w", err), backendConn.ctx, clientCtx)
			return false
		}
	}
}

// handleReconnection обрабатывает переподключение к бэкенду
func handleReconnection(
	backendConn *BackendConnection,
	clientConn *websocket.Conn,
	clientCtx context.Context,
	r *http.Request,
	errCh chan error,
) {
	defer func() {
		backendConn.close()
	}()

	for range errCh {
		// Проверяем, что клиент еще подключен
		if isContextDone(clientCtx) {
			return
		}

		sendEvent(clientConn, disconnectedEvent)

		// Пытаемся переподключиться
		deadline := time.Now().Add(dialTimeout)
		newConn, err := reconnectToBackend(r, deadline)
		if err != nil {
			log.Printf("reconnect failed: %v", err)
			return
		}

		// Проверяем контекст перед заменой соединения
		if isContextDone(clientCtx) {
			_ = newConn.Close()
			return
		}

		// Заменяем соединение (это закроет старое и отменит контекст)
		backendConn.replace(newConn)

		if isContextDone(clientCtx) {
			return
		}

		sendEvent(clientConn, connectedEvent)

		// Запускаем новые горутины чтения без WaitGroup
		// Они завершатся автоматически при отмене контекста или ошибке
		go readFromBackendNoWait(backendConn, clientConn, clientCtx, errCh)
		go readFromClientNoWait(clientConn, backendConn, clientCtx, errCh)
	}
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	// Проверка лимита соединений
	if maxConnections > 0 {
		current := atomic.LoadInt64(&activeConnections)
		if current >= int64(maxConnections) {
			log.Printf("max connections reached: %d/%d", current, maxConnections)
			http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
	}

	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade failed: %v", err)
		return
	}

	// Увеличиваем счетчик активных соединений
	atomic.AddInt64(&activeConnections, 1)
	defer atomic.AddInt64(&activeConnections, -1)

	// Логируем только при необходимости (можно убрать для production)
	log.Printf("active connections: %d", atomic.LoadInt64(&activeConnections))

	var closeOnce sync.Once
	defer closeOnce.Do(func() { _ = clientConn.Close() })

	// Подключаемся к бэкенду
	backendConn, err := dialBackend(backendURL, dialTimeout, r)
	if err != nil {
		sendEvent(clientConn, disconnectedEvent)
		return
	}

	backend := newBackendConnection(backendConn)

	clientCtx, clientCancel := context.WithCancel(context.Background())
	defer clientCancel()

	sendEvent(clientConn, connectedEvent)

	// Увеличенный буфер для канала ошибок (лучше для высокой нагрузки)
	errCh := make(chan error, 40)
	var readWg sync.WaitGroup
	var reconnectWg sync.WaitGroup
	clientDisconnected := make(chan bool, 1)

	// Запускаем горутины для чтения
	readWg.Add(2)
	go func() {
		defer readWg.Done()
		readFromBackendNoWait(backend, clientConn, clientCtx, errCh)
	}()
	go func() {
		defer readWg.Done()
		if readFromClientNoWait(clientConn, backend, clientCtx, errCh) {
			// Клиент отключился - сразу отменяем контекст и закрываем бэкенд
			// Это предотвратит ненужные переподключения в handleReconnection
			clientCancel()
			backend.closeWithCode(websocket.CloseGoingAway, "client disconnected")
			select {
			case clientDisconnected <- true:
			default:
			}
		}
	}()

	// Запускаем горутину для обработки переподключений
	reconnectWg.Add(1)
	go func() {
		defer reconnectWg.Done()
		handleReconnection(backend, clientConn, clientCtx, r, errCh)
	}()

	readWg.Wait()

	// Если контекст еще не отменен (клиент не отключился явно), отменяем его сейчас
	// и закрываем бэкенд обычным способом
	select {
	case <-clientDisconnected:
		// Контекст уже отменен и бэкенд закрыт с кодом GoingAway
	default:
		// Клиент не отключился явно - отменяем контекст и закрываем бэкенд
		clientCancel()
		backend.close()
	}

	close(errCh)
	reconnectWg.Wait()
}
