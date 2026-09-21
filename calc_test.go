package main

import (
	"math"
	"testing"
)

func fp(x float64) *float64 { return &x }

func near(a, b float64) bool { return math.Abs(a-b) < 0.0001 }

// Die Werte stammen aus der echten Rechnung 2025 (Gartennr. 35, 300 m²).
func TestBeispielrechnung2025(t *testing.T) {
	s := defaultSettings()
	s.Pflichtstunden = 14 // 2025 galten noch 14 Pflichtstunden
	p := Paechter{Gartengroesse: 300}
	a := Ablesung{WasserVJ: fp(148), WasserAkt: fp(191), StromVJ: fp(2136), StromAkt: fp(2302), Stunden: fp(20), Abschlag: 150}
	r := calculate(s, p, a)

	checks := map[string][2]float64{
		"Wasser":         {r.KostenWasser, 89.42},
		"Energie":        {r.KostenEnergie, 72.92},
		"Nachzahlung":    {r.NachzahlungStunden, 0},
		"Zwischensumme1": {r.Zwischensumme1, 162.34},
		"Pacht Garten":   {r.PachtGarten, 21.00},
		"Pacht Verein":   {r.PachtVerein, 3.88},
		"Pacht frei":     {r.PachtFrei, 15.61},
		"Mitgliedsbeitr": {r.Mitgliedsbeitrag, 72.00},
		"Umlage":         {r.Umlage, 35.00},
		"Zwischensumme2": {r.Zwischensumme2, 147.49},
		"Verguetung":     {r.Verguetung, 30.00},
		"Gesamt":         {r.Gesamt, 129.83},
	}
	for name, c := range checks {
		if !near(c[0], c[1]) {
			t.Errorf("%s: %.4f, erwartet %.2f", name, c[0], c[1])
		}
	}
	if !r.Vollstaendig || r.Status != StatusOK {
		t.Errorf("Status %q, erwartet OK", r.Status)
	}
}

func TestArbeitsstundenRegel(t *testing.T) {
	s := defaultSettings() // 12 Pflicht, bis 24 vergütet, 5 €/h, 10 €/h Nachzahlung
	p := Paechter{Gartengroesse: 100}
	base := Ablesung{WasserVJ: fp(0), WasserAkt: fp(0), StromVJ: fp(0), StromAkt: fp(0)}
	cases := []struct{ h, nachzahlung, verguetung float64 }{
		{0, 120, 0},
		{8, 40, 0},
		{11.5, 5, 0},
		{12, 0, 0},
		{20, 0, 40},
		{24, 0, 60},
		{30, 0, 60}, // über 24 h gibt es nichts mehr dazu
	}
	for _, c := range cases {
		a := base
		a.Stunden = fp(c.h)
		r := calculate(s, p, a)
		if !near(r.NachzahlungStunden, c.nachzahlung) || !near(r.Verguetung, c.verguetung) {
			t.Errorf("%.1f h: Nachzahlung %.2f (erw. %.2f), Vergütung %.2f (erw. %.2f)",
				c.h, r.NachzahlungStunden, c.nachzahlung, r.Verguetung, c.verguetung)
		}
	}
}

func TestStatusUndGuthaben(t *testing.T) {
	s := defaultSettings()
	p := Paechter{Gartengroesse: 100}
	if r := calculate(s, p, Ablesung{}); r.Vollstaendig || r.Status != StatusFehlt {
		t.Errorf("leere Ablesung: %q", r.Status)
	}
	a := Ablesung{WasserVJ: fp(100), WasserAkt: fp(90), StromVJ: fp(0), StromAkt: fp(1), Stunden: fp(12)}
	if r := calculate(s, p, a); r.Status != StatusZaehler {
		t.Errorf("Zähler rückwärts: %q", r.Status)
	}
	a = Ablesung{WasserVJ: fp(0), WasserAkt: fp(1), StromVJ: fp(0), StromAkt: fp(1), Stunden: fp(12), Abschlag: 1000}
	if r := calculate(s, p, a); r.Gesamt >= 0 {
		t.Errorf("Guthaben erwartet, Gesamt %.2f", r.Gesamt)
	}
	// abweichende Umlage
	p.UmlageAbweichend = fp(50)
	if r := calculate(s, p, a); !near(r.Umlage, 50) {
		t.Errorf("abweichende Umlage %.2f", r.Umlage)
	}
}

func TestZahlenformat(t *testing.T) {
	cases := map[float64]string{0: "0,00 €", 129.83: "129,83 €", 1234.5: "1.234,50 €", -30: "-30,00 €", 1234567.891: "1.234.567,89 €"}
	for in, want := range cases {
		if got := fmtEUR(in); got != want {
			t.Errorf("fmtEUR(%v) = %q, erwartet %q", in, got, want)
		}
	}
	if got := fmtFlex(55.4); got != "55,4" {
		t.Errorf("fmtFlex(55.4) = %q", got)
	}
	if got := fmtFlex(300); got != "300" {
		t.Errorf("fmtFlex(300) = %q", got)
	}
}
