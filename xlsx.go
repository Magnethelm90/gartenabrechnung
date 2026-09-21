package main

// Minimaler Excel-Leser und -Schreiber (nur Standardbibliothek).
// Reicht für Import der Mitgliederliste und Export der Übersicht.

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxXMLPart = 64 << 20 // Schutz vor Zip-Bomben: höchstens 64 MB je Teil

// ---------------------------------------------------------------- Schreiben

const (
	stNormal = 0
	stHeader = 1
	stEUR    = 2
	stNum    = 3
	stBold   = 4
	stEURb   = 5
)

type xCell struct {
	V     any // string, float64 oder nil
	Style int
}

func colName(i int) string { // 0 -> A
	s := ""
	for i >= 0 {
		s = string(rune('A'+i%26)) + s
		i = i/26 - 1
	}
	return s
}

func xmlText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\t' || r == '\n' || r == '\r' || (r >= 0x20 && r != 0xFFFE && r != 0xFFFF && !(r >= 0xD800 && r <= 0xDFFF)) {
			b.WriteRune(r)
		}
	}
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(b.String()))
	return out.String()
}

func writeXLSX(sheetName string, widths []float64, rows [][]xCell) ([]byte, error) {
	var sheet bytes.Buffer
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	lastCol := 0
	for _, r := range rows {
		if len(r) > lastCol {
			lastCol = len(r)
		}
	}
	sheet.WriteString(`<sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>`)
	sheet.WriteString(`<sheetFormatPr defaultRowHeight="15"/>`)
	if len(widths) > 0 {
		sheet.WriteString("<cols>")
		for i, w := range widths {
			fmt.Fprintf(&sheet, `<col min="%d" max="%d" width="%.1f" customWidth="1"/>`, i+1, i+1, w)
		}
		sheet.WriteString("</cols>")
	}
	sheet.WriteString("<sheetData>")
	for ri, r := range rows {
		fmt.Fprintf(&sheet, `<row r="%d">`, ri+1)
		for ci, c := range r {
			ref := colName(ci) + strconv.Itoa(ri+1)
			switch v := c.V.(type) {
			case string:
				fmt.Fprintf(&sheet, `<c r="%s" s="%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, c.Style, xmlText(v))
			case float64:
				fmt.Fprintf(&sheet, `<c r="%s" s="%d"><v>%s</v></c>`, ref, c.Style, strconv.FormatFloat(v, 'f', -1, 64))
			default:
				fmt.Fprintf(&sheet, `<c r="%s" s="%d"/>`, ref, c.Style)
			}
		}
		sheet.WriteString("</row>")
	}
	sheet.WriteString("</sheetData>")
	if lastCol > 0 && len(rows) > 0 {
		fmt.Fprintf(&sheet, `<autoFilter ref="A1:%s%d"/>`, colName(lastCol-1), len(rows))
	}
	sheet.WriteString(`<pageMargins left="0.5" right="0.5" top="0.6" bottom="0.6" header="0.3" footer="0.3"/>` +
		`<pageSetup paperSize="9" orientation="landscape" fitToHeight="0"/></worksheet>`)

	styles := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<numFmts count="2"><numFmt numFmtId="164" formatCode="#,##0.00\ &quot;€&quot;"/><numFmt numFmtId="165" formatCode="#,##0.##"/></numFmts>` +
		`<fonts count="3"><font><sz val="10"/><name val="Arial"/></font>` +
		`<font><b/><sz val="10"/><color rgb="FFFFFFFF"/><name val="Arial"/></font>` +
		`<font><b/><sz val="10"/><name val="Arial"/></font></fonts>` +
		`<fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill>` +
		`<fill><patternFill patternType="solid"><fgColor rgb="FF2E6B3A"/><bgColor indexed="64"/></patternFill></fill></fills>` +
		`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
		`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
		`<cellXfs count="6">` +
		`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
		`<xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0" applyFont="1" applyFill="1" applyAlignment="1"><alignment horizontal="center" vertical="center" wrapText="1"/></xf>` +
		`<xf numFmtId="164" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>` +
		`<xf numFmtId="165" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>` +
		`<xf numFmtId="0" fontId="2" fillId="0" borderId="0" xfId="0" applyFont="1"/>` +
		`<xf numFmtId="164" fontId="2" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyFont="1"/>` +
		`</cellXfs><cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles></styleSheet>`

	name := xmlText(sheetName)
	files := []struct{ name, body string }{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
			`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
			`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
			`<sheets><sheet name="` + name + `" sheetId="1" r:id="rId1"/></sheets>` +
			`<definedNames><definedName name="_xlnm._FilterDatabase" localSheetId="0" hidden="1">'` + name + `'!$A$1:$` +
			colName(maxInt(lastCol-1, 0)) + `$` + strconv.Itoa(maxInt(len(rows), 1)) + `</definedName></definedNames></workbook>`},
		{"xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>` +
			`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`},
		{"xl/styles.xml", styles},
		{"xl/worksheets/sheet1.xml", sheet.String()},
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range files {
		w, err := zw.Create(f.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.body)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---------------------------------------------------------------- Lesen

func readZipPart(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, name) {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			b, err := io.ReadAll(io.LimitReader(rc, maxXMLPart+1))
			if err != nil {
				return nil, err
			}
			if len(b) > maxXMLPart {
				return nil, errors.New("Datei ist zu groß")
			}
			return b, nil
		}
	}
	return nil, fmt.Errorf("Teil %q nicht gefunden", name)
}

// readXLSX liefert die Zellen des Blatts »preferSheet« (falls vorhanden, sonst
// des ersten Blatts) als Textzeilen. Zahlen kommen als Rohwert (Punkt als Dezimaltrenner).
func readXLSX(data []byte, preferSheet string) ([][]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("keine gültige Excel-Datei (.xlsx)")
	}

	// Blattnamen und Beziehungen
	wb, err := readZipPart(zr, "xl/workbook.xml")
	if err != nil {
		return nil, errors.New("keine gültige Excel-Datei (.xlsx)")
	}
	type sheetRef struct{ name, rid string }
	var sheets []sheetRef
	dec := xml.NewDecoder(bytes.NewReader(wb))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "sheet" {
			var sr sheetRef
			for _, a := range se.Attr {
				switch a.Name.Local {
				case "name":
					sr.name = a.Value
				case "id":
					sr.rid = a.Value
				}
			}
			sheets = append(sheets, sr)
		}
	}
	if len(sheets) == 0 {
		return nil, errors.New("die Excel-Datei enthält kein Tabellenblatt")
	}
	rels := map[string]string{}
	if rb, err := readZipPart(zr, "xl/_rels/workbook.xml.rels"); err == nil {
		d := xml.NewDecoder(bytes.NewReader(rb))
		for {
			tok, err := d.Token()
			if err != nil {
				break
			}
			if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "Relationship" {
				var id, target string
				for _, a := range se.Attr {
					switch a.Name.Local {
					case "Id":
						id = a.Value
					case "Target":
						target = a.Value
					}
				}
				rels[id] = target
			}
		}
	}
	chosen := sheets[0]
	for _, s := range sheets {
		if strings.EqualFold(s.name, preferSheet) {
			chosen = s
			break
		}
	}
	target := rels[chosen.rid]
	if target == "" {
		target = "worksheets/sheet1.xml"
	}
	if strings.HasPrefix(target, "/") {
		target = strings.TrimPrefix(target, "/")
	} else {
		target = path.Join("xl", target)
	}

	// gemeinsame Zeichenketten
	var shared []string
	if sb, err := readZipPart(zr, "xl/sharedStrings.xml"); err == nil {
		d := xml.NewDecoder(bytes.NewReader(sb))
		var cur strings.Builder
		inSI, inT, inPhonetic := false, false, 0
		for {
			tok, err := d.Token()
			if err != nil {
				break
			}
			switch t := tok.(type) {
			case xml.StartElement:
				switch t.Name.Local {
				case "si":
					inSI = true
					cur.Reset()
				case "rPh":
					inPhonetic++
				case "t":
					inT = inSI && inPhonetic == 0
				}
			case xml.EndElement:
				switch t.Name.Local {
				case "si":
					shared = append(shared, cur.String())
					inSI = false
				case "rPh":
					inPhonetic--
				case "t":
					inT = false
				}
			case xml.CharData:
				if inT {
					cur.Write(t)
				}
			}
		}
	}

	// Tabellenblatt
	sb, err := readZipPart(zr, target)
	if err != nil {
		return nil, errors.New("Tabellenblatt konnte nicht gelesen werden")
	}
	d := xml.NewDecoder(bytes.NewReader(sb))
	var rows [][]string
	var rowMap map[int]string
	var cellCol int
	var cellType string
	var inV, inIS bool
	var val strings.Builder
	maxCol := 0
	flush := func() {
		if rowMap == nil {
			return
		}
		r := make([]string, maxCol+1)
		for c, v := range rowMap {
			if c < len(r) {
				r[c] = v
			}
		}
		rows = append(rows, r)
		rowMap = nil
	}
	rowCount := 0
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				rowMap = map[int]string{}
				maxCol = 0
				rowCount++
				if rowCount > 20000 {
					return nil, errors.New("Tabelle hat zu viele Zeilen")
				}
			case "c":
				cellType = ""
				cellCol = len(rowMap)
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "r":
						cellCol = refCol(a.Value)
					case "t":
						cellType = a.Value
					}
				}
				val.Reset()
			case "v":
				inV = true
			case "is":
				inIS = true
			case "t":
				if inIS {
					inV = true
				}
			}
		case xml.CharData:
			if inV {
				val.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "t":
				if inIS {
					inV = false
				}
			case "is":
				inIS = false
			case "c":
				s := val.String()
				if cellType == "s" {
					if i, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && i >= 0 && i < len(shared) {
						s = shared[i]
					} else {
						s = ""
					}
				}
				if rowMap != nil && s != "" && cellCol >= 0 && cellCol < 500 {
					rowMap[cellCol] = s
					if cellCol > maxCol {
						maxCol = cellCol
					}
				}
			case "row":
				flush()
			}
		}
	}
	return rows, nil
}

