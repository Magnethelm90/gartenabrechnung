package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testPort = 8765

func newTestApp(t *testing.T) (*App, http.Handler) {
	t.Helper()
	st, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := &App{st: st, port: testPort, sessions: map[string]time.Time{}}
	return app, app.routes(http.NotFoundHandler())
}

func do(h http.Handler, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-GA-Request", "1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("keine gültige Antwort (%d): %s", rec.Code, rec.Body.String())
	}
	return v
}

func TestZugriffsschutz(t *testing.T) {
	_, h := newTestApp(t)

	// richtiger Host: erlaubt
	if rec := do(h, "GET", "/api/ping", nil); rec.Code != 200 {
		t.Fatalf("ping: %d", rec.Code)
	}
	// falscher Host (DNS-Rebinding): verboten
	req := httptest.NewRequest("GET", "/api/state", nil)
	req.Host = "evil.example.com:8765"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("fremder Host: %d, erwartet 403", rec.Code)
	}
	// fremde Herkunft: verboten
	req = httptest.NewRequest("GET", "/api/state", nil)
	req.Host = "127.0.0.1:8765"
	req.Header.Set("Origin", "https://evil.example.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("fremde Herkunft: %d, erwartet 403", rec.Code)
	}
	// Schreibzugriff ohne Sonderkopfzeile (typisch für Formular-Angriffe): verboten
	req = httptest.NewRequest("POST", "/api/quit", nil)
	req.Host = "127.0.0.1:8765"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST ohne Kopfzeile: %d, erwartet 403", rec.Code)
	}
	// Sicherheits-Kopfzeilen
	rec = do(h, "GET", "/api/ping", nil)
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("Sicherheits-Kopfzeilen fehlen")
	}
}

