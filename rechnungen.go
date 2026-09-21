package main

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	statusGueltig = "gueltig"
	statusErsetzt = "ersetzt"
)

// Rechnung ist eine ausgestellte (festgeschriebene) Rechnung. Sie enthält eine
// vollständige Kopie aller Werte von damals. Spätere Änderungen an Preisen,
// Pächterdaten oder Zählerständen verändern sie nicht.
type Rechnung struct {
	ID          string   `json:"id"`
	Jahr        int      `json:"jahr"`
	PaechterID  string   `json:"paechterId"`
	Nummer      string   `json:"nummer"`
	Version     int      `json:"version"`
	Status      string   `json:"status"` // gueltig | ersetzt
	Ausgestellt string   `json:"ausgestellt"`
	Datei       string   `json:"datei"` // relativ zum Datenordner
	Settings    Settings `json:"settings"`
	Paechter    Paechter `json:"paechter"`
	Ablesung    Ablesung `json:"ablesung"`
	Result      Result   `json:"result"`

	// Zahlungsstand (kann jederzeit geändert werden, gehört nicht zur festgeschriebenen Rechnung)
	BezahltAm     string   `json:"bezahltAm,omitempty"`     // JJJJ-MM-TT, leer = offen
	BezahltBetrag *float64 `json:"bezahltBetrag,omitempty"` // leer = voller Betrag
	Notiz         string   `json:"notiz,omitempty"`
}

// issuedInfo beschreibt die gültige Rechnung eines Pächters im aktuellen Jahr.
type issuedInfo struct {
	ID          string  `json:"id"`
	Nummer      string  `json:"nummer"`
	Version     int     `json:"version"`
	Ausgestellt string  `json:"ausgestellt"`
	Gesamt      float64 `json:"gesamt"`
	Geaendert   bool    `json:"geaendert"` // Werte weichen seit der Ausstellung ab
}

// archiveEntry ist eine Zeile im Rechnungsarchiv.
type archiveEntry struct {
	ID          string  `json:"id"`
	Mitgliedsnr string  `json:"mitgliedsnr"`
	Name        string  `json:"name"`
	Nummer      string  `json:"nummer"`
	Version     int     `json:"version"`
	Status      string  `json:"status"`
	Ausgestellt string  `json:"ausgestellt"`
	Gesamt      float64 `json:"gesamt"`
	Datei       string  `json:"datei"`

	Faellig       string   `json:"faellig"`
	BezahltAm     string   `json:"bezahltAm"`
	BezahltBetrag *float64 `json:"bezahltBetrag"`
	Notiz         string   `json:"notiz"`
}

// archiveViewLocked liefert die gültigen Rechnungen je Pächter und das Archiv
// des Jahres. Der Aufrufer hält s.mu.
func (s *Store) archiveViewLocked(v yearView) (map[string]issuedInfo, []archiveEntry) {
	issued := map[string]issuedInfo{}
	archive := []archiveEntry{}
	pByID := map[string]Paechter{}
	for _, p := range v.Paechter {
		pByID[p.ID] = p
	}
	for _, r := range s.d.Rechnungen {
		if r.Jahr != v.Jahr {
			continue
		}
		archive = append(archive, archiveEntry{
			ID: r.ID, Mitgliedsnr: r.Paechter.Mitgliedsnr, Name: r.Paechter.Name, Nummer: r.Nummer,
			Version: r.Version, Status: r.Status, Ausgestellt: r.Ausgestellt, Gesamt: r.Result.Gesamt, Datei: r.Datei,
			Faellig: r.Settings.Zahlungsziel, BezahltAm: r.BezahltAm, BezahltBetrag: r.BezahltBetrag, Notiz: r.Notiz,
		})
		if r.Status != statusGueltig {
			continue
		}
		info := issuedInfo{ID: r.ID, Nummer: r.Nummer, Version: r.Version, Ausgestellt: r.Ausgestellt, Gesamt: r.Result.Gesamt}
		if p, ok := pByID[r.PaechterID]; ok {
			info.Geaendert = !reflect.DeepEqual(r.Settings, v.Settings) ||
				!reflect.DeepEqual(r.Paechter, p) ||
				!reflect.DeepEqual(r.Ablesung, v.Ablesungen[r.PaechterID])
		} else {
			info.Geaendert = true // Pächter wurde inzwischen gelöscht
		}
		issued[r.PaechterID] = info
	}
	sort.SliceStable(archive, func(i, j int) bool {
		if archive[i].Mitgliedsnr != archive[j].Mitgliedsnr {
			return natLess(archive[i].Mitgliedsnr, archive[j].Mitgliedsnr)
		}
		return archive[i].Version < archive[j].Version
	})
	return issued, archive
}

