package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const dataFileName = "gartenabrechnung-daten.json"

// Store hält den Datenbestand im Speicher und schreibt ihn sicher in eine Datei.
type Store struct {
	mu   sync.Mutex
	dir  string
	path string
	d    *Data
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func openStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// Schreibrechte prüfen
	probe := filepath.Join(dir, ".schreibtest")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		return nil, fmt.Errorf("Ordner %s ist nicht beschreibbar: %w", dir, err)
	}
	_ = os.Remove(probe)

	s := &Store{dir: dir, path: filepath.Join(dir, dataFileName)}
	raw, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.d = newData()
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		var d Data
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("Datendatei %s ist beschädigt (%v). Bitte eine Sicherung aus dem Ordner »Sicherungen« zurückkopieren", s.path, err)
		}
		if d.Jahre == nil {
			d.Jahre = map[string]*Jahr{}
		}
		if d.Paechter == nil {
			d.Paechter = []Paechter{}
		}
		if d.Rechnungen == nil {
			d.Rechnungen = []*Rechnung{}
		}
		s.d = &d
		s.ensureYear(d.Settings.Jahr)
	}
	return s, nil
}

func (s *Store) ensureYear(y int) *Jahr {
	k := yearKey(y)
	j := s.d.Jahre[k]
	if j == nil {
		j = &Jahr{}
		s.d.Jahre[k] = j
	}
	if j.Ablesungen == nil {
		j.Ablesungen = map[string]Ablesung{}
	}
	return j
}

// saveLocked schreibt atomar (erst temporäre Datei, dann umbenennen) und legt
// höchstens eine Tagessicherung an. Der Aufrufer hält s.mu.
func (s *Store) saveLocked() error {
	raw, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	s.dailyBackup()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) backupDir() string { return filepath.Join(s.dir, "Sicherungen") }

func (s *Store) dailyBackup() {
	old, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	dir := s.backupDir()
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	name := filepath.Join(dir, "gartenabrechnung-daten-"+time.Now().Format("2006-01-02")+".json")
	if _, err := os.Stat(name); err == nil {
		return // heute schon gesichert
	}
	_ = os.WriteFile(name, old, 0o600)
	// nur die letzten 60 Sicherungen behalten
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "gartenabrechnung-daten-") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > 60 {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}

// snapshotBackup legt eine benannte Sicherung an (z. B. vor dem Jahreswechsel).
func (s *Store) snapshotBackup(label string) {
	old, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	dir := s.backupDir()
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	name := fmt.Sprintf("gartenabrechnung-daten-%s-%s.json", time.Now().Format("2006-01-02_150405"), label)
	_ = os.WriteFile(filepath.Join(dir, name), old, 0o600)
}

// yearView liefert Einstellungen, Pächter und Ablesungen für ein Jahr.
// Für abgeschlossene Jahre kommen die eingefrorenen Kopien zurück.
type yearView struct {
	Jahr       int
	Settings   Settings
	Paechter   []Paechter
	Ablesungen map[string]Ablesung
	ReadOnly   bool
}

func (s *Store) viewLocked(year int) (yearView, bool) {
	j := s.d.Jahre[yearKey(year)]
	if j == nil {
		return yearView{}, false
	}
	v := yearView{Jahr: year, Ablesungen: j.Ablesungen}
	if j.Abgeschlossen && j.Settings != nil {
		v.Settings = *j.Settings
		v.Paechter = j.Paechter
		v.ReadOnly = true
	} else {
		v.Settings = s.d.Settings
		v.Paechter = s.d.Paechter
	}
	return v, true
}

func (s *Store) years() []int {
	var ys []int
	for k := range s.d.Jahre {
		var y int
		if _, err := fmt.Sscanf(k, "%d", &y); err == nil {
			ys = append(ys, y)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ys)))
	return ys
}
