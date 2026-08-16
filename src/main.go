package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
)

type Config struct {
	DiscordToken      string `json:"discordToken"`
	AlertChannelID    string `json:"alertChannelId"`    // where stale-data alerts go (optional if ownerUserId set)
	OwnerUserID       string `json:"ownerUserId"`       // DM target for stale-data alerts (optional)
	PollSeconds       int    `json:"pollSeconds"`       // default 60 (minimum 60); the bulk endpoint caches ~5min, polling each minute catches refreshes promptly
	StaleAlertMinutes int    `json:"staleAlertMinutes"` // default 15; clamped to at least 3 poll intervals
	RepingHours       int    `json:"repingHours"`       // same seller+price re-announce window, default 4

	DailyBonusChannelID      string `json:"dailyBonusChannelId"`      // channel for the zone-bonus board ("" = off)
	BonusBoardRefreshMinutes int    `json:"bonusBoardRefreshMinutes"` // how often the board re-syncs, default 60
	DailyPostHour            int    `json:"dailyPostHour"`            // local hour for the fresh daily board post, default 3
	DailyPostMinute          int    `json:"dailyPostMinute"`          // local minute, default 10
}

type Bot struct {
	cfg        Config
	dg         *discordgo.Session
	api        *APIClient
	store      *Store
	startTs    time.Time
	mu         sync.Mutex
	lastData   time.Time // last successful poll
	staleAt    time.Time // when we last sent a stale alert
	isStale    bool
	lastErr    string
	sellerSeen map[string]sellerMark // key: userID|itemID|auctioneer -> last announced listing
	acCache    map[string]acEntry    // autocomplete catalog cache, keyed by lowercased query
	boardMu    sync.Mutex            // serializes bonus-board create/edit
	itemdb     *ItemDB               // local item stats/icons; nil when itemdb folder is absent
	zoneNames  []string              // bonus-zone names for autocomplete, refreshed with the board
}

type acEntry struct {
	choices []*discordgo.ApplicationCommandOptionChoice
	at      time.Time
}

func defaultConfig() Config {
	return Config{PollSeconds: 60, StaleAlertMinutes: 15, RepingHours: 4, BonusBoardRefreshMinutes: 60, DailyPostHour: 3, DailyPostMinute: 10}
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal(b, &cfg)
	if cfg.PollSeconds < 60 {
		cfg.PollSeconds = 60 // the bulk endpoint caches per item ~5min; hammering it gains nothing
	}
	if cfg.StaleAlertMinutes <= 0 {
		cfg.StaleAlertMinutes = 15
	}
	if cfg.StaleAlertMinutes*60 < 3*cfg.PollSeconds {
		cfg.StaleAlertMinutes = (3*cfg.PollSeconds + 59) / 60 // never alert inside a normal poll gap
	}
	if cfg.RepingHours < 1 {
		cfg.RepingHours = 4
	}
	if cfg.BonusBoardRefreshMinutes < 10 {
		cfg.BonusBoardRefreshMinutes = 60
	}
	if cfg.DailyPostHour < 0 || cfg.DailyPostHour > 23 {
		cfg.DailyPostHour = 3
	}
	if cfg.DailyPostMinute < 0 || cfg.DailyPostMinute > 59 {
		cfg.DailyPostMinute = 10
	}
	return cfg, err
}

func exeDir() string {
	p, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(p)
}

func main() {
	dir := exeDir()
	logPath := filepath.Join(dir, "bot.log")
	if fi, statErr := os.Stat(logPath); statErr == nil && fi.Size() > 10*1024*1024 {
		_ = os.Remove(logPath + ".old")
		_ = os.Rename(logPath, logPath+".old")
	}
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		log.SetOutput(io.MultiWriter(os.Stdout, logFile))
	}
	log.Printf("a_gnome_trader starting (server lock: %s)", serverName)

	cfgPath := filepath.Join(dir, "config.json")
	cfg, err := loadConfig(cfgPath)
	if err != nil && !os.IsNotExist(err) {
		log.Printf("ERROR: could not read config.json at %s: %v", cfgPath, err)
		log.Printf("Fix or delete config.json and run the bot again; deleting it restarts first-time setup.")
		waitForEnter()
		return
	}
	if err != nil || cfg.DiscordToken == "" || strings.Contains(cfg.DiscordToken, "PASTE") {
		c, cont := runSetup(dir)
		if !cont {
			return
		}
		cfg = c
	}

	bot := &Bot{
		cfg:        cfg,
		api:        NewAPIClient(),
		store:      NewStore(filepath.Join(dir, "watches.json"), filepath.Join(dir, "bonuswatches.json"), filepath.Join(dir, "state.json")),
		startTs:    time.Now(),
		lastData:   time.Now(), // grace period on boot
		sellerSeen: map[string]sellerMark{},
		acCache:    map[string]acEntry{},
	}
	if db, err := LoadItemDB(filepath.Join(dir, "itemdb")); err == nil {
		bot.itemdb = db
		log.Printf("Item database loaded: %d items, %d icons", db.Count(), len(db.byIco))
	} else {
		log.Printf("WARN: item database not loaded (%v), cards will omit item stats", err)
	}

	dg, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		log.Printf("ERROR: bad token: %v", err)
		waitForEnter()
		return
	}
	bot.dg = dg
	dg.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsDirectMessages

	dg.AddHandler(bot.onReady)
	dg.AddHandler(bot.onInteraction)
	dg.AddHandler(func(s *discordgo.Session, g *discordgo.GuildCreate) {
		bot.registerCommands(g.ID)
	})

	if err := dg.Open(); err != nil {
		log.Printf("ERROR: cannot connect to Discord: %v", err)
		waitForEnter()
		return
	}
	defer dg.Close()

	go bot.pollLoop()
	go bot.watchdogLoop()
	go bot.bonusBoardLoop()
	go bot.dailyBoardPostLoop()

	log.Printf("Bot is up. Checking watched Frostreaver items every %ds via the bulk sales API. Press Ctrl+C to stop.", cfg.PollSeconds)
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
	log.Printf("Shutting down.")
}

func waitForEnter() {
	fmt.Println("\nPress Enter to close this window...")
	fmt.Scanln()
}