// natLess sortiert Mitgliedsnummern natürlich (10-2 vor 10-10).
func natLess(a, b string) bool {
	as, bs := strings.Split(a, "-"), strings.Split(b, "-")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, e1 := strconv.Atoi(as[i])
		y, e2 := strconv.Atoi(bs[i])
		if e1 == nil && e2 == nil {
			if x != y {
				return x < y
			}
			continue
		}
		if as[i] != bs[i] {
			return as[i] < bs[i]
		}
	}
	if len(as) != len(bs) {
		return len(as) < len(bs)
	}
	return a < b
}

func (s *Store) validInvoiceLocked(year int, paechterID string) *Rechnung {
	for _, r := range s.d.Rechnungen {
		if r.Jahr == year && r.PaechterID == paechterID && r.Status == statusGueltig {
			return r
		}
	}
	return nil
}

type issueReq struct {
	IDs     []string `json:"ids"`     // leer = alle offenen
	Replace bool     `json:"replace"` // bereits ausgestellte Rechnung ersetzen (nur Admin)
}

type issueSkip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// handleIssue stellt Rechnungen aus: Werte einfrieren, PDF-Datei schreiben,
// Eintrag im Archiv speichern.
func (a *App) handleIssue(w http.ResponseWriter, r *http.Request) {
	var req issueReq
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.Replace && !a.isAdmin(r) {
		writeErr(w, apiError{http.StatusForbidden, "Zum Ersetzen einer ausgestellten Rechnung bitte im Admin-Bereich anmelden"})
		return
	}
	year := a.yearParam(r)

	type job struct {
		p   Paechter
		ab  Ablesung
		set Settings
		res Result
	}
	var jobs []job
	skipped := []issueSkip{}
	a.st.mu.Lock()
	v, ok := a.st.viewLocked(year)
	if !ok {
		a.st.mu.Unlock()
		writeErr(w, notFound("Jahr nicht gefunden"))
		return
	}
	want := map[string]bool{}
	for _, id := range req.IDs {
		want[id] = true
	}
	for _, p := range v.Paechter {
		if len(want) > 0 && !want[p.ID] {
			continue
		}
		label := p.Mitgliedsnr + " " + p.Name
		ab := clone(v.Ablesungen[p.ID])
		res := calculate(v.Settings, p, ab)
		if !res.Vollstaendig {
			skipped = append(skipped, issueSkip{label, res.Status})
			continue
		}
		if old := a.st.validInvoiceLocked(year, p.ID); old != nil && !req.Replace {
			if len(want) > 0 {
				skipped = append(skipped, issueSkip{label, "ist schon ausgestellt"})
			}
			continue
		}
		jobs = append(jobs, job{p: p, ab: ab, set: v.Settings, res: res})
	}
	a.st.mu.Unlock()

	dir := filepath.Join(a.st.dir, "Rechnungen", strconv.Itoa(year))
	if len(jobs) > 0 {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			writeErr(w, err)
			return
		}
	}
	type done struct {
		rec *Rechnung
	}
	var made []done
	names := []string{}
	for _, j := range jobs {
		pdf, err := buildInvoice(j.set, j.p, j.ab, j.res)
		if err != nil {
			skipped = append(skipped, issueSkip{j.p.Mitgliedsnr + " " + j.p.Name, err.Error()})
			continue
		}
		a.st.mu.Lock()
		version := 1
		for _, o := range a.st.d.Rechnungen {
			if o.Jahr == year && o.PaechterID == j.p.ID && o.Version >= version {
				version = o.Version + 1
			}
		}
		a.st.mu.Unlock()
		name := invoiceFileName(j.set, j.p)
		if version > 1 {
			name = strings.TrimSuffix(name, ".pdf") + "_v" + strconv.Itoa(version) + ".pdf"
		}
		if err := os.WriteFile(filepath.Join(dir, name), pdf, 0o600); err != nil {
			skipped = append(skipped, issueSkip{j.p.Mitgliedsnr + " " + j.p.Name, err.Error()})
			continue
		}
		rec := &Rechnung{
			ID: newID(), Jahr: year, PaechterID: j.p.ID, Nummer: invoiceNumber(j.set, j.p), Version: version,
			Status: statusGueltig, Ausgestellt: time.Now().Format(time.RFC3339),
			Datei:    filepath.ToSlash(filepath.Join("Rechnungen", strconv.Itoa(year), name)),
			Settings: j.set, Paechter: j.p, Ablesung: j.ab, Result: j.res,
		}
		made = append(made, done{rec})
		names = append(names, name)
	}

	if len(made) > 0 {
		a.st.mu.Lock()
		a.st.snapshotBackup("vor-Rechnungsausstellung")
		for _, m := range made {
			for _, o := range a.st.d.Rechnungen {
				if o.Jahr == year && o.PaechterID == m.rec.PaechterID && o.Status == statusGueltig {
					o.Status = statusErsetzt
					// Zahlungsvermerk übernehmen; der Betrag kann sich geändert haben und lässt sich ansehen und anpassen
					m.rec.BezahltAm, m.rec.BezahltBetrag, m.rec.Notiz = o.BezahltAm, o.BezahltBetrag, o.Notiz
				}
			}
			a.st.d.Rechnungen = append(a.st.d.Rechnungen, m.rec)
		}
		err := a.st.saveLocked()
		a.st.mu.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"folder": dir, "created": names, "skipped": skipped})
}

