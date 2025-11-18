package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	flag.Parse()

	if showHelp {
		printUsage()
		os.Exit(0)
	}

	if listenAddr == "" {
		fmt.Fprintf(os.Stderr, "Ошибка: параметр -listen обязателен\n\n")
		printUsage()
		os.Exit(1)
	}

	http.HandleFunc("/", handleWS)

	srv := &http.Server{Addr: listenAddr}

	go func() {
		log.Printf("WS proxy listening on %s -> backend %s", listenAddr, backendURL)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe: %v", err)
		}
	}()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	<-ch
	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
