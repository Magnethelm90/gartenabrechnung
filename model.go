package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Settings enthält alle Preise und Regeln eines Abrechnungsjahres.
type Settings struct {
	VereinName         string `json:"vereinName"`
	Ort                string `json:"ort"`
	Absenderzeile      string `json:"absenderzeile"`
	Jahr               int    `json:"jahr"`
	Rechnungsdatum     string `json:"rechnungsdatum"` // JJJJ-MM-TT
	Zahlungsziel       string `json:"zahlungsziel"`   // JJJJ-MM-TT
	RechnungsnrPraefix string `json:"rechnungsnrPraefix"`
	EinspruchTage      int    `json:"einspruchTage"`
	BankName           string `json:"bankName"`
	IBAN               string `json:"iban"`
	BIC                string `json:"bic"`

	WasserGrundpreis  float64 `json:"wasserGrundpreis"`
	WasserPreis       float64 `json:"wasserPreis"`
	EnergieGrundpreis float64 `json:"energieGrundpreis"`
	EnergiePreis      float64 `json:"energiePreis"`

	Pflichtstunden    float64 `json:"pflichtstunden"`
	StundenObergrenze float64 `json:"stundenObergrenze"`
	VerguetungJeStd   float64 `json:"verguetungJeStd"`
	NachzahlungJeStd  float64 `json:"nachzahlungJeStd"`

	PachtJeQm          float64 `json:"pachtJeQm"`
	VereinsflaecheQm   float64 `json:"vereinsflaecheQm"`
	FreieGaertenQm     float64 `json:"freieGaertenQm"`
	Vereinsbeitrag     float64 `json:"vereinsbeitrag"`
	Territorialverband float64 `json:"territorialverband"`
	UmlageStandard     float64 `json:"umlageStandard"`
}

// Paechter sind die Stammdaten eines Pächters (ändern sich selten).
type Paechter struct {
	ID               string   `json:"id"`
	Mitgliedsnr      string   `json:"mitgliedsnr"`
	Gartennr         string   `json:"gartennr"`
	Anrede           string   `json:"anrede"`
	Name             string   `json:"name"`
	Strasse          string   `json:"strasse"`
	PLZOrt           string   `json:"plzOrt"`
	Versand          string   `json:"versand"`
	Gartengroesse    float64  `json:"gartengroesse"`
	UmlageAbweichend *float64 `json:"umlageAbweichend"`
	WasserzaehlerNr  string   `json:"wasserzaehlerNr"`
	StromzaehlerNr   string   `json:"stromzaehlerNr"`
}

// Ablesung sind die jährlichen Eingaben pro Pächter.
type Ablesung struct {
	WasserVJ     *float64 `json:"wasserVJ"`
	WasserAkt    *float64 `json:"wasserAkt"`
	StromVJ      *float64 `json:"stromVJ"`
	StromAkt     *float64 `json:"stromAkt"`
	Stunden      *float64 `json:"stunden"`
	Versicherung float64  `json:"versicherung"`
	Grundsteuer  float64  `json:"grundsteuer"`
	Auslagen     float64  `json:"auslagen"`
	Abschlag     float64  `json:"abschlag"`
	Hinweis      string   `json:"hinweis"`
}

// Jahr fasst die Daten eines Abrechnungsjahres zusammen. Abgeschlossene Jahre
// enthalten eine Kopie der Einstellungen und Pächter, damit alte Rechnungen
// später unverändert neu gedruckt werden können.
type Jahr struct {
	Abgeschlossen bool                `json:"abgeschlossen"`
	Settings      *Settings           `json:"settings,omitempty"`
	Paechter      []Paechter          `json:"paechter,omitempty"`
	Ablesungen    map[string]Ablesung `json:"ablesungen"`
}

// AdminAuth speichert das Admin-Passwort nur als Hash.
type AdminAuth struct {
	Salt string `json:"salt,omitempty"`
	Hash string `json:"hash,omitempty"`
	Iter int    `json:"iter,omitempty"`
}