// handleArchivePDF liefert eine ausgestellte Rechnung. Das PDF wird aus den
// eingefrorenen Werten erzeugt und ist damit unabhängig von späteren Änderungen.
func (a *App) handleArchivePDF(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(r.PathValue("id"), ".pdf")
	var rec Rechnung
	found := false
	a.st.mu.Lock()
	for _, x := range a.st.d.Rechnungen {
		if x.ID == id {
			rec, found = *x, true
			break
		}
	}
	a.st.mu.Unlock()
	if !found {
		writeErr(w, notFound("Rechnung nicht im Archiv gefunden"))
		return
	}
	pdf, err := buildInvoice(rec.Settings, rec.Paechter, rec.Ablesung, rec.Result)
	if err != nil {
		writeErr(w, bad(err.Error()))
		return
	}
	name := invoiceFileName(rec.Settings, rec.Paechter)
	if rec.Version > 1 {
		name = strings.TrimSuffix(name, ".pdf") + "_v" + strconv.Itoa(rec.Version) + ".pdf"
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(pdf)
}

// openAmount ist der noch offene Betrag einer Rechnung (immer positiv; bei
// einem Guthaben der noch nicht ausgezahlte Betrag).
func openAmount(r *Rechnung) float64 {
	total := absf(r.Result.Gesamt)
	if r.BezahltAm == "" {
		return total
	}
	paid := total
	if r.BezahltBetrag != nil {
		paid = *r.BezahltBetrag
	}
	return round2(total - paid)
}

type paymentReq struct {
	BezahltAm     string   `json:"bezahltAm"`
	BezahltBetrag *float64 `json:"bezahltBetrag"`
	Notiz         string   `json:"notiz"`
}

// handlePayment vermerkt Zahlungseingang (oder nimmt ihn zurück).
func (a *App) handlePayment(w http.ResponseWriter, r *http.Request) {
	var req paymentReq
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	req.BezahltAm = strings.TrimSpace(req.BezahltAm)
	if req.BezahltAm != "" {
		if _, err := parseDate(req.BezahltAm); err != nil {
			writeErr(w, bad("Bitte ein gültiges Datum angeben"))
			return
		}
	}
	if !validNumPtr(req.BezahltBetrag) {
		writeErr(w, bad("Bitte einen gültigen Betrag angeben (0 oder mehr)"))
		return
	}
	id := r.PathValue("id")
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for _, x := range a.st.d.Rechnungen {
		if x.ID != id {
			continue
		}
		if req.BezahltAm == "" {
			x.BezahltAm, x.BezahltBetrag = "", nil
		} else {
			x.BezahltAm = req.BezahltAm
			x.BezahltBetrag = nil
			if req.BezahltBetrag != nil {
				b := round2(*req.BezahltBetrag)
				if b != round2(absf(x.Result.Gesamt)) {
					x.BezahltBetrag = &b // nur bei Teil- oder Mehrzahlung speichern
				}
			}
		}
		x.Notiz = trim(req.Notiz, 200)
		if err := a.st.saveLocked(); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "offen": openAmount(x)})
		return
	}
	writeErr(w, notFound("Rechnung nicht gefunden"))
}

