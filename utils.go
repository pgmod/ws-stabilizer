package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// isContextDone проверяет, отменен ли хотя бы один из контекстов
func isContextDone(ctxs ...context.Context) bool {
	for _, ctx := range ctxs {
		if ctx.Err() != nil {
			return true
		}
	}
	return false
}

// isWebSocketHeader проверяет, является ли заголовок WebSocket-специфичным
func isWebSocketHeader(key string) bool {
	normalizedKey := http.CanonicalHeaderKey(key)
	excludedHeaders := map[string]bool{
		"Connection": true,
		"Upgrade":    true,
	}
	return excludedHeaders[normalizedKey] || strings.HasPrefix(strings.ToLower(normalizedKey), "sec-websocket")
}

// copyHeaders копирует заголовки из запроса, исключая WebSocket-специфичные
func copyHeaders(r *http.Request) http.Header {
	header := http.Header{}
	for k, vv := range r.Header {
		if !isWebSocketHeader(k) {
			for _, v := range vv {
				header.Add(k, v)
			}
		}
	}
	return header
}

// isTimeoutError проверяет, является ли ошибка таймаутом
func isTimeoutError(err error) bool {
	netErr, ok := err.(net.Error)
	return ok && netErr.Timeout()
}

// isCloseError проверяет, является ли ошибка нормальным закрытием соединения
func isCloseError(err error) bool {
	log.Printf("isCloseError: %v", err)
	return websocket.IsCloseError(err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
		websocket.CloseNoStatusReceived,
		websocket.CloseAbnormalClosure,
	)
}

// readMessageSafe безопасно читает сообщение с защитой от паники
func readMessageSafe(conn *websocket.Conn) (int, []byte, error) {
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
	if err == nil {
		conn.SetReadDeadline(time.Time{})
	}
	return mt, msg, err
}

// writeMessageSafe безопасно записывает сообщение с защитой от паники
func writeMessageSafe(conn *websocket.Conn, messageType int, data []byte) error {
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("write panic: %v", r)
			}
		}()
		conn.SetWriteDeadline(time.Now().Add(writeDeadlineTimeout))
		err = conn.WriteMessage(messageType, data)
	}()
	if err == nil {
		conn.SetWriteDeadline(time.Time{})
	}
	return err
}

// sendEvent отправляет событие клиенту
func sendEvent(conn *websocket.Conn, event string) {
	_ = writeMessageSafe(conn, websocket.TextMessage, []byte(event))
}

// safeSendError безопасно отправляет ошибку в канал, защищаясь от паники при закрытом канале
func safeSendError(errCh chan<- error, err error, ctxs ...context.Context) {
	if isContextDone(ctxs...) {
		return
	}

	defer func() {
		if r := recover(); r != nil {
			// Канал закрыт, игнорируем ошибку
		}
	}()

	select {
	case errCh <- err:
	default:
		// Канал полон или закрыт, игнорируем ошибку
	}
}
