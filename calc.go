package main

// Result enthält alle berechneten Werte einer Rechnung.
type Result struct {
	WasserVerbrauch    *float64 `json:"wasserVerbrauch"`
	EnergieVerbrauch   *float64 `json:"energieVerbrauch"`
	KostenWasser       float64  `json:"kostenWasser"`
	KostenEnergie      float64  `json:"kostenEnergie"`
	NachzahlungStunden float64  `json:"nachzahlungStunden"`
	Zwischensumme1     float64  `json:"zwischensumme1"`
	PachtGarten        float64  `json:"pachtGarten"`
	PachtVerein        float64  `json:"pachtVerein"`
	PachtFrei          float64  `json:"pachtFrei"`
	Mitgliedsbeitrag   float64  `json:"mitgliedsbeitrag"`
	Umlage             float64  `json:"umlage"`
	Zwischensumme2     float64  `json:"zwischensumme2"`
	Verguetung         float64  `json:"verguetung"`
	Gesamt             float64  `json:"gesamt"`
	Vollstaendig       bool     `json:"vollstaendig"`
	Status             string   `json:"status"`
}

const (
	StatusOK          = "OK"
	StatusFehlt       = "Angaben fehlen"
	StatusZaehler     = "Neuer Stand kleiner als alter Stand"
	StatusGroesse     = "Gartengröße fehlt"
	StatusUnvollstNum = "Zählerstände oder Stunden fehlen"
)

// calculate berechnet eine Rechnung. Die Regeln entsprechen der Excel-Vorlage
// und der bisherigen Word-Rechnung des Vereins.
func calculate(s Settings, p Paechter, a Ablesung) Result {
	var r Result

	// Wasser
	if a.WasserVJ != nil && a.WasserAkt != nil {
		v := *a.WasserAkt - *a.WasserVJ
		r.WasserVerbrauch = &v
		r.KostenWasser = round2(s.WasserGrundpreis + v*s.WasserPreis)
	}
	// Energie
	if a.StromVJ != nil && a.StromAkt != nil {
		v := *a.StromAkt - *a.StromVJ
		r.EnergieVerbrauch = &v
		r.KostenEnergie = round2(s.EnergieGrundpreis + v*s.EnergiePreis)
	}
	// Arbeitsstunden
	if a.Stunden != nil {
		h := *a.Stunden
		if fehl := s.Pflichtstunden - h; fehl > 0 {
			r.NachzahlungStunden = round2(fehl * s.NachzahlungJeStd)
		}
		obergrenze := h
		if s.StundenObergrenze < obergrenze {
			obergrenze = s.StundenObergrenze
		}
		if extra := obergrenze - s.Pflichtstunden; extra > 0 {
			r.Verguetung = round2(extra * s.VerguetungJeStd)
		}
	}
	r.Zwischensumme1 = round2(r.KostenWasser + r.KostenEnergie + r.NachzahlungStunden)

	// Beiträge für das Folgejahr
	r.PachtGarten = round2(p.Gartengroesse * s.PachtJeQm)
	r.PachtVerein = round2(s.VereinsflaecheQm * s.PachtJeQm)
	r.PachtFrei = round2(s.FreieGaertenQm * s.PachtJeQm)
	r.Mitgliedsbeitrag = round2(s.Vereinsbeitrag + s.Territorialverband)
	r.Umlage = s.UmlageStandard
	if p.UmlageAbweichend != nil {
		r.Umlage = *p.UmlageAbweichend
	}
	r.Zwischensumme2 = round2(r.PachtGarten + r.PachtVerein + r.PachtFrei + r.Mitgliedsbeitrag +
		r.Umlage + a.Versicherung + a.Grundsteuer + a.Auslagen)

	r.Gesamt = round2(r.Zwischensumme1 + r.Zwischensumme2 - r.Verguetung - a.Abschlag)

	// Vollständigkeit prüfen
	switch {
	case a.WasserVJ == nil || a.WasserAkt == nil || a.StromVJ == nil || a.StromAkt == nil || a.Stunden == nil:
		r.Status = StatusFehlt
	case *a.WasserAkt < *a.WasserVJ || *a.StromAkt < *a.StromVJ:
		r.Status = StatusZaehler
	case p.Gartengroesse <= 0:
		r.Status = StatusGroesse
	default:
		r.Status = StatusOK
		r.Vollstaendig = true
	}
	return r
}
