package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const appVersion = "1.0"

// appAutor erscheint in der Fußzeile der Oberfläche und im Konsolenfenster.
const appAutor = "Derek"

type App struct {
	st   *Store
	port int

	smu      sync.Mutex
	sessions map[string]time.Time

	lmu       sync.Mutex
	failures  int
	strikes   uint
	lockUntil time.Time
}

// ---------------------------------------------------------------- Helfer

type apiError struct {
	Code int
	Msg  string
}

func (e apiError) Error() string { return e.Msg }

func bad(msg string) error      { return apiError{http.StatusBadRequest, msg} }
func notFound(msg string) error { return apiError{http.StatusNotFound, msg} }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var ae apiError
	if errors.As(err, &ae) {
		writeJSON(w, ae.Code, map[string]string{"error": ae.Msg})
		return
	}
	fmt.Fprintln(os.Stderr, "Interner Fehler:", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Interner Fehler. Einzelheiten stehen im Konsolenfenster des Programms."})
}

func readJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		return bad("Ungültige Eingabe")
	}
	return nil
}

func (a *App) yearParam(r *http.Request) int {
	if y, err := strconv.Atoi(r.URL.Query().Get("year")); err == nil {
		return y
	}
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	return a.st.d.Settings.Jahr
}

// ---------------------------------------------------------------- Schutz

func (a *App) allowedHosts() map[string]bool {
	p := strconv.Itoa(a.port)
	return map[string]bool{"127.0.0.1:" + p: true, "localhost:" + p: true, "[::1]:" + p: true}
}

// guard schützt die lokale Oberfläche vor fremden Webseiten (DNS-Rebinding, CSRF).
func (a *App) guard(next http.Handler) http.Handler {
	hosts := a.allowedHosts()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hosts[r.Host] {
			http.Error(w, "Zugriff nur über 127.0.0.1", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || !hosts[u.Host] {
				http.Error(w, "Fremde Herkunft nicht erlaubt", http.StatusForbidden)
				return
			}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-GA-Request") != "1" {
				http.Error(w, "Anfrage abgelehnt", http.StatusForbidden)
				return
			}
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		if !strings.HasSuffix(r.URL.Path, ".pdf") {
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-src 'self'; object-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'self'")
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- Admin-Anmeldung

func hashPassword(pw string, salt []byte, iter int) string {
	k, err := pbkdf2.Key(sha256.New, pw, salt, iter, 32)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(k)
}

func (a *App) hasPassword() bool {
	return a.st.d.Admin.Hash != ""
}

func (a *App) sessionValid(r *http.Request) bool {
	c, err := r.Cookie("ga_session")
	if err != nil {
		return false
	}
	a.smu.Lock()
	defer a.smu.Unlock()
	exp, ok := a.sessions[c.Value]
	if !ok || time.Now().After(exp) {
		delete(a.sessions, c.Value)
		return false
	}
	a.sessions[c.Value] = time.Now().Add(4 * time.Hour)
	return true
}

func (a *App) isAdmin(r *http.Request) bool {
	a.st.mu.Lock()
	has := a.hasPassword()
	a.st.mu.Unlock()
	if !has {
		return true // kein Passwort gesetzt: Admin-Bereich ist offen
	}
	return a.sessionValid(r)
}

func (a *App) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.isAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Bitte im Admin-Bereich anmelden"})
			return
		}
		h(w, r)
	}
}

