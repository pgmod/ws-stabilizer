package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

const (
	// Timeouts and intervals
	readDeadlineTimeout   = 30 * time.Second
	writeDeadlineTimeout  = 5 * time.Second
	serverShutdownTimeout = 5 * time.Second
	// HTTP Server timeouts for high concurrency
	httpReadTimeout  = 10 * time.Second
	httpWriteTimeout = 10 * time.Second
	httpIdleTimeout  = 120 * time.Second
	// Default max header size (1MB)
	maxHeaderBytes = 1 << 20
)

var (
	listenAddr        string
	backendURL        string
	dialTimeout       time.Duration
	retryBackoff      time.Duration
	disconnectedEvent string
	connectedEvent    string
	maxConnections    int
	showHelp          bool
)

func init() {
	flag.StringVar(&listenAddr, "listen", "", "address to listen for clients (required)")
	flag.StringVar(&listenAddr, "l", "", "alias for -listen")
	flag.StringVar(&backendURL, "backend", "ws://localhost:80/api/ws", "backend websocket URL")
	flag.StringVar(&backendURL, "b", "ws://localhost:80/api/ws", "alias for -backend")
	flag.DurationVar(&dialTimeout, "dial-timeout", 5*time.Second, "timeout for connecting to backend")
	flag.DurationVar(&dialTimeout, "t", 5*time.Second, "alias for -dial-timeout")
	flag.DurationVar(&retryBackoff, "retry-backoff", 200*time.Millisecond, "backoff between reconnect attempts")
	flag.DurationVar(&retryBackoff, "r", 200*time.Millisecond, "alias for -retry-backoff")
	flag.StringVar(&disconnectedEvent, "disconnected-event", "backend_disconnected", "event name sent when backend connection is lost")
	flag.StringVar(&disconnectedEvent, "de", "backend_disconnected", "alias for -disconnected-event")
	flag.StringVar(&connectedEvent, "connected-event", "backend_connected", "event name sent when backend connection is restored")
	flag.StringVar(&connectedEvent, "ce", "backend_connected", "alias for -connected-event")
	flag.IntVar(&maxConnections, "max-connections", 0, "maximum concurrent connections (0 = unlimited)")
	flag.IntVar(&maxConnections, "mc", 0, "alias for -max-connections")
	flag.BoolVar(&showHelp, "help", false, "show usage information")
	flag.BoolVar(&showHelp, "h", false, "alias for -help")
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `WebSocket Stabilizer - прокси для стабилизации WebSocket соединений

Использование:
  %s -listen <адрес> [опции]
  %s -l <адрес> [опции]

Пример:
  %s -listen :8080
  %s -l :8080
  %s -l 0.0.0.0:8080 -b wss://example.com/ws
  %s -l :8080 -mc 500

Параметры:
`, os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0])
	flag.PrintDefaults()
}

