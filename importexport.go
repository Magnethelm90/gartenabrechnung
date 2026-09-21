package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// ---------------------------------------------------------------- Import

type field int

const (
	fNone field = iota
	fMitgl
	fGarten
	fAnrede
	fName
	fStrasse
	fPLZ
	fVersand
	fGroesse
	fUmlageAbw
	fUmlage
	fWZ
	fSZ
	fWVJ
	fWAkt
	fSVJ
	fSAkt
	fStd
	fVers
	fGrund
	fAusl
	fHinweis
	fAbschlag
)

// normHeader macht aus einer Spaltenüberschrift einen Vergleichsschlüssel.
func normHeader(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss").Replace(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if r < 128 { // ², ³ usw. fallen weg
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func classify(n string) field {
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(n, s) {
				return true
			}
		}
		return false
	}
	pre := func(subs ...string) bool {
		for _, s := range subs {
			if strings.HasPrefix(n, s) {
				return true
			}
		}
		return false
	}
	wasser := has("wasser")
	strom := has("strom", "energie")
	switch {
	case pre("mitgliedsbeitrag", "mitgliedsbeit"):
		return fNone
	case pre("mitglied"):
		return fMitgl
	case pre("gartennr", "gartennummer", "parzelle"):
		return fGarten
	case n == "anrede":
		return fAnrede
	case n == "name" || n == "paechter" || n == "pachter":
		return fName
	case pre("strasse") || n == "str":
		return fStrasse
	case n == "plzort" || n == "plz" || n == "ort":
		return fPLZ
	case pre("versand"):
		return fVersand
	case pre("gartengroesse", "groesse", "flaeche", "quadratmeter"):
		return fGroesse
	case pre("umlageabw"):
		return fUmlageAbw
	case n == "umlage":
		return fUmlage
	case wasser && has("vorjahr") || wasser && strings.HasSuffix(n, "vj"):
		return fWVJ
	case wasser && has("aktuell") || wasser && strings.HasSuffix(n, "akt"):
		return fWAkt
	case wasser && has("zaehler"):
		return fWZ
	case strom && has("vorjahr") || strom && strings.HasSuffix(n, "vj"):
		return fSVJ
	case strom && has("aktuell") || strom && strings.HasSuffix(n, "akt"):
		return fSAkt
	case strom && has("zaehler"):
		return fSZ
	case has("stunden") && !has("nachzahlung", "verguetung"):
		return fStd
	case pre("versicherung"):
		return fVers
	case pre("grundsteuer"):
		return fGrund
	case has("auslagen"):
		return fAusl
	case pre("hinweis"):
		return fHinweis
	case pre("abschlag"):
		return fAbschlag
	}
	return fNone
}