func (a *App) checkPassword(pw string) bool {
	adm := a.st.d.Admin
	salt, err := hex.DecodeString(adm.Salt)
	if err != nil || adm.Hash == "" {
		return false
	}
	got := hashPassword(pw, salt, adm.Iter)
	return subtle.ConstantTimeCompare([]byte(got), []byte(adm.Hash)) == 1
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, err)
		return
	}
	// Sperre und Prüfung laufen unter einer gemeinsamen Sperre. So kann niemand mit vielen
	// gleichzeitigen Anfragen die Fehlversuchs-Sperre umgehen.
	a.st.mu.Lock()
	has := a.hasPassword()
	a.lmu.Lock()
	if time.Now().Before(a.lockUntil) {
		wait := time.Until(a.lockUntil).Round(time.Second)
		a.lmu.Unlock()
		a.st.mu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": fmt.Sprintf("Zu viele Fehlversuche. Bitte %s warten.", wait)})
		return
	}
	a.lmu.Unlock()
	ok := has && a.checkPassword(in.Password)
	if has && !ok {
		a.lmu.Lock()
		a.failures++
		if a.failures >= 5 {
			// Sperrzeit verdoppelt sich mit jeder Serie: 30 s, 1 min, 2 min ... höchstens 15 min
			d := 30 * time.Second << a.strikes
			if d > 15*time.Minute {
				d = 15 * time.Minute
			}
			if a.strikes < 10 {
				a.strikes++
			}
			a.lockUntil = time.Now().Add(d)
			a.failures = 0
		}
		a.lmu.Unlock()
		a.st.mu.Unlock()
		time.Sleep(700 * time.Millisecond)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Falsches Passwort"})
		return
	}
	a.st.mu.Unlock()
	a.lmu.Lock()
	a.failures, a.strikes = 0, 0
	a.lmu.Unlock()
	a.startSession(w)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) startSession(w http.ResponseWriter) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	a.smu.Lock()
	a.sessions[tok] = time.Now().Add(4 * time.Hour)
	a.smu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "ga_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("ga_session"); err == nil {
		a.smu.Lock()
		delete(a.sessions, c.Value)
		a.smu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "ga_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handlePassword setzt, ändert oder entfernt das Admin-Passwort.
func (a *App) handlePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, err)
		return
	}
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	if a.hasPassword() {
		if !a.sessionValid(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Bitte im Admin-Bereich anmelden"})
			return
		}
		if !a.checkPassword(in.Old) {
			time.Sleep(700 * time.Millisecond)
			writeErr(w, bad("Das bisherige Passwort stimmt nicht"))
			return
		}
	}
	if in.New == "" {
		a.st.d.Admin = AdminAuth{}
	} else {
		if len([]rune(in.New)) < 8 {
			writeErr(w, bad("Das Passwort muss mindestens 8 Zeichen haben"))
			return
		}
		if len(in.New) > 200 {
			writeErr(w, bad("Das Passwort ist zu lang"))
			return
		}
		salt := make([]byte, 16)
		_, _ = rand.Read(salt)
		const iter = 600000
		a.st.d.Admin = AdminAuth{Salt: hex.EncodeToString(salt), Iter: iter, Hash: hashPassword(in.New, salt, iter)}
	}
	if err := a.st.saveLocked(); err != nil {
		writeErr(w, err)
		return
	}
	if in.New != "" {
		a.startSession(w)
	}
	writeJSON(w, 200, map[string]bool{"ok": true, "hasPassword": in.New != ""})
}

// ---------------------------------------------------------------- Zustand

type stateResp struct {
	Autor       string                `json:"autor"`
	Version     string                `json:"version"`
	DataDir     string                `json:"dataDir"`
	CurrentYear int                   `json:"currentYear"`
	Years       []int                 `json:"years"`
	Year        int                   `json:"year"`
	ReadOnly    bool                  `json:"readOnly"`
	Settings    Settings              `json:"settings"`
	Paechter    []Paechter            `json:"paechter"`
	Ablesungen  map[string]Ablesung   `json:"ablesungen"`
	Results     map[string]Result     `json:"results"`
	HasPassword bool                  `json:"hasPassword"`
	LoggedIn    bool                  `json:"loggedIn"`
	Issued      map[string]issuedInfo `json:"issued"`
	Archive     []archiveEntry        `json:"archive"`
}

func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	year := a.yearParam(r)
	a.st.mu.Lock()
	has := a.hasPassword()
	a.st.mu.Unlock()
	loggedIn := has && a.sessionValid(r)

	a.st.mu.Lock()
	v, ok := a.st.viewLocked(year)
	if !ok {
		a.st.mu.Unlock()
		writeErr(w, notFound("Dieses Jahr gibt es nicht"))
		return
	}
	res := stateResp{
		Autor: appAutor, Version: appVersion, DataDir: a.st.dir, CurrentYear: a.st.d.Settings.Jahr, Years: a.st.years(),
		Year: year, ReadOnly: v.ReadOnly, Settings: v.Settings, Paechter: v.Paechter,
		Ablesungen: v.Ablesungen, Results: map[string]Result{}, HasPassword: has, LoggedIn: loggedIn,
	}
	if res.Paechter == nil {
		res.Paechter = []Paechter{}
	}
	for _, p := range v.Paechter {
		res.Results[p.ID] = calculate(v.Settings, p, v.Ablesungen[p.ID])
	}
	res.Issued, res.Archive = a.st.archiveViewLocked(v)
	// JSON innerhalb der Sperre erzeugen, weil die Maps geteilt sind
	raw, err := json.Marshal(res)
	a.st.mu.Unlock()
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