// refCol wandelt »AB12« in den Spaltenindex (0-basiert).
func refCol(ref string) int {
	n := 0
	i := 0
	for ; i < len(ref); i++ {
		c := ref[i]
		if c >= 'A' && c <= 'Z' {
			n = n*26 + int(c-'A') + 1
		} else if c >= 'a' && c <= 'z' {
			n = n*26 + int(c-'a') + 1
		} else {
			break
		}
	}
	return n - 1
}

// decodeText wandelt eine CSV-Datei nach UTF-8 (UTF-8 mit/ohne BOM oder Windows-1252).
func decodeText(b []byte) string {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	if utf8.Valid(b) {
		return string(b)
	}
	cp := map[byte]rune{0x80: '€', 0x82: '‚', 0x83: 'ƒ', 0x84: '„', 0x85: '…', 0x86: '†', 0x87: '‡', 0x88: 'ˆ', 0x89: '‰',
		0x8A: 'Š', 0x8B: '‹', 0x8C: 'Œ', 0x8E: 'Ž', 0x91: '‘', 0x92: '’', 0x93: '“', 0x94: '”', 0x95: '•', 0x96: '–',
		0x97: '—', 0x98: '˜', 0x99: '™', 0x9A: 'š', 0x9B: '›', 0x9C: 'œ', 0x9E: 'ž', 0x9F: 'Ÿ'}
	var sb strings.Builder
	for _, c := range b {
		if r, ok := cp[c]; ok {
			sb.WriteRune(r)
		} else {
			sb.WriteRune(rune(c))
		}
	}
	return sb.String()
}
