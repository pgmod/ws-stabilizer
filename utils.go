package main

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// isContextDone проверяет, отменен ли хотя бы один из контекстов
func isContextDone(ctxs ...context.Context) bool {
	for _, ctx := range ctxs {
		select {
		case <-ctx.Done():
			return true
		default:
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
	return websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure)
}

// sendEvent отправляет событие клиенту
func sendEvent(conn *websocket.Conn, event string) {
	conn.SetWriteDeadline(time.Now().Add(writeDeadlineTimeout))
	err := conn.WriteMessage(websocket.TextMessage, []byte(event))
	if err != nil {
		// Игнорируем ошибки записи - соединение может быть уже закрыто
		return
	}
	conn.SetWriteDeadline(time.Time{})
}