// ---------------------------------------------------------------- Ablesungen

func cleanAblesung(in Ablesung) (Ablesung, error) {
	for _, p := range []*float64{in.WasserVJ, in.WasserAkt, in.StromVJ, in.StromAkt, in.Stunden} {
		if !validNumPtr(p) {
			return in, bad("Zählerstände und Stunden müssen Zahlen ab 0 sein")
		}
	}
	for _, x := range []float64{in.Versicherung, in.Grundsteuer, in.Auslagen, in.Abschlag} {
		if !validNum(x) {
			return in, bad("Beträge müssen Zahlen ab 0 sein")
		}
	}
	in.Hinweis = trim(in.Hinweis, 300)
	return in, nil
}

func (a *App) handleAblesung(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	year := a.yearParam(r)
	var in Ablesung
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, err)
		return
	}
	in, err := cleanAblesung(in)
	if err != nil {
		writeErr(w, err)
		return
	}
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	if year != a.st.d.Settings.Jahr {
		writeErr(w, bad("Abgeschlossene Jahre können nicht mehr geändert werden"))
		return
	}
	var pa *Paechter
	for i := range a.st.d.Paechter {
		if a.st.d.Paechter[i].ID == id {
			pa = &a.st.d.Paechter[i]
		}
	}
	if pa == nil {
		writeErr(w, notFound("Pächter nicht gefunden"))
		return
	}
	j := a.st.ensureYear(year)
	j.Ablesungen[id] = in
	if err := a.st.saveLocked(); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, calculate(a.st.d.Settings, *pa, in))
}

