package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"neongrid/internal/config"
	"neongrid/internal/game"
	"neongrid/internal/irc"
	"neongrid/internal/storage"
)

func main() {
	configPath := flag.String("config", os.Getenv("NEONGRID_CONFIG"), "YAML config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if dir := filepath.Dir(cfg.Database); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			log.Fatal(err)
		}
	}

	store, err := storage.Open(cfg.Database)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	engine, err := game.New(store, cfg.Rules(), nil, time.Now())
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	bot := irc.New(cfg, engine, log.Default())
	go runScheduler(ctx, engine, bot, cfg.Events.TickSeconds)
	if err := bot.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

func runScheduler(ctx context.Context, engine *game.Engine, bot *irc.Bot, seconds int) {
	ticker := time.NewTicker(time.Duration(seconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			messages, err := engine.Tick(now)
			if err != nil {
				log.Printf("game tick: %v", err)
				continue
			}
			for _, message := range messages {
				bot.Announce(message)
			}
		}
	}
}