// parseNumber liest Zahlen. raw=true: Excel-Rohwert (Punkt = Dezimaltrenner).
// raw=false: deutsche Schreibweise (1.234,56 oder 1234,5), zur Not auch 12.5.
func parseNumber(s string, raw bool) (float64, bool) {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer("€", "", "m²", "", "m³", "", "kWh", "", " ", "", " ", "").Replace(s)
	if s == "" {
		return 0, false
	}
	if !raw {
		if strings.Contains(s, ",") {
			s = strings.ReplaceAll(s, ".", "")
			s = strings.Replace(s, ",", ".", 1)
		} else if strings.Count(s, ".") > 1 {
			s = strings.ReplaceAll(s, ".", "")
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// ImportRow ist eine geprüfte Zeile aus der Importdatei.
type ImportRow struct {
	Zeile     int       `json:"zeile"`
	Aktion    string    `json:"aktion"` // "neu" oder "aktualisieren"
	Paechter  Paechter  `json:"paechter"`
	Ablesung  *Ablesung `json:"ablesung,omitempty"`
	Felder    []string  `json:"felder"` // welche Spalten in der Datei vorhanden waren
	Warnungen []string  `json:"warnungen"`
}

// parseImport wertet eine Tabelle (erste passende Kopfzeile in den ersten 15 Zeilen) aus.
func parseImport(table [][]string, raw bool, st Settings, existing []Paechter) ([]ImportRow, []string, error) {
	hdr := -1
	var colField map[int]field
	for i := 0; i < len(table) && i < 15; i++ {
		m := map[int]field{}
		hasMitgl, hasName := false, false
		for c, cell := range table[i] {
			f := classify(normHeader(cell))
			if f == fNone {
				continue
			}
			// erste passende Spalte gewinnt (spätere Spalten gleicher Art sind meist berechnete Werte)
			dup := false
			for _, ff := range m {
				if ff == f {
					dup = true
				}
			}
			if dup {
				continue
			}
			m[c] = f
			hasMitgl = hasMitgl || f == fMitgl
			hasName = hasName || f == fName
		}
		if hasMitgl && hasName {
			hdr, colField = i, m
			break
		}
	}
	if hdr < 0 {
		return nil, nil, errors.New("Keine Kopfzeile gefunden. Es werden mindestens die Spalten »Mitgliedsnr.« und »Name« erwartet")
	}
	present := map[field]bool{}
	felder := []string{}
	names := map[field]string{fGarten: "Gartennr.", fAnrede: "Anrede", fStrasse: "Straße", fPLZ: "PLZ Ort", fVersand: "Versandart",
		fGroesse: "Gartengröße", fUmlageAbw: "Umlage abweichend", fUmlage: "Umlage", fWZ: "Wasserzähler-Nr.", fSZ: "Stromzähler-Nr.",
		fWVJ: "Wasser Vorjahr", fWAkt: "Wasser aktuell", fSVJ: "Energie Vorjahr", fSAkt: "Energie aktuell", fStd: "Arbeitsstunden",
		fVers: "Versicherung", fGrund: "Grundsteuer", fAusl: "Auslagen", fHinweis: "Hinweis", fAbschlag: "Abschlag"}
	for _, f := range colField {
		present[f] = true
	}
	for f := fGarten; f <= fAbschlag; f++ {
		if present[f] && names[f] != "" {
			felder = append(felder, names[f])
		}
	}

	byNr := map[string]bool{}
	for _, p := range existing {
		byNr[strings.ToLower(strings.TrimSpace(p.Mitgliedsnr))] = true
	}
	seen := map[string]int{}
	var rows []ImportRow
	warn := []string{}
	for i := hdr + 1; i < len(table); i++ {
		cells := table[i]
		get := func(f field) string {
			for c, ff := range colField {
				if ff == f && c < len(cells) {
					return strings.TrimSpace(cells[c])
				}
			}
			return ""
		}
		nr := trim(get(fMitgl), 20)
		name := trim(get(fName), 100)
		if nr == "" && name == "" {
			continue
		}
		row := ImportRow{Zeile: i + 1, Felder: felder, Warnungen: []string{}}
		if nr == "" {
			warn = append(warn, fmt.Sprintf("Zeile %d: Mitgliedsnummer fehlt – übersprungen", i+1))
			continue
		}
		if name == "" {
			warn = append(warn, fmt.Sprintf("Zeile %d (%s): Name fehlt – übersprungen", i+1, nr))
			continue
		}
		key := strings.ToLower(nr)
		if prev, dup := seen[key]; dup {
			warn = append(warn, fmt.Sprintf("Zeile %d (%s): Mitgliedsnummer kommt schon in Zeile %d vor – übersprungen", i+1, nr, prev))
			continue
		}
		seen[key] = i + 1

		p := Paechter{Mitgliedsnr: nr, Name: name,
			Gartennr: trim(get(fGarten), 20), Anrede: trim(get(fAnrede), 30), Strasse: trim(get(fStrasse), 100),
			PLZOrt: trim(get(fPLZ), 100), Versand: trim(get(fVersand), 30),
			WasserzaehlerNr: trim(get(fWZ), 40), StromzaehlerNr: trim(get(fSZ), 40)}
		if g := get(fGroesse); g != "" {
			if v, ok := parseNumber(g, raw); ok && validNum(v) {
				p.Gartengroesse = v
			} else {
				row.Warnungen = append(row.Warnungen, "Gartengröße nicht lesbar")
			}
		}
		if u := get(fUmlageAbw); u != "" {
			if v, ok := parseNumber(u, raw); ok && validNum(v) {
				p.UmlageAbweichend = &v
			}
		} else if u := get(fUmlage); u != "" {
			if v, ok := parseNumber(u, raw); ok && validNum(v) && math.Abs(v-st.UmlageStandard) > 0.004 {
				p.UmlageAbweichend = &v
			}
		}
		if strings.HasPrefix(strings.ToLower(p.Versand), "e") {
			p.Versand = "Emailsendung"
		} else if strings.HasPrefix(strings.ToLower(p.Versand), "p") {
			p.Versand = "Postversand"
		}

		// Ablesungen (optional)
		var a Ablesung
		anyA := false
		num := func(f field, dst **float64) {
			if s := get(f); s != "" {
				if v, ok := parseNumber(s, raw); ok && validNum(v) {
					*dst = &v
					anyA = true
				} else {
					row.Warnungen = append(row.Warnungen, "Wert nicht lesbar: "+names[f])
				}
			}
		}
		num(fWVJ, &a.WasserVJ)
		num(fWAkt, &a.WasserAkt)
		num(fSVJ, &a.StromVJ)
		num(fSAkt, &a.StromAkt)
		num(fStd, &a.Stunden)
		money := func(f field, dst *float64) {
			if s := get(f); s != "" {
				if v, ok := parseNumber(s, raw); ok && validNum(v) {
					*dst = v
					anyA = true
				}
			}
		}
		money(fVers, &a.Versicherung)
		money(fGrund, &a.Grundsteuer)
		money(fAusl, &a.Auslagen)
		money(fAbschlag, &a.Abschlag)
		if h := get(fHinweis); h != "" {
			a.Hinweis = trim(h, 300)
			anyA = true
		}
		if anyA {
			row.Ablesung = &a
		}
		row.Paechter = p
		if byNr[key] {
			row.Aktion = "aktualisieren"
		} else {
			row.Aktion = "neu"
		}
		rows = append(rows, row)
		if len(rows) > 2000 {
			return nil, nil, errors.New("Mehr als 2000 Zeilen – bitte die Datei kürzen")
		}
	}
	if len(rows) == 0 {
		return nil, warn, errors.New("Keine gültigen Zeilen gefunden")
	}
	return rows, warn, nil
}

// parseCSV liest CSV/TXT-Dateien (Trennzeichen ; , oder Tab).
func parseCSV(data []byte) ([][]string, error) {
	text := decodeText(data)
	first := text
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		first = text[:i]
	}
	delim := ';'
	best := strings.Count(first, ";")
	if c := strings.Count(first, "\t"); c > best {
		delim, best = '\t', c
	}
	if c := strings.Count(first, ","); c > best {
		delim = ','
	}
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	return r.ReadAll()
}

// ---------------------------------------------------------------- Export

func exportOverview(v yearView) ([]byte, error) {
	head := []string{"Mitgliedsnr.", "Gartennr.", "Anrede", "Name", "Straße", "PLZ Ort", "Versandart", "Gartengröße (m²)",
		"Wasserzähler-Nr.", "Wasser Stand Vorjahr", "Wasser Stand aktuell", "Stromzähler-Nr.", "Energie Stand Vorjahr", "Energie Stand aktuell",
		"Erbrachte Arbeitsstunden", "Versicherung (€)", "Grundsteuer (€)", "Sonstige Auslagen (€)", "Hinweis / Erläuterung", "Abschlagszahlung (€)",
		"Umlage abweichend (€)",
		"Wasserverbrauch (m³)", "Kosten Wasser (€)", "Energieverbrauch (kWh)", "Kosten Energie (€)", "Nachzahlung fehlende Stunden (€)",
		"Zwischensumme 1 (€)", "Pacht Garten (€)", "Pacht Vereinsfläche (€)", "Pacht freie Gärten (€)", "Mitgliedsbeitrag (€)", "Umlage (€)",
		"Zwischensumme 2 (€)", "Vergütung Arbeitsstunden (€)", "Gesamtbetrag (€)", "Prüfung"}
	widths := []float64{12, 9, 9, 22, 22, 16, 14, 11, 13, 11, 11, 13, 11, 11, 11, 11, 11, 11, 24, 12, 12, 11, 11, 11, 11, 13, 12, 11, 11, 11, 11, 10, 12, 12, 13, 30}
	hdrRow := make([]xCell, len(head))
	for i, h := range head {
		hdrRow[i] = xCell{h, stHeader}
	}
	rows := [][]xCell{hdrRow}
	np := func(p *float64) any {
		if p == nil {
			return nil
		}
		return *p
	}
	for _, p := range v.Paechter {
		a := v.Ablesungen[p.ID]
		r := calculate(v.Settings, p, a)
		var umlAbw any
		if p.UmlageAbweichend != nil {
			umlAbw = *p.UmlageAbweichend
		}
		row := []xCell{
			{p.Mitgliedsnr, stNormal}, {p.Gartennr, stNormal}, {p.Anrede, stNormal}, {p.Name, stNormal}, {p.Strasse, stNormal},
			{p.PLZOrt, stNormal}, {p.Versand, stNormal}, {p.Gartengroesse, stNum},
			{p.WasserzaehlerNr, stNormal}, {np(a.WasserVJ), stNum}, {np(a.WasserAkt), stNum},
			{p.StromzaehlerNr, stNormal}, {np(a.StromVJ), stNum}, {np(a.StromAkt), stNum},
			{np(a.Stunden), stNum}, {a.Versicherung, stEUR}, {a.Grundsteuer, stEUR}, {a.Auslagen, stEUR},
			{a.Hinweis, stNormal}, {a.Abschlag, stEUR}, {umlAbw, stEUR},
			{np(r.WasserVerbrauch), stNum}, {r.KostenWasser, stEUR}, {np(r.EnergieVerbrauch), stNum}, {r.KostenEnergie, stEUR},
			{r.NachzahlungStunden, stEUR}, {r.Zwischensumme1, stEUR}, {r.PachtGarten, stEUR}, {r.PachtVerein, stEUR},
			{r.PachtFrei, stEUR}, {r.Mitgliedsbeitrag, stEUR}, {r.Umlage, stEUR}, {r.Zwischensumme2, stEUR},
			{r.Verguetung, stEUR}, {r.Gesamt, stEURb}, {r.Status, stNormal},
		}
		if !r.Vollstaendig {
			row[34] = xCell{nil, stEURb}
		}
		rows = append(rows, row)
	}
	name := fmt.Sprintf("Jahresübersicht %d", v.Jahr)
	return writeXLSX(name, widths, rows)
}