// handleZaehler ändert nur die Zählernummern eines Pächters (auch für den Vorstand ohne Admin-Rechte).
func (a *App) handleZaehler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		WasserzaehlerNr string `json:"wasserzaehlerNr"`
		StromzaehlerNr  string `json:"stromzaehlerNr"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, err)
		return
	}
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.d.Paechter {
		if a.st.d.Paechter[i].ID == id {
			a.st.d.Paechter[i].WasserzaehlerNr = trim(in.WasserzaehlerNr, 40)
			a.st.d.Paechter[i].StromzaehlerNr = trim(in.StromzaehlerNr, 40)
			if err := a.st.saveLocked(); err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, 200, a.st.d.Paechter[i])
			return
		}
	}
	writeErr(w, notFound("Pächter nicht gefunden"))
}

// ---------------------------------------------------------------- Pächter (Admin)

func cleanPaechter(p Paechter) (Paechter, error) {
	p.Mitgliedsnr = trim(p.Mitgliedsnr, 20)
	p.Gartennr = trim(p.Gartennr, 20)
	p.Anrede = trim(p.Anrede, 30)
	p.Name = trim(p.Name, 100)
	p.Strasse = trim(p.Strasse, 100)
	p.PLZOrt = trim(p.PLZOrt, 100)
	p.Versand = trim(p.Versand, 30)
	p.WasserzaehlerNr = trim(p.WasserzaehlerNr, 40)
	p.StromzaehlerNr = trim(p.StromzaehlerNr, 40)
	if p.Mitgliedsnr == "" {
		return p, bad("Bitte eine Mitgliedsnummer eintragen")
	}
	if p.Name == "" {
		return p, bad("Bitte einen Namen eintragen")
	}
	if !validNum(p.Gartengroesse) {
		return p, bad("Die Gartengröße muss eine Zahl ab 0 sein")
	}
	if p.UmlageAbweichend != nil && !validNum(*p.UmlageAbweichend) {
		return p, bad("Die Umlage muss eine Zahl ab 0 sein")
	}
	return p, nil
}

func (s *Store) nrTaken(nr, exceptID string) bool {
	for _, q := range s.d.Paechter {
		if q.ID != exceptID && strings.EqualFold(strings.TrimSpace(q.Mitgliedsnr), nr) {
			return true
		}
	}
	return false
}

func (a *App) handlePaechterCreate(w http.ResponseWriter, r *http.Request) {
	var in Paechter
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, err)
		return
	}
	in, err := cleanPaechter(in)
	if err != nil {
		writeErr(w, err)
		return
	}
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	if a.st.nrTaken(in.Mitgliedsnr, "") {
		writeErr(w, bad("Diese Mitgliedsnummer gibt es schon"))
		return
	}
	in.ID = newID()
	a.st.d.Paechter = append(a.st.d.Paechter, in)
	a.st.ensureYear(a.st.d.Settings.Jahr).Ablesungen[in.ID] = Ablesung{}
	if err := a.st.saveLocked(); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, in)
}

func (a *App) handlePaechterUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in Paechter
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, err)
		return
	}
	in, err := cleanPaechter(in)
	if err != nil {
		writeErr(w, err)
		return
	}
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	if a.st.nrTaken(in.Mitgliedsnr, id) {
		writeErr(w, bad("Diese Mitgliedsnummer gibt es schon"))
		return
	}
	for i := range a.st.d.Paechter {
		if a.st.d.Paechter[i].ID == id {
			in.ID = id
			a.st.d.Paechter[i] = in
			if err := a.st.saveLocked(); err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, 200, in)
			return
		}
	}
	writeErr(w, notFound("Pächter nicht gefunden"))
}

func (a *App) handlePaechterDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.d.Paechter {
		if a.st.d.Paechter[i].ID == id {
			a.st.snapshotBackup("vor-Loeschen")
			a.st.d.Paechter = append(a.st.d.Paechter[:i], a.st.d.Paechter[i+1:]...)
			if j := a.st.d.Jahre[yearKey(a.st.d.Settings.Jahr)]; j != nil {
				delete(j.Ablesungen, id)
			}
			if err := a.st.saveLocked(); err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, 200, map[string]bool{"ok": true})
			return
		}
	}
	writeErr(w, notFound("Pächter nicht gefunden"))
}

// ---------------------------------------------------------------- Einstellungen (Admin)

func cleanSettings(in, cur Settings) (Settings, error) {
	in.Jahr = cur.Jahr // wird nur über den Jahreswechsel geändert
	in.VereinName = trim(in.VereinName, 120)
	in.Ort = trim(in.Ort, 80)
	in.Absenderzeile = trim(in.Absenderzeile, 250)
	in.RechnungsnrPraefix = trim(in.RechnungsnrPraefix, 20)
	in.BankName = trim(in.BankName, 80)
	in.IBAN = trim(in.IBAN, 42)
	in.BIC = trim(in.BIC, 15)
	if in.VereinName == "" {
		return in, bad("Bitte einen Vereinsnamen eintragen")
	}
	if _, err := parseDate(in.Rechnungsdatum); err != nil {
		return in, bad("Das Rechnungsdatum ist ungültig")
	}
	if _, err := parseDate(in.Zahlungsziel); err != nil {
		return in, bad("Das Zahlungsziel ist ungültig")
	}
	if in.EinspruchTage < 0 || in.EinspruchTage > 365 {
		return in, bad("Einspruchsfrist: bitte 0 bis 365 Tage")
	}
	for name, x := range map[string]float64{
		"Wasser Grundpreis": in.WasserGrundpreis, "Wasser Preis": in.WasserPreis, "Energie Grundpreis": in.EnergieGrundpreis,
		"Energie Preis": in.EnergiePreis, "Pflichtstunden": in.Pflichtstunden, "Stundengrenze": in.StundenObergrenze,
		"Vergütung je Stunde": in.VerguetungJeStd, "Nachzahlung je Stunde": in.NachzahlungJeStd, "Pacht je m²": in.PachtJeQm,
		"Vereinsfläche": in.VereinsflaecheQm, "Freie Gärten": in.FreieGaertenQm, "Vereinsbeitrag": in.Vereinsbeitrag,
		"Territorialverband": in.Territorialverband, "Umlage": in.UmlageStandard,
	} {
		if !validNum(x) {
			return in, bad(name + ": bitte eine Zahl ab 0 eingeben")
		}
	}
	return in, nil
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	var in Settings
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, err)
		return
	}
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	in, err := cleanSettings(in, a.st.d.Settings)
	if err != nil {
		writeErr(w, err)
		return
	}
	a.st.d.Settings = in
	if err := a.st.saveLocked(); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, in)
}

// ---------------------------------------------------------------- Jahreswechsel (Admin)

func clone[T any](v T) T {
	raw, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(raw, &out)
	return out
}

func carry(akt, vj *float64) *float64 {
	if akt != nil {
		v := *akt
		return &v
	}
	if vj != nil {
		v := *vj
		return &v
	}
	return nil
}

func addYear(date string) string {
	t, err := parseDate(date)
	if err != nil {
		return date
	}
	return t.AddDate(1, 0, 0).Format("2006-01-02")
}

func (a *App) handleNextYear(w http.ResponseWriter, r *http.Request) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	d := a.st.d
	cur := d.Settings.Jahr
	next := cur + 1
	if _, exists := d.Jahre[yearKey(next)]; exists {
		writeErr(w, bad("Das Folgejahr existiert schon"))
		return
	}
	a.st.snapshotBackup("vor-Jahreswechsel")
	old := a.st.ensureYear(cur)
	cs := clone(d.Settings)
	old.Settings = &cs
	old.Paechter = clone(d.Paechter)
	old.Abgeschlossen = true

	nj := &Jahr{Ablesungen: map[string]Ablesung{}}
	for _, p := range d.Paechter {
		o := old.Ablesungen[p.ID]
		nj.Ablesungen[p.ID] = Ablesung{
			WasserVJ: carry(o.WasserAkt, o.WasserVJ),
			StromVJ:  carry(o.StromAkt, o.StromVJ),
		}
	}
	d.Jahre[yearKey(next)] = nj
	d.Settings.Jahr = next
	d.Settings.Rechnungsdatum = addYear(d.Settings.Rechnungsdatum)
	d.Settings.Zahlungsziel = addYear(d.Settings.Zahlungsziel)
	if err := a.st.saveLocked(); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]int{"jahr": next})
}

// ---------------------------------------------------------------- Import (Admin)

func (a *App) handleImportPreview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeErr(w, bad("Datei konnte nicht gelesen werden"))
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, bad("Bitte eine Datei auswählen"))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		writeErr(w, bad("Datei konnte nicht gelesen werden"))
		return
	}
	var table [][]string
	raw := false
	name := strings.ToLower(hdr.Filename)
	switch {
	case strings.HasSuffix(name, ".xlsx"):
		table, err = readXLSX(data, "Mitglieder")
		raw = true
	case strings.HasSuffix(name, ".csv"), strings.HasSuffix(name, ".txt"):
		table, err = parseCSV(data)
	default:
		err = errors.New("Bitte eine .xlsx- oder .csv-Datei wählen")
	}
	if err != nil {
		writeErr(w, bad(err.Error()))
		return
	}
	a.st.mu.Lock()
	settings := a.st.d.Settings
	existing := append([]Paechter(nil), a.st.d.Paechter...)
	a.st.mu.Unlock()
	rows, warn, err := parseImport(table, raw, settings, existing)
	if err != nil {
		writeErr(w, bad(err.Error()))
		return
	}
	writeJSON(w, 200, map[string]any{"rows": rows, "warnings": warn})
}

func (a *App) handleImportApply(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Rows []ImportRow `json:"rows"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, err)
		return
	}
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	a.st.snapshotBackup("vor-Import")
	j := a.st.ensureYear(a.st.d.Settings.Jahr)
	added, updated := 0, 0
	for _, row := range in.Rows {
		p, err := cleanPaechter(row.Paechter)
		if err != nil {
			writeErr(w, bad(fmt.Sprintf("Zeile %d: %s", row.Zeile, err.Error())))
			return
		}
		idx := -1
		for i, q := range a.st.d.Paechter {
			if strings.EqualFold(strings.TrimSpace(q.Mitgliedsnr), p.Mitgliedsnr) {
				idx = i
			}
		}
		if idx >= 0 {
			old := a.st.d.Paechter[idx]
			p.ID = old.ID
			// Felder, die in der Datei fehlten, bleiben unverändert
			has := map[string]bool{}
			for _, f := range row.Felder {
				has[f] = true
			}
			if !has["Gartennr."] {
				p.Gartennr = old.Gartennr
			}
			if !has["Anrede"] {
				p.Anrede = old.Anrede
			}
			if !has["Straße"] {
				p.Strasse = old.Strasse
			}
			if !has["PLZ Ort"] {
				p.PLZOrt = old.PLZOrt
			}
			if !has["Versandart"] {
				p.Versand = old.Versand
			}
			if !has["Gartengröße"] {
				p.Gartengroesse = old.Gartengroesse
			}
			if !has["Umlage abweichend"] && !has["Umlage"] {
				p.UmlageAbweichend = old.UmlageAbweichend
			}
			if !has["Wasserzähler-Nr."] {
				p.WasserzaehlerNr = old.WasserzaehlerNr
			}
			if !has["Stromzähler-Nr."] {
				p.StromzaehlerNr = old.StromzaehlerNr
			}
			a.st.d.Paechter[idx] = p
			updated++
		} else {
			p.ID = newID()
			a.st.d.Paechter = append(a.st.d.Paechter, p)
			added++
		}
		if row.Ablesung != nil {
			ab, err := cleanAblesung(*row.Ablesung)
			if err != nil {
				writeErr(w, bad(fmt.Sprintf("Zeile %d: %s", row.Zeile, err.Error())))
				return
			}
			cur := j.Ablesungen[p.ID]
			// nur die Felder überschreiben, die die Datei enthielt
			if ab.WasserVJ != nil {
				cur.WasserVJ = ab.WasserVJ
			}
			if ab.WasserAkt != nil {
				cur.WasserAkt = ab.WasserAkt
			}
			if ab.StromVJ != nil {
				cur.StromVJ = ab.StromVJ
			}
			if ab.StromAkt != nil {
				cur.StromAkt = ab.StromAkt
			}
			if ab.Stunden != nil {
				cur.Stunden = ab.Stunden
			}
			if ab.Versicherung != 0 {
				cur.Versicherung = ab.Versicherung
			}
			if ab.Grundsteuer != 0 {
				cur.Grundsteuer = ab.Grundsteuer
			}
			if ab.Auslagen != 0 {
				cur.Auslagen = ab.Auslagen
			}
			if ab.Abschlag != 0 {
				cur.Abschlag = ab.Abschlag
			}
			if ab.Hinweis != "" {
				cur.Hinweis = ab.Hinweis
			}
			j.Ablesungen[p.ID] = cur
		} else if _, ok := j.Ablesungen[p.ID]; !ok {
			j.Ablesungen[p.ID] = Ablesung{}
		}
	}
	if err := a.st.saveLocked(); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]int{"neu": added, "aktualisiert": updated})
}

