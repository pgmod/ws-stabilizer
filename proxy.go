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

// readFromBackendNoWait читает сообщения из бэкенда без WaitGroup
func readFromBackendNoWait(backendConn *BackendConnection, clientConn *websocket.Conn, clientCtx context.Context, errCh chan<- error) {
	defer func() {
		if r := recover(); r != nil {
			// Защита от паники при чтении из закрытого соединения
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

		if isContextDone(backendConn.ctx, clientCtx) {
			return
		}

		// Защищаем чтение от паники
		var mt int
		var msg []byte
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("read panic: %v", r)
				}
			}()
			conn.SetReadDeadline(time.Now().Add(readDeadlineTimeout))
			mt, msg, err = conn.ReadMessage()
		}()

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

			safeSendError(errCh, fmt.Errorf("backend read: %w", err), backendConn.ctx, clientCtx)
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
			safeSendError(errCh, fmt.Errorf("client write: %w", err), backendConn.ctx, clientCtx)
			return
		}
	}
}

// readFromClientNoWait читает сообщения от клиента без WaitGroup
func readFromClientNoWait(clientConn *websocket.Conn, backendConn *BackendConnection, clientCtx context.Context, errCh chan<- error) {
	defer func() {
		if r := recover(); r != nil {
			// Защита от паники при чтении из закрытого соединения
			safeSendError(errCh, fmt.Errorf("client read panic: %v", r), backendConn.ctx, clientCtx)
		}
	}()

	for {
		if isContextDone(backendConn.ctx, clientCtx) {
			return
		}

		// Защищаем чтение от паники
		var mt int
		var msg []byte
		var err error
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("read panic: %v", r)
			}
			func() {
				clientConn.SetReadDeadline(time.Now().Add(readDeadlineTimeout))
				mt, msg, err = clientConn.ReadMessage()
			}()
		}()

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

			safeSendError(errCh, fmt.Errorf("client read: %w", err), backendConn.ctx, clientCtx)
			return
		}

		clientConn.SetReadDeadline(time.Time{})
		conn := backendConn.getConn()
		if conn == nil {
			return
		}

		// Защищаем запись от паники
		writeErr := func() error {
			defer func() {
				if r := recover(); r != nil {
					// Игнорируем панику при записи в закрытое соединение
				}
			}()
			return conn.WriteMessage(mt, msg)
		}()

		if writeErr != nil {
			if isContextDone(backendConn.ctx, clientCtx) {
				return
			}
			safeSendError(errCh, fmt.Errorf("backend write: %w", writeErr), backendConn.ctx, clientCtx)
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

		// Запускаем новые горутины чтения без WaitGroup
		// Они завершатся автоматически при отмене контекста или ошибке
		go readFromBackendNoWait(backendConn, clientConn, clientCtx, errCh)
		go readFromClientNoWait(clientConn, backendConn, clientCtx, errCh)
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
	var readWg sync.WaitGroup
	var reconnectWg sync.WaitGroup

	// Запускаем горутины для чтения
	readWg.Add(2)
	go func() {
		defer readWg.Done()
		readFromBackendNoWait(backend, clientConn, clientCtx, errCh)
	}()
	go func() {
		defer readWg.Done()
		readFromClientNoWait(clientConn, backend, clientCtx, errCh)
	}()

	// Запускаем горутину для обработки переподключений
	reconnectWg.Add(1)
	go func() {
		defer reconnectWg.Done()
		handleReconnection(backend, clientConn, clientCtx, r, errCh)
	}()

	// Ждем завершения начальных горутин чтения
	readWg.Wait()
	// Закрываем канал ошибок, чтобы handleReconnection завершился
	close(errCh)
	// Ждем завершения горутины переподключения
	reconnectWg.Wait()
}