// handlePaymentsExport liefert die Zahlungsübersicht des Jahres als Excel-Datei.
func (a *App) handlePaymentsExport(w http.ResponseWriter, r *http.Request) {
	year := a.yearParam(r)
	onlyOpen := r.URL.Query().Get("nur") == "offen"
	head := []string{"Mitgliedsnr.", "Name", "Rechnungs-Nr.", "Betrag (€)", "Fällig am", "Bezahlt am", "Bezahlt (€)", "Noch offen (€)", "Notiz"}
	rows := [][]xCell{}
	hdr := make([]xCell, len(head))
	for i, h := range head {
		hdr[i] = xCell{h, stHeader}
	}
	rows = append(rows, hdr)
	a.st.mu.Lock()
	var list []*Rechnung
	for _, x := range a.st.d.Rechnungen {
		if x.Jahr == year && x.Status == statusGueltig {
			list = append(list, x)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return natLess(list[i].Paechter.Mitgliedsnr, list[j].Paechter.Mitgliedsnr) })
	for _, x := range list {
		open := openAmount(x)
		if onlyOpen && open <= 0 {
			continue
		}
		var paid, paidDate any
		if x.BezahltAm != "" {
			paidDate = germanDate(x.BezahltAm)
			paid = absf(x.Result.Gesamt)
			if x.BezahltBetrag != nil {
				paid = *x.BezahltBetrag
			}
		}
		rows = append(rows, []xCell{
			{x.Paechter.Mitgliedsnr, stNormal}, {x.Paechter.Name, stNormal}, {x.Nummer, stNormal}, {x.Result.Gesamt, stNum},
			{germanDate(x.Settings.Zahlungsziel), stNormal}, {paidDate, stNormal}, {paid, stNum}, {open, stNum}, {x.Notiz, stNormal},
		})
	}
	a.st.mu.Unlock()
	data, err := writeXLSX("Zahlungen", []float64{12, 26, 14, 12, 12, 12, 12, 13, 30}, rows)
	if err != nil {
		writeErr(w, err)
		return
	}
	name := "Zahlungsuebersicht"
	if onlyOpen {
		name = "Offene_Posten"
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`_`+strconv.Itoa(year)+`.xlsx"`)
	_, _ = w.Write(data)
}
