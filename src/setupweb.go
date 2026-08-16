package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Browser-based first-run setup. The bot serves a small local page, opens it
// in a window, and walks the user through the same steps as the console
// wizard, plus naming the bot and setting its status line.

//go:embed setup.html
var setupHTML []byte

type setupResult struct {
	cfg  Config
	cont bool
}

type setupServer struct {
	dir        string // where this program file lives
	installDir string // where config and data will be written
	token      string
	sess       *discordgo.Session
	botName    string
	statusText string
	done       chan setupResult
	srv        *http.Server
}

// runSetupFlow picks the setup style for the platform. Windows and macOS get
// the windowed wizard with a console fallback. Linux defaults to the console
// wizard and offers the browser one, since many Linux installs are headless
// and the config file is easy to edit by hand there.
func runSetupFlow(dir string) (Config, bool) {
	if runtime.GOOS == "linux" {
		return runSetup(dir)
	}
	cfg, ok, fellBack := runWebSetup(dir)
	if fellBack {
		return runSetup(dir)
	}
	return cfg, ok
}

// runWebSetup starts the local setup page. The third return value reports
// that the page could not be shown and the console wizard should run instead.
func runWebSetup(dir string) (Config, bool, bool) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Config{}, false, true
	}
	s := &setupServer{
		dir:        dir,
		installDir: dir,
		statusText: defaultConfig().StatusText,
		done:       make(chan setupResult, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(setupHTML)
	})
	mux.HandleFunc("/api/state", s.hState)
	mux.HandleFunc("/api/install", s.hInstall)
	mux.HandleFunc("/api/verify", s.hVerify)
	mux.HandleFunc("/api/identity", s.hIdentity)
	mux.HandleFunc("/api/itemdbstatus", s.hItemdbStatus)
	mux.HandleFunc("/api/finish", s.hFinish)
	s.srv = &http.Server{Handler: mux}
	go s.srv.Serve(ln)

	url := fmt.Sprintf("http://127.0.0.1:%d/", ln.Addr().(*net.TCPAddr).Port)
	fmt.Println()
	fmt.Println("First-time setup has opened in a window.")
	fmt.Printf("If nothing opened, paste this address into any browser: %s\n", url)
	fmt.Println()
	if !openSetupWindow(url) {
		s.srv.Close()
		return Config{}, false, true
	}

	res := <-s.done
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.srv.Shutdown(ctx)
	return res.cfg, res.cont, false
}

// openSetupWindow shows url in an app-style window when possible, otherwise
// in the default browser. Returns false when nothing could be opened.
func openSetupWindow(url string) bool {
	switch runtime.GOOS {
	case "windows":
		// Edge ships with Windows 10 and later; app mode gives a clean
		// chromeless window that looks like an installer.
		if exec.Command("cmd", "/c", "start", "msedge", "--app="+url).Run() == nil {
			return true
		}
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Run() == nil
	case "darwin":
		for _, app := range []string{"Google Chrome", "Microsoft Edge"} {
			if exec.Command("open", "-n", "-a", app, "--args", "--app="+url).Run() == nil {
				return true
			}
		}
		return exec.Command("open", url).Run() == nil
	default:
		return exec.Command("xdg-open", url).Run() == nil
	}
}

func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonIn(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func (s *setupServer) hState(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, map[string]any{
		"dir":           s.dir,
		"defaultStatus": s.statusText,
	})
}

