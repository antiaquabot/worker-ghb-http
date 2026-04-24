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
	tg := notifier.New(cfg.Telegram.Enabled, cfg.Telegram.BotToken, cfg.Telegram.ChatID)

	// Setup watchlist matcher
	wl := watchlist.New(cfg.WatchList)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// smsCodeFn: ask the user for SMS code.
	// If Telegram enabled: send message with deadline via Telegram and wait for reply.
	// If Telegram disabled: prompt via terminal with deadline shown.
	var smsCodeFn func(context.Context) (string, error)
	if tg.IsEnabled() {
		smsCodeFn = func(innerCtx context.Context) (string, error) {
			deadline, _ := innerCtx.Deadline()
			msg := tg.FormatSMSCodeRequest(deadline)
			if err := tg.Send(innerCtx, msg); err != nil {
				log.Printf("telegram send error: %v", err)
				return "", err
			}
			log.Printf("[sms-code] waiting for SMS code from Telegram...")
			code, err := tg.WaitForCode(innerCtx, 0)
			if err != nil {
				return "", err
			}
			log.Printf("[sms-code] received code from Telegram: %s", code)
			return code, nil
		}
	} else {
		smsCodeFn = func(innerCtx context.Context) (string, error) {
			deadline, _ := innerCtx.Deadline()
			log.Printf("[sms-code] введите SMS-код до [%s]:", deadline.Format("02.01.2006 15:04:05"))
			var code string
			fmt.Scanln(&code)
			log.Printf("[sms-code] received code from terminal: %s", code)
			return code, nil
		}
	}

	// Event handler — called by SSE client or polling fallback
	handler := func(eventType, externalID string, data map[string]any) {
		if eventType != "REGISTRATION_OPENED" {
			return
		}
		entries := wl.Match(externalID)
		for _, entry := range entries {
			if entry.NotifyOnOpen {
				if tg.IsEnabled() {
					msg := tg.FormatRegistrationOpened(externalID, data)
					if err := tg.Send(ctx, msg); err != nil {
						log.Printf("telegram send error: %v", err)
					}
				} else {
					log.Printf("📦 Регистрация открыта: %s", externalID)
					if title, ok := data["title"].(string); ok && title != "" {
						log.Printf("   Название: %s", title)
					}
					if regURL, ok := data["registration_url"].(string); ok && regURL != "" {
						log.Printf("   Ссылка: %s", regURL)
					}
				}
			}
			if entry.AutoRegister {
				go func(eid string, data map[string]any) {
					regURL, _ := data["registration_url"].(string)
					if regURL == "" {
						log.Printf("missing registration_url for %s, skipping auto-register", eid)
						return
					}
					reg := registrar.NewGHBRegistrar()
					if err := reg.Register(ctx, eid, regURL, cfg.PersonalData, cfg.Registration, smsCodeFn); err != nil {
						log.Printf("auto-register error for %s: %v", eid, err)
						if tg.IsEnabled() {
							if sendErr := tg.Send(ctx, tg.FormatRegistrationError(eid, err)); sendErr != nil {
								log.Printf("telegram send error: %v", sendErr)
							}
						} else {
							log.Printf("❌ Ошибка авторегистрации: %s — %v", eid, err)
						}
					} else {
						if tg.IsEnabled() {
							if sendErr := tg.Send(ctx, tg.FormatRegistrationSuccess(eid)); sendErr != nil {
								log.Printf("telegram send error: %v", sendErr)
							}
						} else {
							log.Printf("✅ Авторегистрация выполнена: %s", eid)
						}
					}
				}(externalID, data)
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
				if tg.IsEnabled() {
					if notifyErr := tg.Send(ctx, "⚠️ SSE недоступен, переключился на REST-поллинг"); notifyErr != nil {
						log.Printf("telegram send error: %v", notifyErr)
					}
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
