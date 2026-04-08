package main

import (
	"log"

	"hysteria-keenetic/internal/app"
)

func main() {
	cfg := app.LoadConfigFromEnv()
	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("failed to initialize app: %v", err)
	}

	if err := application.Run(); err != nil {
		log.Fatalf("application exited with error: %v", err)
	}
}
