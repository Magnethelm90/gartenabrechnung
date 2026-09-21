package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

//go:embed web
var webFS embed.FS

const defaultPort = 8765

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}

// dataDir bestimmt, wo die Daten liegen: neben der .exe, sonst im Benutzerordner.
func dataDir(flagDir string) string {
	if flagDir != "" {
		return flagDir
	}
	if env := os.Getenv("GARTENABRECHNUNG_DATEN"); env != "" {
		return env
	}
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		probe := filepath.Join(dir, ".schreibtest")
		if werr := os.WriteFile(probe, []byte("x"), 0o644); werr == nil {
			_ = os.Remove(probe)
			return dir
		}
	}
	if cfg, err := os.UserConfigDir(); err == nil {
		return filepath.Join(cfg, "Gartenabrechnung")
	}
	return "."
}

// alreadyRunning prüft, ob unter der Adresse schon unser Programm läuft.
func alreadyRunning(port int) bool {
	client := http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/ping", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var m map[string]string
	if json.NewDecoder(resp.Body).Decode(&m) != nil {
		return false
	}
	return m["app"] == "gartenabrechnung"
}

func main() {
	dirFlag := flag.String("data", "", "Ordner für Daten, Sicherungen und Rechnungen (Standard: neben der .exe)")
	noBrowser := flag.Bool("no-browser", false, "Browser nicht automatisch öffnen")
	port := flag.Int("port", defaultPort, "Port auf 127.0.0.1")
	resetAdmin := flag.Bool("reset-admin", false, "Admin-Passwort entfernen und beenden")
	flag.Parse()
	setupConsole()

	dir := dataDir(*dirFlag)
	st, err := openStore(dir)
	if err != nil {
		fmt.Println("FEHLER:", err)
		fmt.Println("Zum Beenden eine Taste druecken ...")
		_, _ = fmt.Scanln()
		os.Exit(1)
	}
	if *resetAdmin {
		st.mu.Lock()
		st.d.Admin = AdminAuth{}
		err := st.saveLocked()
		st.mu.Unlock()
		if err != nil {
			fmt.Println("FEHLER:", err)
			os.Exit(1)
		}
		fmt.Println("Das Admin-Passwort wurde entfernt.")
		return
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		if alreadyRunning(*port) {
			fmt.Println("Gartenabrechnung laeuft bereits - oeffne den Browser.")
			if !*noBrowser {
				openBrowser(fmt.Sprintf("http://127.0.0.1:%d/", *port))
			}
			return
		}
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Println("FEHLER: kein freier Port:", err)
			os.Exit(1)
		}
	}
	actual := ln.Addr().(*net.TCPAddr).Port

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	static := http.FileServerFS(sub)
	noCache := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	})
	app := &App{st: st, port: actual, sessions: map[string]time.Time{}}
	srv := &http.Server{
		Handler:           app.routes(noCache),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/", actual)
	fmt.Println("==============================================================")
	fmt.Println(" Gartenabrechnung", appVersion, "- Copyright (c)", time.Now().Year(), appAutor)
	fmt.Println("==============================================================")
	fmt.Println(" Das Programm laeuft. Dieses Fenster bitte offen lassen.")
	fmt.Println(" Adresse im Browser:", url)
	fmt.Println(" Daten liegen in:   ", dir)
	fmt.Println()
	fmt.Println(" Beenden: im Programm oben rechts auf \"Beenden\" klicken")
	fmt.Println(" oder dieses Fenster schliessen.")
	fmt.Println("==============================================================")
	if !*noBrowser {
		go func() {
			time.Sleep(300 * time.Millisecond)
			openBrowser(url)
		}()
	}
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fmt.Println("FEHLER:", err)
		os.Exit(1)
	}
}