// ---------------------------------------------------------------- Rechnungen, Export

func safeName(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20, strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(strings.TrimSpace(b.String()), ".")
	rs := []rune(out)
	if len(rs) > max {
		rs = rs[:max]
	}
	return strings.TrimSpace(string(rs))
}

func invoiceFileName(s Settings, p Paechter) string {
	return safeName("Rechnung_"+invoiceNumber(s, p)+"_"+p.Name, 80) + ".pdf"
}

func findPaechter(v yearView, id string) (Paechter, bool) {
	for _, p := range v.Paechter {
		if p.ID == id {
			return p, true
		}
	}
	return Paechter{}, false
}

func (a *App) handleInvoice(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(r.PathValue("id"), ".pdf")
	year := a.yearParam(r)
	a.st.mu.Lock()
	v, ok := a.st.viewLocked(year)
	var (
		p     Paechter
		found bool
		ab    Ablesung
	)
	if ok {
		p, found = findPaechter(v, id)
		ab = v.Ablesungen[id]
	}
	settings := v.Settings
	a.st.mu.Unlock()
	if !ok || !found {
		writeErr(w, notFound("Pächter oder Jahr nicht gefunden"))
		return
	}
	res := calculate(settings, p, ab)
	pdf, err := buildInvoice(settings, p, ab, res)
	if err != nil {
		writeErr(w, bad(err.Error()))
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="`+invoiceFileName(settings, p)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(pdf)
}

func (a *App) handleExport(w http.ResponseWriter, r *http.Request) {
	year := a.yearParam(r)
	a.st.mu.Lock()
	v, ok := a.st.viewLocked(year)
	var data []byte
	var err error
	if ok {
		data, err = exportOverview(v)
	}
	a.st.mu.Unlock()
	if !ok {
		writeErr(w, notFound("Jahr nicht gefunden"))
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="Jahresuebersicht_%d.xlsx"`, year))
	_, _ = w.Write(data)
}

func (a *App) handleBackup(w http.ResponseWriter, r *http.Request) {
	a.st.mu.Lock()
	raw, err := os.ReadFile(a.st.path)
	a.st.mu.Unlock()
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="gartenabrechnung-sicherung-`+time.Now().Format("2006-01-02")+`.json"`)
	_, _ = w.Write(raw)
}

func openPath(p string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", p)
	case "darwin":
		cmd = exec.Command("open", p)
	default:
		cmd = exec.Command("xdg-open", p)
	}
	_ = cmd.Start()
	if cmd.Process != nil {
		go func() { _ = cmd.Wait() }()
	}
}

