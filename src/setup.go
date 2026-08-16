package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// First-run setup wizard. Runs when config.json is missing or still holds the
// placeholder token. Walks the user through creating a Discord bot, inviting
// it, picking channels, and optionally installing to a chosen folder.

const setupRepoRaw = "https://raw.githubusercontent.com/Lisa-Mays/a_gnome_trader/main"

// invite permissions: View Channels, Send Messages, Embed Links, Attach Files
const invitePermissions = "52224"

var setupIn = bufio.NewReader(os.Stdin)

func ask(prompt string) string {
	fmt.Print(prompt)
	line, _ := setupIn.ReadString('\n')
	return strings.TrimSpace(line)
}

func askYN(prompt string, def bool) bool {
	hint := "Y/n"
	if !def {
		hint = "y/N"
	}
	for {
		a := strings.ToLower(ask(fmt.Sprintf("%s [%s]: ", prompt, hint)))
		switch a {
		case "":
			return def
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
		fmt.Println("Please answer y or n.")
	}
}

// askID reads an optional Discord snowflake ID. Empty input is allowed.
func askID(prompt string) string {
	for {
		a := ask(prompt)
		if a == "" {
			return ""
		}
		ok := len(a) >= 15
		for _, r := range a {
			if r < '0' || r > '9' {
				ok = false
				break
			}
		}
		if ok {
			return a
		}
		fmt.Println("That does not look like a Discord ID. IDs are long numbers, like 1183765012345678901. Press Enter to skip.")
	}
}

// runSetup drives the wizard. It returns the finished config and true when the
// bot should keep booting in this process. It returns false when the user
// aborted, or when the bot was installed to another folder and this copy
// should exit.
func runSetup(dir string) (Config, bool) {
	cfg := defaultConfig()

	fmt.Println()
	fmt.Println("=====================================================")
	fmt.Println("  a_gnome_trader first-time setup")
	fmt.Println("=====================================================")
	fmt.Println()
	fmt.Println("This bot watches the Frostreaver auction feed and posts sale")
	fmt.Println("alerts for items you track to your Discord server.")
	fmt.Println()
	fmt.Println("You will need:")
	fmt.Println("  1. A Discord account.")
	fmt.Println("  2. Permission to add a bot to the server where alerts should go.")
	fmt.Println()
	if runtime.GOOS == "linux" {
		fmt.Println("Prefer a point-and-click setup? Type w to open it in a browser.")
		fmt.Println("Prefer no wizard at all? Copy config.example.json to config.json and edit it.")
		a := ask("Press Enter to continue here, or w for the browser setup: ")
		if strings.EqualFold(a, "w") {
			if cfg2, ok, fell := runWebSetup(dir); !fell {
				return cfg2, ok
			}
			fmt.Println("Could not open a browser; continuing here.")
		}
	} else if !askYN("Start setup now?", true) {
		fmt.Println("Setup cancelled. Run this program again any time to restart it.")
		waitForEnter()
		return cfg, false
	}

	// Step 1: install folder.
	fmt.Println()
	fmt.Println("Step 1 of 5: choose an install folder")
	fmt.Println()
	fmt.Printf("The bot currently lives in: %s\n", dir)
	fmt.Println("It keeps its config, watch list, and logs next to the program file.")
	target := ask("Press Enter to use this folder, or type a full path to install somewhere else: ")
	if target != "" {
		target = filepath.Clean(target)
	}
	installDir := dir
	if target != "" && target != dir {
		if err := installTo(dir, target); err != nil {
			fmt.Printf("Could not install to %s: %v\n", target, err)
			fmt.Println("Continuing setup in the current folder instead.")
		} else {
			installDir = target
			fmt.Printf("Files copied to %s.\n", installDir)
		}
	}

	// Step 2: bot token.
	fmt.Println()
	fmt.Println("Step 2 of 5: create your Discord bot and get its token")
	fmt.Println()
	fmt.Println("  1. Open https://discord.com/developers/applications in your browser.")
	fmt.Println("  2. Click New Application, give it a name (for example Gnome Trader), click Create.")
	fmt.Println("  3. In the left menu click Bot.")
	fmt.Println("  4. Click Reset Token, confirm, then click Copy.")
	fmt.Println()
	var token string
	var sess *discordgo.Session
	for {
		token = ask("Paste your bot token here: ")
		token = strings.TrimPrefix(token, "Bot ")
		token = strings.Trim(token, "\"' ")
		if token == "" {
			fmt.Println("A token is required for the bot to run.")
			if askYN("Try again?", true) {
				continue
			}
			fmt.Println("Setup cancelled. Run this program again any time to restart it.")
			waitForEnter()
			return cfg, false
		}
		s, err := discordgo.New("Bot " + token)
		if err == nil {
			var u *discordgo.User
			u, err = s.User("@me")
			if err == nil {
				fmt.Printf("Token accepted. Your bot is named %s.\n", u.Username)
				sess = s
				break
			}
		}
		fmt.Printf("Could not verify that token: %v\n", err)
		fmt.Println("Double-check you copied the Bot token, not the Application ID or client secret.")
		if askYN("Try a different token?", true) {
			continue
		}
		if askYN("Keep this token anyway? Pick yes only if this machine is offline right now.", false) {
			break
		}
	}

	// Step 3: invite the bot.
	fmt.Println()
	fmt.Println("Step 3 of 5: invite the bot to your server")
	fmt.Println()
	inviteShown := false
	if sess != nil {
		if app, err := sess.Application("@me"); err == nil && app != nil {
			fmt.Println("Open this link in your browser, pick your server, and click Authorize:")
			fmt.Println()
			fmt.Printf("  https://discord.com/oauth2/authorize?client_id=%s&scope=bot+applications.commands&permissions=%s\n", app.ID, invitePermissions)
			fmt.Println()
			inviteShown = true
		}
	}
	if !inviteShown {
		fmt.Println("In the developer portal, open your application, click OAuth2 > URL Generator,")
		fmt.Println("check bot and applications.commands, check Send Messages, Embed Links, and")
		fmt.Println("Attach Files, then open the generated URL and add the bot to your server.")
	}
	ask("Press Enter once the bot has been added to your server... ")

	// Step 4: channels and owner.
	fmt.Println()
	fmt.Println("Step 4 of 5: channels and alerts (all optional, Enter skips any of them)")
	fmt.Println()
	fmt.Println("To copy Discord IDs you need Developer Mode: in Discord open")
	fmt.Println("User Settings > Advanced and turn on Developer Mode. Then right-click")
	fmt.Println("a channel or user and pick Copy ID.")
	fmt.Println()
	fmt.Println("Alert channel: where the bot warns you if the auction feed goes quiet.")
	cfg.AlertChannelID = askID("Alert channel ID (Enter to skip): ")
	fmt.Println()
	fmt.Println("Owner: your own user ID, so those warnings can reach you by DM instead.")
	cfg.OwnerUserID = askID("Your user ID (Enter to skip): ")
	fmt.Println()
	fmt.Println("Bonus board: a channel where the bot maintains a daily zone bonus board.")
	cfg.DailyBonusChannelID = askID("Bonus board channel ID (Enter to skip): ")
	fmt.Println()
	fmt.Println("Status message: the small line under the bot's name in the member list.")
	if st := ask(fmt.Sprintf("Status message [%s]: ", cfg.StatusText)); st != "" {
		cfg.StatusText = st
	}

	// Step 5: item database.
	fmt.Println()
	fmt.Println("Step 5 of 5: item database")
	fmt.Println()
	dbPath := filepath.Join(installDir, "itemdb", "oc-itemdb-all.json")
	if _, err := os.Stat(dbPath); err == nil {
		fmt.Println("Item database found. Sale alerts will include item stats and icons.")
	} else {
		fmt.Println("The item database adds stats and icons to sale alerts (about 21 MB).")
		if askYN("Download it now?", true) {
			if err := downloadItemDB(filepath.Join(installDir, "itemdb")); err != nil {
				fmt.Printf("Download failed: %v\n", err)
				fmt.Println("The bot still works without it; alerts just omit item stats.")
				fmt.Println("You can copy an itemdb folder next to the program later.")
			} else {
				fmt.Println("Item database downloaded.")
			}
		} else {
			fmt.Println("Skipped. The bot works without it; alerts just omit item stats.")
		}
	}

	// Write config.
	cfg.DiscordToken = token
	if err := writeConfig(filepath.Join(installDir, "config.json"), cfg); err != nil {
		fmt.Printf("ERROR: could not write config.json: %v\n", err)
		waitForEnter()
		return cfg, false
	}

	finalExe := mustExePath()
	if installDir != dir {
		finalExe = filepath.Join(installDir, filepath.Base(finalExe))
	}
	fmt.Println()
	if askYN("Create a desktop shortcut to the bot?", runtime.GOOS != "linux") {
		if err := createDesktopShortcut(finalExe, installDir); err != nil {
			fmt.Printf("Could not create the desktop shortcut: %v\n", err)
		} else {
			fmt.Println("Desktop shortcut created.")
		}
	}

	fmt.Println()
	fmt.Println("=====================================================")
	fmt.Println("  Setup complete")
	fmt.Println("=====================================================")
	fmt.Println()
	fmt.Printf("Settings saved to %s.\n", filepath.Join(installDir, "config.json"))
	fmt.Println("Once the bot is running, type /watch add in your server to track an item,")
	fmt.Println("and /help for everything else.")
	fmt.Println()

	if installDir != dir {
		if askYN("Start the installed copy now?", true) {
			if err := launchDetached(finalExe, installDir); err != nil {
				fmt.Printf("Could not start %s: %v\n", finalExe, err)
				fmt.Println("Start it manually from the install folder.")
				waitForEnter()
				return cfg, false
			}
			fmt.Println("The bot is now running in its own window; this setup window can be closed.")
		} else {
			fmt.Printf("Run the bot from %s when ready.\n", installDir)
		}
		waitForEnter()
		return cfg, false
	}

	fmt.Println("Starting the bot now.")
	fmt.Println()
	return cfg, true
}

func mustExePath() string {
	p, err := os.Executable()
	if err != nil {
		return "a_gnome_trader"
	}
	return p
}

// installTo copies the program file and the itemdb folder (when present) into
// target, creating it if needed.
func installTo(from, target string) error {
	if err := os.MkdirAll(target, 0755); err != nil {
		return err
	}
	src := mustExePath()
	dst := filepath.Join(target, filepath.Base(src))
	if err := copyFile(src, dst, 0755); err != nil {
		return fmt.Errorf("copying program: %w", err)
	}
	srcDB := filepath.Join(from, "itemdb")
	if fi, err := os.Stat(srcDB); err == nil && fi.IsDir() {
		if err := copyTree(srcDB, filepath.Join(target, "itemdb")); err != nil {
			return fmt.Errorf("copying itemdb: %w", err)
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(out, 0755)
		}
		return copyFile(p, out, fi.Mode().Perm())
	})
}

// downloadItemDB fetches the item database files from the project repository.
func downloadItemDB(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	files := []string{"oc-itemdb-all.json", "icons.zip"}
	client := &http.Client{Timeout: 5 * time.Minute}
	for _, name := range files {
		url := setupRepoRaw + "/itemdb/" + name
		fmt.Printf("Downloading %s... ", name)
		resp, err := client.Get(url)
		if err != nil {
			fmt.Println("failed")
			return err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			fmt.Println("failed")
			return fmt.Errorf("%s: HTTP %d", name, resp.StatusCode)
		}
		tmp := filepath.Join(dir, name+".part")
		out, err := os.Create(tmp)
		if err != nil {
			resp.Body.Close()
			fmt.Println("failed")
			return err
		}
		n, err := io.Copy(out, resp.Body)
		resp.Body.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(tmp)
			fmt.Println("failed")
			return err
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			os.Remove(tmp)
			fmt.Println("failed")
			return err
		}
		fmt.Printf("done (%.1f MB)\n", float64(n)/1024/1024)
	}
	return nil
}

func writeConfig(path string, cfg Config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}
