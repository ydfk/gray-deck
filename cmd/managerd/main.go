package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"mihomo-manager/internal/app"
)

func main() {
	cfg := app.LoadConfigFromEnv()

	server, err := app.NewServer(cfg)
	if err != nil {
		log.Fatalf("create server: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.ListenAndServe(ctx); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}
