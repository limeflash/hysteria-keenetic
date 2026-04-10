package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hysteria-keenetic/internal/app"
)

func main() {
	cfg := app.LoadConfigFromEnv()
	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("failed to initialize app: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Println("received shutdown signal, cleaning up...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		application.Shutdown(shutdownCtx)
		os.Exit(0)
	}()

	if err := application.Run(); err != nil {
		log.Fatalf("application exited with error: %v", err)
	}
}
