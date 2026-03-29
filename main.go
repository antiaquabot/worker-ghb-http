package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/stroi-homes/worker-ghb-http/internal/config"
	"github.com/stroi-homes/worker-ghb-http/internal/notifier"
	"github.com/stroi-homes/worker-ghb-http/internal/polling"
	"github.com/stroi-homes/worker-ghb-http/internal/sse"
	"github.com/stroi-homes/worker-ghb-http/internal/watchlist"
)

// DeveloperID is hardcoded at compile time — not read from config.
// All API URLs are constructed using this constant.
const DeveloperID = "ghb"

// Version is set by the build system via ldflags.
var Version = "dev"

func main() {
	var (
		configPath  = flag.String("config", "config.yaml", "path to config file")
		showVersion = flag.Bool("version", false, "print version and exit")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "worker-ghb-http %s\n\n", Version)
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  worker-ghb-http [flags]\n")
		fmt.Fprintf(os.Stderr, "  worker-ghb-http init --config config.yaml\n")
		fmt.Fprintf(os.Stderr, "  worker-ghb-http edit --config config.yaml\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Printf("worker-ghb-http %s (developer: %s)\n", Version, DeveloperID)
		return
	}

	// Subcommands
	if flag.NArg() > 0 {
		switch flag.Arg(0) {
		case "init":
			if err := config.InitConfig(*configPath); err != nil {
				log.Fatalf("init failed: %v", err)
			}
			return
		case "edit":
			if err := config.EditConfig(*configPath); err != nil {
				log.Fatalf("edit failed: %v", err)
			}
			return
		default:
			flag.Usage()
			os.Exit(1)
		}
	}

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	log.Printf("worker-ghb-http %s started (developer_id=%s)", Version, DeveloperID)

	// Setup notifier
	tg := notifier.New(cfg.Telegram.BotToken, cfg.Telegram.ChatID)

	// Setup watchlist matcher
	wl := watchlist.New(cfg.WatchList)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Event handler — called by SSE client or polling fallback
	handler := func(eventType, externalID string, data map[string]any) {
		entries := wl.Match(externalID)
		for _, entry := range entries {
			if !entry.NotifyOnOpen {
				continue
			}
			msg := tg.FormatRegistrationOpened(externalID, data)
			if err := tg.Send(ctx, msg); err != nil {
				log.Printf("telegram send error: %v", err)
			}
			if entry.AutoRegister {
				log.Printf("auto-register triggered for object %s (TODO: implement registrar)", externalID)
				// TODO: invoke GHBRegistrar once implemented
			}
		}
	}

	// Try SSE first; fall back to polling on failure
	if cfg.Service.UseSSE {
		sseClient := sse.New(cfg.Service.BaseURL, DeveloperID, handler)
		pollingClient := polling.New(cfg.Service.BaseURL, DeveloperID, cfg.Service.PollIntervalSeconds, handler)

		go func() {
			if err := sseClient.Run(ctx); err != nil {
				log.Printf("SSE client stopped (%v), switching to polling fallback", err)
				if notifyErr := tg.Send(ctx, "⚠️ SSE недоступен, переключился на REST-поллинг"); notifyErr != nil {
					log.Printf("telegram send error: %v", notifyErr)
				}
				if err := pollingClient.Run(ctx); err != nil {
					log.Printf("polling client stopped: %v", err)
				}
			}
		}()
	} else {
		pollingClient := polling.New(cfg.Service.BaseURL, DeveloperID, cfg.Service.PollIntervalSeconds, handler)
		go func() {
			if err := pollingClient.Run(ctx); err != nil {
				log.Printf("polling client stopped: %v", err)
			}
		}()
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")
	cancel()
}
