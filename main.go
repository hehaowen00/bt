// Command bt is a BitTorrent client for the terminal.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/gdamore/tcell/v2"

	"bt/app"
	"bt/torrent"
)

const appDir = "bt"

type config struct {
	downloadDir string
	port        int
	dht         bool
	seedRatio   float64
	theme       string
}

func xdg(env, fallback string) string {
	base := os.Getenv(env)
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}

		base = filepath.Join(home, fallback)
	}

	return filepath.Join(base, appDir)
}

func defaultDownloads() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}

	return filepath.Join(home, "Downloads")
}

// loadConfig reads name = value lines from $XDG_CONFIG_HOME/bt/config,
// with # for comments:
//
//	download_dir = ~/Downloads
//	port = 6881          # for incoming peers
//	dht = yes            # find peers without trackers
//	seed_ratio = 0       # stop seeding at this ratio; 0 never stops
//	theme = nord         # one of app.ThemeNames
func loadConfig() (config, error) {
	cfg := config{
		downloadDir: defaultDownloads(),
		port:        6881,
		dht:         true,
		theme:       app.DefaultTheme,
	}

	f, err := os.Open(filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "config"))
	if os.IsNotExist(err) {
		return cfg, nil
	}

	if err != nil {
		return cfg, err
	}

	defer f.Close()

	sc := bufio.NewScanner(f)
	n := 0

	for sc.Scan() {
		n++

		line, _, _ := strings.Cut(sc.Text(), "#")

		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)

		switch name {
		case "download_dir":
			cfg.downloadDir = expandHome(value)
		case "port":
			p, err := strconv.Atoi(value)
			if err != nil || p < 0 || p > 65535 {
				return cfg, fmt.Errorf("config line %d: port is a number up to 65535", n)
			}

			cfg.port = p
		case "dht":
			switch strings.ToLower(value) {
			case "yes", "true", "on", "1":
				cfg.dht = true
			case "no", "false", "off", "0":
				cfg.dht = false
			default:
				return cfg, fmt.Errorf("config line %d: dht is yes or no", n)
			}
		case "seed_ratio":
			r, err := strconv.ParseFloat(value, 64)
			if err != nil || r < 0 {
				return cfg, fmt.Errorf("config line %d: seed_ratio is a number, 0 or more", n)
			}

			cfg.seedRatio = r
		case "theme":
			if !slices.Contains(app.ThemeNames(), value) {
				return cfg, fmt.Errorf("config line %d: theme is one of %s", n, strings.Join(app.ThemeNames(), ", "))
			}

			cfg.theme = value
		default:
			return cfg, fmt.Errorf("config line %d: unknown setting %q", n, name)
		}
	}

	return cfg, sc.Err()
}

func expandHome(p string) string {
	rest, ok := strings.CutPrefix(p, "~")
	if !ok {
		return p
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}

	return filepath.Join(home, rest)
}

func main() {
	for _, a := range os.Args[1:] {
		if a == "-h" || a == "--help" {
			fmt.Println("usage: bt [file.torrent | magnet link]...")
			return
		}
	}

	cfg, cfgErr := loadConfig()

	var ui atomic.Pointer[app.App]

	sess, sessErr := torrent.New(torrent.Config{
		DownloadDir: cfg.downloadDir,
		StateDir:    xdg("XDG_STATE_HOME", filepath.Join(".local", "state")),
		Port:        cfg.port,
		DHT:         cfg.dht,
		SeedRatio:   cfg.seedRatio,
		Notify: func(name, msg string, isErr bool) {
			if a := ui.Load(); a != nil {
				a.Notify(name, msg, isErr)
			}
		},
	})

	screen, err := tcell.NewScreen()
	if err == nil {
		err = screen.Init()
	}

	if err != nil {
		sess.Close()
		fmt.Fprintln(os.Stderr, "bt:", err)
		os.Exit(1)
	}

	a := app.New(screen, sess, app.Config{
		DownloadDir: cfg.downloadDir,
		Theme:       cfg.theme,
	})
	ui.Store(a)

	switch {
	case cfgErr != nil:
		a.Error("%v", cfgErr)
	case sessErr != nil:
		a.Error("%v", sessErr)
	}

	for _, src := range os.Args[1:] {
		a.Add(src)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sigs
		signal.Stop(sigs)
		a.Quit()
	}()

	a.Run()
	screen.Fini()
	sess.Close()
}