func (s *setupServer) hInstall(w http.ResponseWriter, r *http.Request) {
	var in struct{ Path string }
	if err := jsonIn(r, &in); err != nil {
		jsonOut(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	target := strings.TrimSpace(in.Path)
	if target == "" || filepath.Clean(target) == s.dir {
		s.installDir = s.dir
		jsonOut(w, map[string]any{"ok": true, "dir": s.dir, "copied": false})
		return
	}
	target = filepath.Clean(target)
	if err := installTo(s.dir, target); err != nil {
		jsonOut(w, map[string]any{"ok": false, "error": fmt.Sprintf("Could not install to %s: %v", target, err)})
		return
	}
	s.installDir = target
	jsonOut(w, map[string]any{"ok": true, "dir": target, "copied": true})
}

func (s *setupServer) hVerify(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token string }
	if err := jsonIn(r, &in); err != nil {
		jsonOut(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	token := strings.Trim(strings.TrimPrefix(strings.TrimSpace(in.Token), "Bot "), "\"' ")
	sess, err := discordgo.New("Bot " + token)
	if err != nil {
		jsonOut(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	u, err := sess.User("@me")
	if err != nil {
		jsonOut(w, map[string]any{"ok": false, "error": fmt.Sprintf("Discord rejected that token: %v", err)})
		return
	}
	s.token = token
	s.sess = sess
	s.botName = u.Username
	inviteURL := ""
	if app, err := sess.Application("@me"); err == nil && app != nil {
		inviteURL = fmt.Sprintf("https://discord.com/oauth2/authorize?client_id=%s&scope=bot+applications.commands&permissions=%s", app.ID, invitePermissions)
	}
	jsonOut(w, map[string]any{
		"ok":        true,
		"botName":   u.Username,
		"avatarUrl": u.AvatarURL("64"),
		"inviteUrl": inviteURL,
	})
}

func (s *setupServer) hIdentity(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, Status string }
	if err := jsonIn(r, &in); err != nil {
		jsonOut(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if st := strings.TrimSpace(in.Status); st != "" {
		s.statusText = st
	}
	warning := ""
	name := strings.TrimSpace(in.Name)
	if s.sess != nil && name != "" && name != s.botName {
		if len(name) < 2 || len(name) > 32 {
			warning = "Bot names must be 2 to 32 characters; keeping the current name."
		} else if err := renameBot(s.sess, name); err != nil {
			warning = fmt.Sprintf("Could not rename the bot right now (%v). Discord limits renames to two per hour; you can rename it later in the developer portal.", err)
		} else {
			s.botName = name
		}
	}
	jsonOut(w, map[string]any{"ok": true, "botName": s.botName, "warning": warning})
}

// renameBot updates the bot account's username with a minimal PATCH so the
// avatar and everything else stay untouched.
func renameBot(sess *discordgo.Session, name string) error {
	data := struct {
		Username string `json:"username"`
	}{name}
	_, err := sess.RequestWithBucketID("PATCH", discordgo.EndpointUser("@me"), data, discordgo.EndpointUsers)
	return err
}

func (s *setupServer) hItemdbStatus(w http.ResponseWriter, r *http.Request) {
	_, err := os.Stat(filepath.Join(s.installDir, "itemdb", "oc-itemdb-all.json"))
	jsonOut(w, map[string]any{"present": err == nil})
}

func (s *setupServer) hFinish(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AlertChannelID string `json:"alertChannelId"`
		OwnerUserID    string `json:"ownerUserId"`
		BonusChannelID string `json:"bonusChannelId"`
		DownloadItemdb bool   `json:"downloadItemdb"`
	}
	if err := jsonIn(r, &in); err != nil {
		jsonOut(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if s.token == "" {
		jsonOut(w, map[string]any{"ok": false, "error": "No verified token; go back to the token step."})
		return
	}
	var warnings []string
	check := func(label, id string) string {
		id = strings.TrimSpace(id)
		if id == "" {
			return ""
		}
		for _, c := range id {
			if c < '0' || c > '9' {
				warnings = append(warnings, label+" does not look like a Discord ID and was skipped.")
				return ""
			}
		}
		return id
	}
	cfg := defaultConfig()
	cfg.DiscordToken = s.token
	cfg.StatusText = s.statusText
	cfg.AlertChannelID = check("Alert channel", in.AlertChannelID)
	cfg.OwnerUserID = check("Your user ID", in.OwnerUserID)
	cfg.DailyBonusChannelID = check("Bonus board channel", in.BonusChannelID)
	if s.sess != nil {
		if cfg.AlertChannelID != "" {
			if _, err := s.sess.Channel(cfg.AlertChannelID); err != nil {
				warnings = append(warnings, "The bot cannot see the alert channel yet; make sure it was invited to that server.")
			}
		}
		if cfg.DailyBonusChannelID != "" {
			if _, err := s.sess.Channel(cfg.DailyBonusChannelID); err != nil {
				warnings = append(warnings, "The bot cannot see the bonus board channel yet; make sure it was invited to that server.")
			}
		}
	}
	if in.DownloadItemdb {
		if err := downloadItemDB(filepath.Join(s.installDir, "itemdb")); err != nil {
			warnings = append(warnings, fmt.Sprintf("Item database download failed (%v). The bot works without it; alerts just omit item stats.", err))
		}
	}
	cfgPath := filepath.Join(s.installDir, "config.json")
	if err := writeConfig(cfgPath, cfg); err != nil {
		jsonOut(w, map[string]any{"ok": false, "error": fmt.Sprintf("Could not write config.json: %v", err)})
		return
	}
	installedElsewhere := s.installDir != s.dir
	jsonOut(w, map[string]any{
		"ok":                 true,
		"configPath":         cfgPath,
		"warnings":           warnings,
		"installedElsewhere": installedElsewhere,
		"installDir":         s.installDir,
	})
	if installedElsewhere {
		exe := filepath.Join(s.installDir, filepath.Base(mustExePath()))
		cmd := exec.Command(exe)
		cmd.Dir = s.installDir
		if err := cmd.Start(); err != nil {
			fmt.Printf("Setup finished in %s but the installed copy could not be started: %v\n", s.installDir, err)
			fmt.Println("Start it manually from the install folder.")
		} else {
			fmt.Println("Setup finished. The bot is now running from the install folder; this window can be closed.")
		}
		s.done <- setupResult{cfg, false}
		return
	}
	s.done <- setupResult{cfg, true}
}