// Data ist der komplette Datenbestand (eine JSON-Datei).
type Data struct {
	Version  int              `json:"version"`
	Settings Settings         `json:"settings"`
	Paechter []Paechter       `json:"paechter"`
	Jahre    map[string]*Jahr `json:"jahre"`
	Admin    AdminAuth        `json:"admin"`
	// Rechnungen ist das Archiv aller ausgestellten Rechnungen. Jeder Eintrag
	// enthält eine vollständige Kopie der damaligen Werte.
	Rechnungen []*Rechnung `json:"rechnungen"`
}

func defaultSettings() Settings {
	return Settings{
		VereinName:         "Kleingartenverein Mustername e. V.",
		Ort:                "12345 Musterstadt",
		Absenderzeile:      "Kleingartenverein Mustername e.V. – Vorname Nachname – Kassenwart – kasse@example.org",
		Jahr:               2025,
		Rechnungsdatum:     "2026-01-10",
		Zahlungsziel:       "2026-02-15",
		RechnungsnrPraefix: "100",
		EinspruchTage:      10,
		BankName:           "Musterbank",
		IBAN:               "DE00 0000 0000 0000 0000 00",
		BIC:                "MUSTDEXXXXX",
		WasserGrundpreis:   1.70,
		WasserPreis:        2.04,
		EnergieGrundpreis:  3.20,
		EnergiePreis:       0.42,
		Pflichtstunden:     12,
		StundenObergrenze:  24,
		VerguetungJeStd:    5,
		NachzahlungJeStd:   10,
		PachtJeQm:          0.07,
		VereinsflaecheQm:   55.4,
		FreieGaertenQm:     223,
		Vereinsbeitrag:     51,
		Territorialverband: 21,
		UmlageStandard:     35,
	}
}

func newData() *Data {
	s := defaultSettings()
	return &Data{
		Version:  1,
		Settings: s,
		Paechter: []Paechter{},
		Jahre:    map[string]*Jahr{yearKey(s.Jahr): {Ablesungen: map[string]Ablesung{}}},
	}
}

func yearKey(y int) string { return fmt.Sprintf("%d", y) }

// ---- Hilfsfunktionen ----

// round2 rundet kaufmännisch auf 2 Nachkommastellen (wie ROUND in Excel).
func round2(x float64) float64 {
	if x >= 0 {
		return math.Round(x*100+1e-9) / 100
	}
	return math.Round(x*100-1e-9) / 100
}

func validNum(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0) && x >= 0 && x < 1e9
}

func validNumPtr(p *float64) bool { return p == nil || validNum(*p) }

func trim(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > max {
		r = r[:max]
	}
	return string(r)
}

func parseDate(s string) (time.Time, error) { return time.Parse("2006-01-02", s) }

func germanDate(s string) string {
	t, err := parseDate(s)
	if err != nil {
		return s
	}
	return t.Format("02.01.2006")
}

// fmtNum formatiert Zahlen deutsch (Komma als Dezimaltrenner, Punkt als Tausender).
func fmtNum(x float64, decimals int) string {
	neg := x < 0
	if neg {
		x = -x
	}
	s := fmt.Sprintf("%.*f", decimals, x)
	intPart, frac := s, ""
	if i := strings.Index(s, "."); i >= 0 {
		intPart, frac = s[:i], s[i+1:]
	}
	var b strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	out := b.String()
	if decimals > 0 {
		out += "," + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

func fmtEUR(x float64) string { return fmtNum(x, 2) + " €" }

// fmtFlex zeigt Zahlen mit bis zu 2 Nachkommastellen ohne unnötige Nullen.
func fmtFlex(x float64) string {
	s := fmtNum(x, 2)
	if strings.Contains(s, ",") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ",")
	}
	return s
}