func (a *App) handleOpenFolder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Which string `json:"which"`
		Year  int    `json:"year"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, err)
		return
	}
	dir := a.st.dir
	switch in.Which {
	case "rechnungen":
		dir = filepath.Join(a.st.dir, "Rechnungen")
		if in.Year > 0 {
			dir = filepath.Join(dir, strconv.Itoa(in.Year))
		}
	case "sicherungen":
		dir = a.st.backupDir()
	case "daten":
	default:
		writeErr(w, bad("Unbekannter Ordner"))
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeErr(w, err)
		return
	}
	openPath(dir)
	writeJSON(w, 200, map[string]string{"folder": dir})
}

func (a *App) handleQuit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"ok": true})
	go func() {
		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	}()
}

// ---------------------------------------------------------------- Routen

func (a *App) routes(static http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"app": "gartenabrechnung", "version": appVersion})
	})
	mux.HandleFunc("GET /api/state", a.handleState)
	mux.HandleFunc("PUT /api/ablesung/{id}", a.handleAblesung)
	mux.HandleFunc("PUT /api/zaehler/{id}", a.handleZaehler)
	mux.HandleFunc("GET /api/invoice/{id}", a.handleInvoice)
	mux.HandleFunc("POST /api/invoices/issue", a.handleIssue)
	mux.HandleFunc("GET /api/archive/{id}", a.handleArchivePDF)
	mux.HandleFunc("PUT /api/payment/{id}", a.handlePayment)
	mux.HandleFunc("GET /api/export-payments", a.handlePaymentsExport)
	mux.HandleFunc("GET /api/export", a.handleExport)
	mux.HandleFunc("POST /api/open-folder", a.handleOpenFolder)
	mux.HandleFunc("POST /api/quit", a.handleQuit)

	mux.HandleFunc("POST /api/admin/login", a.handleLogin)
	mux.HandleFunc("POST /api/admin/logout", a.handleLogout)
	mux.HandleFunc("POST /api/admin/password", a.handlePassword)
	mux.HandleFunc("POST /api/admin/paechter", a.admin(a.handlePaechterCreate))
	mux.HandleFunc("PUT /api/admin/paechter/{id}", a.admin(a.handlePaechterUpdate))
	mux.HandleFunc("DELETE /api/admin/paechter/{id}", a.admin(a.handlePaechterDelete))
	mux.HandleFunc("PUT /api/admin/settings", a.admin(a.handleSettings))
	mux.HandleFunc("POST /api/admin/jahreswechsel", a.admin(a.handleNextYear))
	mux.HandleFunc("POST /api/admin/import/preview", a.admin(a.handleImportPreview))
	mux.HandleFunc("POST /api/admin/import/apply", a.admin(a.handleImportApply))
	mux.HandleFunc("GET /api/admin/backup", a.admin(a.handleBackup))
	mux.Handle("/", static)
	return a.guard(mux)
}