func TestAdminPasswort(t *testing.T) {
	_, h := newTestApp(t)
	p := map[string]any{"mitgliedsnr": "1", "name": "A", "gartengroesse": 100}

	// ohne Passwort ist der Admin-Bereich offen
	if rec := do(h, "POST", "/api/admin/paechter", p); rec.Code != 200 {
		t.Fatalf("anlegen ohne Passwort: %d %s", rec.Code, rec.Body)
	}
	// Passwort setzen
	rec := do(h, "POST", "/api/admin/password", map[string]string{"new": "geheim123"})
	if rec.Code != 200 {
		t.Fatalf("Passwort setzen: %d %s", rec.Code, rec.Body)
	}
	// ab jetzt: ohne Anmeldung gesperrt
	for _, path := range []string{"/api/admin/paechter", "/api/admin/jahreswechsel", "/api/admin/import/apply"} {
		if rec := do(h, "POST", path, map[string]any{}); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s ohne Anmeldung: %d, erwartet 401", path, rec.Code)
		}
	}
	if rec := do(h, "PUT", "/api/admin/settings", defaultSettings()); rec.Code != http.StatusUnauthorized {
		t.Errorf("Einstellungen ohne Anmeldung: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/admin/backup", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("Backup ohne Anmeldung: %d", rec.Code)
	}
	// falsches Passwort
	if rec := do(h, "POST", "/api/admin/login", map[string]string{"password": "falsch"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("falsches Passwort: %d", rec.Code)
	}
	// richtiges Passwort
	rec = do(h, "POST", "/api/admin/login", map[string]string{"password": "geheim123"})
	if rec.Code != 200 || len(rec.Result().Cookies()) == 0 {
		t.Fatalf("Login: %d", rec.Code)
	}
	c := rec.Result().Cookies()[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Error("Cookie ist nicht HttpOnly/SameSite=Strict")
	}
	if rec := do(h, "POST", "/api/admin/paechter", map[string]any{"mitgliedsnr": "2", "name": "B", "gartengroesse": 50}, c); rec.Code != 200 {
		t.Errorf("anlegen nach Login: %d %s", rec.Code, rec.Body)
	}
	st := decode[stateResp](t, do(h, "GET", "/api/state", nil, c))
	if !st.HasPassword || !st.LoggedIn {
		t.Errorf("Zustand: hasPassword=%v loggedIn=%v", st.HasPassword, st.LoggedIn)
	}
}

func TestPasswortNichtImKlartext(t *testing.T) {
	app, h := newTestApp(t)
	do(h, "POST", "/api/admin/password", map[string]string{"new": "ganzgeheim99"})
	raw, err := os.ReadFile(app.st.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "ganzgeheim99") {
		t.Error("Passwort steht im Klartext in der Datendatei")
	}
	if !strings.Contains(string(raw), `"hash"`) {
		t.Error("Passwort-Hash fehlt")
	}
}

func TestJahreswechsel(t *testing.T) {
	app, h := newTestApp(t)
	rec := do(h, "POST", "/api/admin/paechter", map[string]any{"mitgliedsnr": "35-95", "name": "Test", "gartengroesse": 300})
	pid := decode[Paechter](t, rec).ID
	abl := Ablesung{WasserVJ: fp(148), WasserAkt: fp(191), StromVJ: fp(2136), StromAkt: fp(2302), Stunden: fp(20), Abschlag: 150}
	if rec := do(h, "PUT", "/api/ablesung/"+pid+"?year=2025", abl); rec.Code != 200 {
		t.Fatalf("Ablesung: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/admin/jahreswechsel", nil); rec.Code != 200 {
		t.Fatalf("Jahreswechsel: %d %s", rec.Code, rec.Body)
	}
	// Einstellungen: neues Jahr, Datum eine Jahr weiter
	if app.st.d.Settings.Jahr != 2026 || app.st.d.Settings.Rechnungsdatum != "2027-01-10" || app.st.d.Settings.Zahlungsziel != "2027-02-15" {
		t.Errorf("Einstellungen nach Wechsel: %+v", app.st.d.Settings)
	}
	// altes Jahr: eingefroren und schreibgeschützt
	old := decode[stateResp](t, do(h, "GET", "/api/state?year=2025", nil))
	if !old.ReadOnly || old.Settings.Jahr != 2025 || old.Settings.Rechnungsdatum != "2026-01-10" {
		t.Errorf("altes Jahr: readOnly=%v %+v", old.ReadOnly, old.Settings.Rechnungsdatum)
	}
	if rec := do(h, "PUT", "/api/ablesung/"+pid+"?year=2025", abl); rec.Code != http.StatusBadRequest {
		t.Errorf("Schreiben ins alte Jahr: %d, erwartet 400", rec.Code)
	}
	// altes Jahr: Rechnung lässt sich weiter erzeugen (mit den alten Preisen)
	if rec := do(h, "GET", "/api/invoice/"+pid+".pdf?year=2025", nil); rec.Code != 200 || !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF")) {
		t.Errorf("Rechnung altes Jahr: %d", rec.Code)
	}
	// neues Jahr: Vorjahresstände übernommen, Rest leer
	cur := decode[stateResp](t, do(h, "GET", "/api/state", nil))
	a := cur.Ablesungen[pid]
	if cur.Year != 2026 || cur.ReadOnly || a.WasserVJ == nil || *a.WasserVJ != 191 || *a.StromVJ != 2302 || a.WasserAkt != nil || a.Stunden != nil || a.Abschlag != 0 {
		t.Errorf("neues Jahr: year=%d ablesung=%+v", cur.Year, a)
	}
	// Preisänderung im neuen Jahr verändert das alte Jahr nicht
	s := cur.Settings
	s.WasserPreis = 9.99
	if rec := do(h, "PUT", "/api/admin/settings", s); rec.Code != 200 {
		t.Fatalf("Einstellungen: %d %s", rec.Code, rec.Body)
	}
	old = decode[stateResp](t, do(h, "GET", "/api/state?year=2025", nil))
	if old.Settings.WasserPreis != 2.04 {
		t.Errorf("altes Jahr hat neuen Preis: %v", old.Settings.WasserPreis)
	}
	// Sicherung vor dem Wechsel wurde angelegt
	entries, _ := os.ReadDir(app.st.backupDir())
	found := false
	for _, e := range entries {
		if strings.Contains(e.Name(), "vor-Jahreswechsel") {
			found = true
		}
	}
	if !found {
		t.Error("keine Sicherung vor dem Jahreswechsel")
	}
}

func TestPaechterVerwaltung(t *testing.T) {
	app, h := newTestApp(t)
	mk := func(nr, name string) *httptest.ResponseRecorder {
		return do(h, "POST", "/api/admin/paechter", map[string]any{"mitgliedsnr": nr, "name": name, "gartengroesse": 100})
	}
	if rec := mk("1", "A"); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	if rec := mk(" 1 ", "Doppelt"); rec.Code != http.StatusBadRequest {
		t.Errorf("doppelte Nummer: %d", rec.Code)
	}
	if rec := mk("", "Ohne"); rec.Code != http.StatusBadRequest {
		t.Errorf("ohne Nummer: %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/admin/paechter", map[string]any{"mitgliedsnr": "3", "name": "X", "gartengroesse": -5}); rec.Code != http.StatusBadRequest {
		t.Errorf("negative Größe: %d", rec.Code)
	}
	id := decode[Paechter](t, mk("2", "B")).ID
	// Zählernummern ohne Admin ändern
	if rec := do(h, "PUT", "/api/zaehler/"+id, map[string]string{"wasserzaehlerNr": "W1", "stromzaehlerNr": "S1"}); rec.Code != 200 {
		t.Errorf("Zähler: %d", rec.Code)
	}
	if app.st.d.Paechter[1].WasserzaehlerNr != "W1" {
		t.Error("Zählernummer nicht gespeichert")
	}
	// Ablesung mit ungültigen Werten
	if rec := do(h, "PUT", "/api/ablesung/"+id+"?year=2025", map[string]any{"wasserVJ": -1}); rec.Code != http.StatusBadRequest {
		t.Errorf("negativer Zählerstand: %d", rec.Code)
	}
	// löschen
	if rec := do(h, "DELETE", "/api/admin/paechter/"+id, nil); rec.Code != 200 {
		t.Errorf("löschen: %d", rec.Code)
	}
	if len(app.st.d.Paechter) != 1 {
		t.Errorf("Pächter nach Löschen: %d", len(app.st.d.Paechter))
	}
	if rec := do(h, "DELETE", "/api/admin/paechter/"+id, nil); rec.Code != http.StatusNotFound {
		t.Errorf("erneut löschen: %d", rec.Code)
	}
}

func TestDatenBleibenErhalten(t *testing.T) {
	dir := t.TempDir()
	st, _ := openStore(dir)
	app := &App{st: st, port: testPort, sessions: map[string]time.Time{}}
	h := app.routes(http.NotFoundHandler())
	do(h, "POST", "/api/admin/paechter", map[string]any{"mitgliedsnr": "7", "name": "Bleibt", "gartengroesse": 10})

	st2, err := openStore(dir) // Neustart
	if err != nil {
		t.Fatal(err)
	}
	if len(st2.d.Paechter) != 1 || st2.d.Paechter[0].Name != "Bleibt" {
		t.Errorf("nach Neustart: %+v", st2.d.Paechter)
	}
	// beschädigte Datei: verständlicher Fehler statt Datenverlust
	if err := os.WriteFile(filepath.Join(dir, dataFileName), []byte("{kaputt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openStore(dir); err == nil || !strings.Contains(err.Error(), "beschädigt") {
		t.Errorf("beschädigte Datei: %v", err)
	}
}

func TestImportCSVDeutsch(t *testing.T) {
	csvText := "\xef\xbb\xbfMitgliedsnr.;Name;Straße;Gartengröße (m²);Wasser Stand Vorjahr;Wasser Stand aktuell;Erbrachte Stunden\r\n" +
		"35-95;Mustermann, Max;Musterstr. 15;1.234,5;148;191,5;20\r\n" +
		"40-01;Müller;Hauptstr. 1;300;10;20;\r\n" +
		"35-95;Doppelt;;1;;;\r\n" +
		";Ohne Nummer;;;;;\r\n"
	table, err := parseCSV([]byte(csvText))
	if err != nil {
		t.Fatal(err)
	}
	rows, warn, err := parseImport(table, false, defaultSettings(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || len(warn) != 2 {
		t.Fatalf("rows=%d warn=%v", len(rows), warn)
	}
	r := rows[0]
	if r.Paechter.Name != "Mustermann, Max" || r.Paechter.Gartengroesse != 1234.5 || r.Ablesung == nil ||
		*r.Ablesung.WasserAkt != 191.5 || *r.Ablesung.Stunden != 20 {
		t.Errorf("Zeile 1: %+v %+v", r.Paechter, r.Ablesung)
	}
	if rows[1].Ablesung == nil || rows[1].Ablesung.Stunden != nil {
		t.Errorf("leere Stunden müssen leer bleiben: %+v", rows[1].Ablesung)
	}
	// Windows-1252-Datei (ä als Byte 0xE4)
	table, err = parseCSV([]byte("Mitgliedsnr;Name\r\n1;M\xfcller\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err = parseImport(table, false, defaultSettings(), nil)
	if err != nil || rows[0].Paechter.Name != "Müller" {
		t.Errorf("cp1252: %v %+v", err, rows)
	}
	// ohne Pflichtspalten
	table, _ = parseCSV([]byte("Foo;Bar\r\n1;2\r\n"))
	if _, _, err := parseImport(table, false, defaultSettings(), nil); err == nil {
		t.Error("Datei ohne Kopfzeile muss abgelehnt werden")
	}
}

func TestExcelRundreise(t *testing.T) {
	s := defaultSettings()
	s.Pflichtstunden = 14
	p := Paechter{ID: "a", Mitgliedsnr: "35-95", Gartennr: "35", Anrede: "Herr", Name: "Muster <&> \"Test\"", Strasse: "Str. 1", PLZOrt: "12345 Musterstadt",
		Versand: "Emailsendung", Gartengroesse: 300, WasserzaehlerNr: "W1", StromzaehlerNr: "S1"}
	a := Ablesung{WasserVJ: fp(148), WasserAkt: fp(191), StromVJ: fp(2136), StromAkt: fp(2302), Stunden: fp(20), Abschlag: 150, Hinweis: "Zeile1\nZeile2"}
	v := yearView{Jahr: 2025, Settings: s, Paechter: []Paechter{p}, Ablesungen: map[string]Ablesung{"a": a}}
	data, err := exportOverview(v)
	if err != nil {
		t.Fatal(err)
	}
	table, err := readXLSX(data, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(table) != 2 || table[1][3] != p.Name || table[1][18] != a.Hinweis {
		t.Fatalf("Rundreise: %d Zeilen, %q, %q", len(table), table[1][3], table[1][18])
	}
	// Export wieder importieren
	rows, warn, err := parseImport(table, true, s, nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("Import des Exports: %v %v", err, warn)
	}
	r := rows[0]
	if r.Paechter.Gartengroesse != 300 || r.Paechter.WasserzaehlerNr != "W1" || r.Ablesung == nil || *r.Ablesung.StromAkt != 2302 || r.Ablesung.Abschlag != 150 {
		t.Errorf("importierte Zeile: %+v %+v", r.Paechter, r.Ablesung)
	}
	// Gesamtbetrag im Export = 129,83
	if got := table[1][34]; got != "129.83" {
		t.Errorf("Gesamtbetrag im Export: %q", got)
	}
	// Datei, die keine Excel-Datei ist
	if _, err := readXLSX([]byte("kein zip"), ""); err == nil {
		t.Error("Nicht-Excel-Datei muss abgelehnt werden")
	}
}

func TestEinstellungenPruefung(t *testing.T) {
	app, h := newTestApp(t)
	s := defaultSettings()

	bad := s
	bad.Rechnungsdatum = "31.12.2025"
	if rec := do(h, "PUT", "/api/admin/settings", bad); rec.Code != http.StatusBadRequest {
		t.Errorf("ungültiges Datum: %d", rec.Code)
	}
	bad = s
	bad.WasserPreis = -1
	if rec := do(h, "PUT", "/api/admin/settings", bad); rec.Code != http.StatusBadRequest {
		t.Errorf("negativer Preis: %d", rec.Code)
	}
	bad = s
	bad.VereinName = "   "
	if rec := do(h, "PUT", "/api/admin/settings", bad); rec.Code != http.StatusBadRequest {
		t.Errorf("leerer Vereinsname: %d", rec.Code)
	}
	// Das Abrechnungsjahr lässt sich nur über den Jahreswechsel ändern
	ok := s
	ok.Jahr = 1999
	ok.WasserPreis = 2.5
	if rec := do(h, "PUT", "/api/admin/settings", ok); rec.Code != 200 {
		t.Fatalf("gültige Einstellungen: %d %s", rec.Code, rec.Body)
	}
	if app.st.d.Settings.Jahr != 2025 || app.st.d.Settings.WasserPreis != 2.5 {
		t.Errorf("Einstellungen: %+v", app.st.d.Settings)
	}
}

func TestRechnungsArchiv(t *testing.T) {
	app, h := newTestApp(t)
	mk := func(nr, name string) string {
		rec := do(h, "POST", "/api/admin/paechter", map[string]any{"mitgliedsnr": nr, "name": name, "gartengroesse": 300})
		return decode[Paechter](t, rec).ID
	}
	p1 := mk("35-95", "Voll")
	mk("35-96", "Leer")
	abl := Ablesung{WasserVJ: fp(148), WasserAkt: fp(191), StromVJ: fp(2136), StromAkt: fp(2302), Stunden: fp(20), Abschlag: 150}
	if rec := do(h, "PUT", "/api/ablesung/"+p1+"?year=2025", abl); rec.Code != 200 {
		t.Fatalf("Ablesung: %d", rec.Code)
	}
	type issueResp struct {
		Created []string    `json:"created"`
		Skipped []issueSkip `json:"skipped"`
	}
	// alle offenen ausstellen: nur der vollständige Pächter
	r := decode[issueResp](t, do(h, "POST", "/api/invoices/issue?year=2025", issueReq{}))
	if len(r.Created) != 1 || len(r.Skipped) != 1 || !strings.Contains(r.Skipped[0].Name, "Leer") {
		t.Fatalf("Ausstellen: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(app.st.dir, "Rechnungen", "2025", r.Created[0])); err != nil {
		t.Errorf("PDF-Datei fehlt: %v", err)
	}
	// zweites Mal: nichts Neues
	if r2 := decode[issueResp](t, do(h, "POST", "/api/invoices/issue?year=2025", issueReq{})); len(r2.Created) != 0 {
		t.Errorf("doppelt ausgestellt: %+v", r2)
	}
	st := decode[stateResp](t, do(h, "GET", "/api/state?year=2025", nil))
	inf, ok := st.Issued[p1]
	if !ok || inf.Geaendert || inf.Version != 1 || len(st.Archive) != 1 {
		t.Fatalf("Zustand nach Ausstellung: %+v archiv=%d", inf, len(st.Archive))
	}
	first := do(h, "GET", "/api/archive/"+inf.ID+".pdf", nil)
	if first.Code != 200 || !bytes.HasPrefix(first.Body.Bytes(), []byte("%PDF")) {
		t.Fatalf("Archiv-PDF: %d", first.Code)
	}
	// Preis und Name ändern: Archiv bleibt, Warnung erscheint
	s := st.Settings
	s.WasserPreis = 9.99
	do(h, "PUT", "/api/admin/settings", s)
	p := st.Paechter[0]
	p.Name = "Neuer Name"
	do(h, "PUT", "/api/admin/paechter/"+p1, p)
	st = decode[stateResp](t, do(h, "GET", "/api/state?year=2025", nil))
	if !st.Issued[p1].Geaendert {
		t.Error("Änderung nach Ausstellung wurde nicht erkannt")
	}
	if st.Archive[0].Name != "Voll" || st.Archive[0].Gesamt != inf.Gesamt {
		t.Errorf("Archiv hat sich verändert: %+v", st.Archive[0])
	}
	if again := do(h, "GET", "/api/archive/"+inf.ID+".pdf", nil); again.Code != 200 {
		t.Errorf("Archiv-PDF später: %d", again.Code)
	}
	// Ersetzen: neue Version, alte bleibt als »ersetzt«
	r3 := decode[issueResp](t, do(h, "POST", "/api/invoices/issue?year=2025", issueReq{IDs: []string{p1}, Replace: true}))
	if len(r3.Created) != 1 || !strings.Contains(r3.Created[0], "_v2") {
		t.Fatalf("Ersetzen: %+v", r3)
	}
	st = decode[stateResp](t, do(h, "GET", "/api/state?year=2025", nil))
	if len(st.Archive) != 2 || st.Archive[0].Status != statusErsetzt || st.Archive[1].Status != statusGueltig || st.Issued[p1].Version != 2 || st.Issued[p1].Geaendert {
		t.Errorf("nach Ersetzen: %+v", st.Archive)
	}
	// gelöschter Pächter: Rechnung bleibt im Archiv abrufbar
	do(h, "DELETE", "/api/admin/paechter/"+p1, nil)
	st = decode[stateResp](t, do(h, "GET", "/api/state?year=2025", nil))
	if len(st.Archive) != 2 {
		t.Errorf("Archiv nach Löschen: %d", len(st.Archive))
	}
	if rec := do(h, "GET", "/api/archive/"+st.Archive[1].ID+".pdf", nil); rec.Code != 200 {
		t.Errorf("PDF nach Löschen: %d", rec.Code)
	}
	// Archiv überlebt Neustart
	st2, err := openStore(app.st.dir)
	if err != nil || len(st2.d.Rechnungen) != 2 {
		t.Errorf("Archiv nach Neustart: %v", err)
	}
}

func TestZahlungen(t *testing.T) {
	_, h := newTestApp(t)
	rec := do(h, "POST", "/api/admin/paechter", map[string]any{"mitgliedsnr": "35-95", "name": "Zahler", "gartengroesse": 300})
	pid := decode[Paechter](t, rec).ID
	abl := Ablesung{WasserVJ: fp(148), WasserAkt: fp(191), StromVJ: fp(2136), StromAkt: fp(2302), Stunden: fp(20), Abschlag: 150}
	do(h, "PUT", "/api/ablesung/"+pid+"?year=2025", abl)
	do(h, "POST", "/api/invoices/issue?year=2025", issueReq{})
	st := decode[stateResp](t, do(h, "GET", "/api/state?year=2025", nil))
	id := st.Issued[pid].ID
	gesamt := st.Issued[pid].Gesamt
	if st.Archive[0].BezahltAm != "" || st.Archive[0].Faellig != "2026-02-15" {
		t.Fatalf("Ausgangszustand: %+v", st.Archive[0])
	}
	// Teilzahlung
	if rec := do(h, "PUT", "/api/payment/"+id, paymentReq{BezahltAm: "2026-01-20", BezahltBetrag: fp(50), Notiz: "Rate 1"}); rec.Code != 200 {
		t.Fatalf("Zahlung: %d %s", rec.Code, rec.Body)
	}
	st = decode[stateResp](t, do(h, "GET", "/api/state?year=2025", nil))
	e := st.Archive[0]
	if e.BezahltAm != "2026-01-20" || e.BezahltBetrag == nil || *e.BezahltBetrag != 50 || e.Notiz != "Rate 1" {
		t.Errorf("Teilzahlung: %+v", e)
	}
	// volle Zahlung: Betrag wird nicht extra gespeichert
	do(h, "PUT", "/api/payment/"+id, paymentReq{BezahltAm: "2026-01-25", BezahltBetrag: fp(gesamt)})
	st = decode[stateResp](t, do(h, "GET", "/api/state?year=2025", nil))
	if st.Archive[0].BezahltBetrag != nil {
		t.Errorf("Vollzahlung speichert Betrag: %v", *st.Archive[0].BezahltBetrag)
	}
	// Fehleingaben
	if rec := do(h, "PUT", "/api/payment/"+id, paymentReq{BezahltAm: "gestern"}); rec.Code != 400 {
		t.Errorf("ungültiges Datum: %d", rec.Code)
	}
	if rec := do(h, "PUT", "/api/payment/"+id, paymentReq{BezahltAm: "2026-01-25", BezahltBetrag: fp(-5)}); rec.Code != 400 {
		t.Errorf("negativer Betrag: %d", rec.Code)
	}
	if rec := do(h, "PUT", "/api/payment/gibtesnicht", paymentReq{}); rec.Code != 404 {
		t.Errorf("unbekannte Rechnung: %d", rec.Code)
	}
	// Ersetzen übernimmt den Zahlungsvermerk; Rücknahme möglich
	do(h, "PUT", "/api/ablesung/"+pid+"?year=2025", Ablesung{WasserVJ: fp(148), WasserAkt: fp(195), StromVJ: fp(2136), StromAkt: fp(2302), Stunden: fp(20), Abschlag: 150})
	do(h, "POST", "/api/invoices/issue?year=2025", issueReq{IDs: []string{pid}, Replace: true})
	st = decode[stateResp](t, do(h, "GET", "/api/state?year=2025", nil))
	if st.Archive[1].BezahltAm != "2026-01-25" {
		t.Errorf("Zahlungsvermerk nicht übernommen: %+v", st.Archive[1])
	}
	do(h, "PUT", "/api/payment/"+st.Archive[1].ID, paymentReq{})
	st = decode[stateResp](t, do(h, "GET", "/api/state?year=2025", nil))
	if st.Archive[1].BezahltAm != "" {
		t.Error("Rücknahme der Zahlung fehlgeschlagen")
	}
	// Excel-Export
	if rec := do(h, "GET", "/api/export-payments?year=2025&nur=offen", nil); rec.Code != 200 || !bytes.HasPrefix(rec.Body.Bytes(), []byte("PK")) {
		t.Errorf("Export: %d", rec.Code)
	}
}

func TestPasswortRichtlinieUndSperre(t *testing.T) {
	app, h := newTestApp(t)
	if rec := do(h, "POST", "/api/admin/password", map[string]string{"new": "kurz123"}); rec.Code != http.StatusBadRequest {
		t.Errorf("zu kurzes Passwort akzeptiert: %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/admin/password", map[string]string{"new": "langgenug1"}); rec.Code != 200 {
		t.Fatalf("Passwort setzen: %d", rec.Code)
	}
	// 40 gleichzeitige falsche Versuche: höchstens 5 dürfen überhaupt geprüft werden
	var mu sync.Mutex
	checked, locked := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := do(h, "POST", "/api/admin/login", map[string]string{"password": "falsch"})
			mu.Lock()
			defer mu.Unlock()
			switch rec.Code {
			case http.StatusUnauthorized:
				checked++
			case http.StatusTooManyRequests:
				locked++
			}
		}()
	}
	wg.Wait()
	if checked != 5 || locked != 35 {
		t.Errorf("Sperre umgangen: geprüft=%d gesperrt=%d (erwartet 5/35)", checked, locked)
	}
	// während der Sperre wird auch das richtige Passwort abgewiesen
	if rec := do(h, "POST", "/api/admin/login", map[string]string{"password": "langgenug1"}); rec.Code != http.StatusTooManyRequests {
		t.Errorf("richtiges Passwort während der Sperre: %d", rec.Code)
	}
	// die zweite Serie sperrt doppelt so lange
	app.lmu.Lock()
	first := time.Until(app.lockUntil)
	app.lockUntil = time.Now().Add(-time.Second)
	app.lmu.Unlock()
	for i := 0; i < 5; i++ {
		do(h, "POST", "/api/admin/login", map[string]string{"password": "falsch"})
	}
	app.lmu.Lock()
	second := time.Until(app.lockUntil)
	app.lmu.Unlock()
	if second < first+20*time.Second {
		t.Errorf("Sperrzeit steigt nicht: %v -> %v", first, second)
	}
}

func TestSicherheitsHeader(t *testing.T) {
	_, h := newTestApp(t)
	rec := do(h, "GET", "/api/ping", nil)
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff", "X-Frame-Options": "SAMEORIGIN", "Referrer-Policy": "no-referrer",
		"Cross-Origin-Resource-Policy": "same-origin",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, erwartet %q", k, got, want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, must := range []string{"default-src 'self'", "base-uri 'none'", "frame-ancestors 'self'"} {
		if !strings.Contains(csp, must) {
			t.Errorf("CSP ohne %q: %s", must, csp)
		}
	}
	if strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "script-src 'unsafe-inline'") {
		t.Errorf("CSP erlaubt Skript-Injektion: %s", csp)
	}
}
