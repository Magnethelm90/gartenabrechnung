package main

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/go-pdf/fpdf"
)

// Eingebettete Schrift (Liberation Sans, SIL Open Font License, siehe fonts/LICENSE-Liberation.txt).
// Sie ist Arial im Aussehen sehr ähnlich, deckt auch Sonderzeichen (ř, ł, ő …) ab und sieht auf jedem PC gleich aus.
//
//go:embed fonts/LiberationSans-Regular.ttf
var fontRegular []byte

//go:embed fonts/LiberationSans-Bold.ttf
var fontBold []byte

// Spaltenraster (mm), entspricht dem Raster der Excel-Rechnung.
var (
	colX = [7]float64{20, 58.8, 87.1, 105, 130.4, 148.3, 169.2}
	colW = [7]float64{38.8, 28.3, 17.9, 25.4, 17.9, 20.9, 20.8}
)

const (
	leftX  = 20.0
	rightX = 190.0
	rowH   = 5.1
)

// invoiceNumber bildet die Rechnungsnummer, z. B. 100-35-95.
func invoiceNumber(s Settings, p Paechter) string {
	return strings.TrimSpace(s.RechnungsnrPraefix) + "-" + strings.TrimSpace(p.Mitgliedsnr)
}

func buildInvoice(s Settings, p Paechter, a Ablesung, r Result) ([]byte, error) {
	if !r.Vollstaendig {
		return nil, errors.New("Rechnung unvollständig: " + r.Status)
	}
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes("lib", "", fontRegular)
	pdf.AddUTF8FontFromBytes("lib", "B", fontBold)
	tr := func(s string) string { return s }
	pdf.SetMargins(leftX, 12, 20)
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetTitle(tr("Rechnung "+invoiceNumber(s, p)), true)
	pdf.SetAuthor(tr(s.VereinName), true)
	pdf.SetCreator("Gartenabrechnung", true)
	pdf.AddPage()

	y := 0.0
	font := func(style string, size float64) { pdf.SetFont("lib", style, size) }
	text := func(x, w float64, txt, align string) {
		pdf.SetXY(x, y)
		pdf.CellFormat(w, rowH, tr(txt), "", 0, align, false, 0, "")
	}
	// Zelle im Spaltenraster: von Spalte a bis einschließlich Spalte b
	span := func(a, b int, txt, align string) {
		text(colX[a], colX[b]+colW[b]-colX[a], txt, align)
	}
	hline := func(at float64, width float64) {
		pdf.SetLineWidth(width)
		pdf.SetDrawColor(90, 90, 90)
		pdf.Line(leftX, at, rightX, at)
	}
	next := func(h float64) { y += h }

	// Kopf
	pdf.SetLineWidth(0.7)
	pdf.SetDrawColor(0, 0, 0)
	pdf.Line(leftX, 14, rightX, 14)
	y = 17
	font("B", 14)
	pdf.SetXY(leftX, y)
	pdf.CellFormat(rightX-leftX, 8, tr(s.VereinName+" "+s.Ort), "", 0, "C", false, 0, "")
	hline(27, 0.25)

	y = 31
	font("", 7.5)
	pdf.SetTextColor(60, 60, 60)
	text(leftX, rightX-leftX, s.Absenderzeile, "L")
	pdf.SetTextColor(0, 0, 0)
	next(6)
	font("B", 10)
	text(leftX, rightX-leftX, p.Versand, "R")

	// Anschrift
	next(5)
	font("", 10)
	text(leftX, 90, p.Anrede, "L")
	next(rowH)
	font("B", 10)
	text(leftX, 90, p.Name, "L")
	next(rowH)
	font("", 10)
	text(leftX, 90, p.Strasse, "L")
	next(rowH)
	text(leftX, 90, p.PLZOrt, "L")

	// Rechnungsdaten rechts
	next(rowH + 4)
	meta := [][2]string{
		{"Mitgliedsnr.:", p.Mitgliedsnr},
		{"Rechnungsdatum:", germanDate(s.Rechnungsdatum)},
		{"Rechnungsnr.:", invoiceNumber(s, p)},
		{"Gartennr.:", p.Gartennr},
	}
	for _, m := range meta {
		font("", 10)
		pdf.SetXY(115, y)
		pdf.CellFormat(50, rowH, tr(m[0]), "", 0, "R", false, 0, "")
		font("B", 10)
		pdf.SetXY(165, y)
		pdf.CellFormat(25, rowH, tr(m[1]), "", 0, "R", false, 0, "")
		next(rowH)
	}

	// Überschriften
	next(5)
	font("B", 12)
	pdf.SetXY(leftX, y)
	pdf.CellFormat(rightX-leftX, 6, tr(fmt.Sprintf("Rechnung Jahresendabrechnung %d", s.Jahr)), "", 0, "L", false, 0, "")
	next(7)
	font("BU", 10)
	text(leftX, rightX-leftX, "1. Jahresverbrauch Wasser/Energie, geleistete Arbeitsstunden", "L")
	next(rowH + 1)

	// Verbrauchsblock (Wasser bzw. Energie)
	block := func(title, zaehlerNr string, vj, akt *float64, verbrauch *float64, unit, gpLabel, preisLabel string, gp, preis, summe float64, sumLabel string) {
		font("B", 10)
		text(colX[0], colW[0], title, "L")
		font("", 10)
		span(1, 1, "ZSt VJ:", "R")
		span(2, 2, fmtFlex(*vj), "R")
		span(3, 3, "ZSt Akt.:", "R")
		span(4, 4, fmtFlex(*akt), "R")
		span(5, 5, "Ges.-Verbr.:", "R")
		font("B", 10)
		span(6, 6, fmtFlex(*verbrauch)+" "+unit, "R")
		next(rowH)
		if strings.TrimSpace(zaehlerNr) != "" {
			font("", 8)
			pdf.SetTextColor(70, 70, 70)
			text(colX[0], colW[0], "Zähler-Nr.: "+zaehlerNr, "L")
			pdf.SetTextColor(0, 0, 0)
		}
		font("", 10)
		span(1, 1, gpLabel, "R")
		span(2, 2, fmtEUR(gp), "R")
		span(3, 3, preisLabel, "R")
		span(4, 4, fmtEUR(preis), "R")
		span(5, 5, sumLabel, "R")
		font("B", 10)
		span(6, 6, fmtEUR(summe), "R")
		next(rowH + 1.2)
		hline(y-0.6, 0.2)
		next(1.4)
	}
	block("1.1. Wasserverbrauch:", p.WasserzaehlerNr, a.WasserVJ, a.WasserAkt, r.WasserVerbrauch, "m³",
		"Grundpreis:", "Preis je m³:", s.WasserGrundpreis, s.WasserPreis, r.KostenWasser, "Summe:")
	block("1.2. Energieverbrauch:", p.StromzaehlerNr, a.StromVJ, a.StromAkt, r.EnergieVerbrauch, "kWh",
		"Grundpreis:", "Preis je kWh:", s.EnergieGrundpreis, s.EnergiePreis, r.KostenEnergie, "Summe:")

	// Arbeitsstunden
	font("", 10)
	pdf.SetXY(leftX, y)
	pdf.MultiCell(rightX-leftX, 4.8, tr(fmt.Sprintf(
		"1.3. Arbeitsstunden: laut der Beitrags- und Gebührenordnung sind %s Pflichtstunden zu erbringen. "+
			"Bis zum Ende der Saison erbrachte Arbeitsstunden: %s h.", fmtFlex(s.Pflichtstunden), fmtFlex(*a.Stunden))), "", "L", false)
	y = pdf.GetY() + 1
	text(colX[0], 80, "Beitrag für nicht erbrachte Pflichtstunden: "+fmtEUR(r.NachzahlungStunden), "L")
	span(4, 5, "Zwischensumme:", "R")
	font("B", 10)
	span(6, 6, fmtEUR(r.Zwischensumme1), "R")
	next(rowH + 1)
	hline(y, 0.2)
	next(2)

	// Beiträge
	font("BU", 10)
	text(leftX, rightX-leftX, "2. Beitragszahlung für das Folgejahr", "L")
	next(rowH + 0.6)
	pacht := func(label string, qm, betrag float64) {
		font("", 10)
		span(0, 0, label, "L")
		span(1, 2, fmtFlex(qm)+" m²", "R")
		span(3, 3, "Pachtbetrag:", "R")
		span(4, 4, fmtEUR(betrag), "R")
		next(rowH)
	}
	pacht("Gartengröße:", p.Gartengroesse, r.PachtGarten)
	pacht("Anteil Vereinsfläche:", s.VereinsflaecheQm, r.PachtVerein)
	pacht("Pachtanteil freie Gärten:", s.FreieGaertenQm, r.PachtFrei)
	next(0.6)
	hline(y, 0.2)
	next(1.4)

	font("", 10)
	span(0, 0, "Vereinsbeitrag:", "L")
	span(1, 1, fmtEUR(s.Vereinsbeitrag), "R")
	next(rowH)
	span(0, 0, "Territorialverband:", "L")
	span(1, 1, fmtEUR(s.Territorialverband), "R")
	span(3, 5, "Gesamt Mitgliedsbeitrag:", "R")
	span(6, 6, fmtEUR(r.Mitgliedsbeitrag), "R")
	next(rowH + 0.6)
	hline(y, 0.2)
	next(1.4)

	posten := func(label string, v float64) {
		font("", 10)
		span(0, 0, label, "L")
		if v != 0 {
			span(6, 6, fmtEUR(v), "R")
		}
		next(rowH)
	}
	posten("Versicherung:", a.Versicherung)
	posten("Umlage:", r.Umlage)
	posten("Grundsteuer:", a.Grundsteuer)
	posten("Sonstige Auslagen:", a.Auslagen)
	font("U", 8.5)
	text(leftX, 40, "Hinweis/Erläuterung:", "L")
	if h := strings.TrimSpace(a.Hinweis); h != "" {
		font("", 9)
		pdf.SetXY(colX[1], y)
		pdf.MultiCell(rightX-colX[1], 4.4, tr(h), "", "L", false)
		if pdf.GetY() > y {
			y = pdf.GetY()
		}
	}
	next(rowH + 1)
	font("", 10)
	span(4, 5, "Zwischensumme:", "R")
	font("B", 10)
	span(6, 6, fmtEUR(r.Zwischensumme2), "R")
	next(rowH + 0.8)
	hline(y, 0.2)
	next(1.6)

	// Abzüge und Gesamtbetrag
	font("", 10)
	span(0, 5, "Abzüglich Vergütung von Arbeitsstunden / Aufwandsentschädigung:", "L")
	if r.Verguetung != 0 {
		span(6, 6, fmtEUR(-r.Verguetung), "R")
	}
	next(rowH)
	span(0, 5, "Abzüglich Abschlagszahlung:", "L")
	if a.Abschlag != 0 {
		span(6, 6, fmtEUR(-a.Abschlag), "R")
	}
	next(rowH + 1)
	guthaben := r.Gesamt < 0
	label := "Gesamtbetrag:"
	if guthaben {
		label = "Guthaben (zu Ihren Gunsten):"
	}
	font("B", 11)
	pdf.SetXY(colX[3], y)
	pdf.CellFormat(colX[5]+colW[5]-colX[3], 6, tr(label), "", 0, "R", false, 0, "")
	pdf.SetXY(colX[6]-6, y)
	pdf.CellFormat(colW[6]+6, 6, tr(fmtEUR(absf(r.Gesamt))), "TB", 0, "R", false, 0, "")
	next(6 + 5)

	// Zahlungshinweis
	einspruch := fmt.Sprintf("Bei Unstimmigkeiten der Rechnung sind diese bis %d Tage nach Eingang beim Vorstand anzuzeigen. "+
		"Nach Ablauf dieser Frist gilt die Rechnung als angenommen.", s.EinspruchTage)
	var pay string
	if guthaben || r.Gesamt == 0 {
		pay = "Es ergibt sich ein Guthaben zu Ihren Gunsten. " + einspruch
	} else {
		pay = fmt.Sprintf("Der Gesamtbetrag ist bis zum %s auf das Konto der %s, IBAN: %s; BIC: %s, Verwendungszweck: %s (*), zu überweisen. %s "+
			"Bei Verspätung der Überweisung werden Mahngebühren bzw. Verzugszinsen angerechnet.",
			germanDate(s.Zahlungsziel), s.BankName, s.IBAN, s.BIC, invoiceNumber(s, p), einspruch)
	}
	font("", 10)
	pdf.SetXY(leftX, y)
	pdf.MultiCell(rightX-leftX, 4.7, tr(pay), "", "L", false)
	y = pdf.GetY() + 3
	if !guthaben && r.Gesamt != 0 {
		text(leftX, rightX-leftX, "(*) Bei Zahlungsvorgängen bitte angeben!", "L")
		next(rowH + 3)
	}
	text(leftX, rightX-leftX, "Der Vorstand", "L")
	next(rowH + 3)
	font("", 8)
	text(leftX, rightX-leftX, "Die Rechnung wird maschinell erstellt und ist ohne Unterschrift gültig.", "L")

	if y > 290 {
		return nil, errors.New("Rechnung passt nicht auf eine Seite (Hinweistext zu lang?)")
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
