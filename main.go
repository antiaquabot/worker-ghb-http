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
	"github.com/stroi-homes/worker-ghb-http/internal/registrar"
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

	// Setup registrar
	reg := registrar.NewGHBRegistrar()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// smsCodeFn: ask the user for SMS code via Telegram message reply.
	// In this implementation the user types the code and sends it back to the bot.
	// The bot receives it via Telegram webhook/polling (future enhancement).
	// For now: prompt on stdin as a simple synchronous fallback.
	smsCodeFn := func(innerCtx context.Context) (string, error) {
		if err := tg.Send(innerCtx, "📲 Введите SMS-код, полученный от GHB, и отправьте его мне в ответ на это сообщение."); err != nil {
			log.Printf("telegram send error: %v", err)
		}
		// Await Telegram reply via long-poll (simplified: wait for next message in a background goroutine).
		// For a full implementation, integrate with Telegram Bot API getUpdates loop.
		// Here we block on stdin as a synchronous fallback (works for interactive use).
		log.Printf("[sms-code] waiting for SMS code on stdin (or Telegram)...")
		fmt.Print("Введите SMS-код: ")
		var code string
		if _, err := fmt.Scanln(&code); err != nil {
			return "", fmt.Errorf("read SMS code: %w", err)
		}
		return code, nil
	}

	// Event handler — called by SSE client or polling fallback
	handler := func(eventType, externalID string, data map[string]any) {
		if eventType != "REGISTRATION_OPENED" {
			return
		}
		entries := wl.Match(externalID)
		for _, entry := range entries {
			if entry.NotifyOnOpen {
				msg := tg.FormatRegistrationOpened(externalID, data)
				if err := tg.Send(ctx, msg); err != nil {
					log.Printf("telegram send error: %v", err)
				}
			}
			if entry.AutoRegister {
				go func(eid string) {
					if err := reg.Register(ctx, eid, cfg.PersonalData, smsCodeFn); err != nil {
						log.Printf("auto-register error for %s: %v", eid, err)
						if sendErr := tg.Send(ctx, tg.FormatRegistrationError(eid, err)); sendErr != nil {
							log.Printf("telegram send error: %v", sendErr)
						}
					} else {
						if sendErr := tg.Send(ctx, tg.FormatRegistrationSuccess(eid)); sendErr != nil {
							log.Printf("telegram send error: %v", sendErr)
						}
					}
				}(externalID)
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
