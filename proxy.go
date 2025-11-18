package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// readFromBackend читает сообщения из бэкенда и пересылает их клиенту
func readFromBackend(backendConn *BackendConnection, clientConn *websocket.Conn, clientCtx context.Context, errCh chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()

	for {
		if isContextDone(backendConn.ctx, clientCtx) {
			return
		}

		conn := backendConn.getConn()
		if conn == nil {
			return
		}

		if isContextDone(backendConn.ctx, clientCtx) {
			return
		}

		conn.SetReadDeadline(time.Now().Add(readDeadlineTimeout))
		mt, msg, err := conn.ReadMessage()
		if err != nil {
			if isContextDone(backendConn.ctx, clientCtx) {
				return
			}

			if isTimeoutError(err) {
				if isContextDone(backendConn.ctx, clientCtx) {
					return
				}
				continue
			}

			select {
			case errCh <- fmt.Errorf("backend read: %w", err):
			case <-backendConn.ctx.Done():
				return
			case <-clientCtx.Done():
				return
			default:
			}
			return
		}

		conn.SetReadDeadline(time.Time{})
		if err := clientConn.WriteMessage(mt, msg); err != nil {
			if isContextDone(backendConn.ctx, clientCtx) {
				return
			}
			if isCloseError(err) {
				return
			}
			select {
			case errCh <- fmt.Errorf("client write: %w", err):
			case <-backendConn.ctx.Done():
				return
			case <-clientCtx.Done():
				return
			default:
			}
			return
		}
	}
}

// readFromClient читает сообщения от клиента и пересылает их в бэкенд
func readFromClient(clientConn *websocket.Conn, backendConn *BackendConnection, clientCtx context.Context, errCh chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()

	for {
		if isContextDone(backendConn.ctx, clientCtx) {
			return
		}

		clientConn.SetReadDeadline(time.Now().Add(readDeadlineTimeout))
		mt, msg, err := clientConn.ReadMessage()
		if err != nil {
			if isContextDone(backendConn.ctx, clientCtx) {
				return
			}

			if isTimeoutError(err) {
				continue
			}

			if isCloseError(err) {
				return
			}

			select {
			case errCh <- fmt.Errorf("client read: %w", err):
			case <-backendConn.ctx.Done():
				return
			case <-clientCtx.Done():
				return
			default:
			}
			return
		}

		clientConn.SetReadDeadline(time.Time{})
		conn := backendConn.getConn()
		if conn == nil {
			return
		}

		if err := conn.WriteMessage(mt, msg); err != nil {
			if isContextDone(backendConn.ctx, clientCtx) {
				return
			}
			select {
			case errCh <- fmt.Errorf("backend write: %w", err):
			case <-backendConn.ctx.Done():
				return
			case <-clientCtx.Done():
				return
			default:
			}
			return
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
	wg *sync.WaitGroup,
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

		// Заменяем соединение (это закроет старое и отменит контекст)
		backendConn.replace(newConn)

		if isContextDone(clientCtx) {
			return
		}

		sendEvent(clientConn, connectedEvent)
		wg.Add(2)
		go readFromBackend(backendConn, clientConn, clientCtx, errCh, wg)
		go readFromClient(clientConn, backendConn, clientCtx, errCh, wg)
	}
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade failed: %v", err)
		return
	}
	log.Printf("client connected: %s", clientConn.RemoteAddr())

	var closeOnce sync.Once
	closeClient := func() {
		closeOnce.Do(func() {
			_ = clientConn.Close()
		})
	}
	defer closeClient()

	// Подключаемся к бэкенду
	backendConn, err := dialBackend(backendURL, dialTimeout, r)
	if err != nil {
		sendEvent(clientConn, disconnectedEvent)
		return
	}
	defer func() {
		if backendConn != nil {
			_ = backendConn.Close()
		}
	}()

	backend := newBackendConnection(backendConn)
	defer backend.close()

	clientCtx, clientCancel := context.WithCancel(context.Background())
	defer clientCancel()

	sendEvent(clientConn, connectedEvent)

	errCh := make(chan error, 2)
	var wg sync.WaitGroup

	// Запускаем горутины для чтения
	wg.Add(2)
	go readFromBackend(backend, clientConn, clientCtx, errCh, &wg)
	go readFromClient(clientConn, backend, clientCtx, errCh, &wg)

	// Запускаем горутину для обработки переподключений
	go handleReconnection(backend, clientConn, clientCtx, r, errCh, &wg)

	// Ждем завершения всех горутин
	wg.Wait()
}

