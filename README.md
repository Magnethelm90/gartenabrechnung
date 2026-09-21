# Gartenabrechnung

Kleines Windows-Programm für Kleingartenvereine: Pächter einmal anlegen, jedes Jahr nur noch die Zählerstände
eintragen, daraus werden die Rechnungen als PDF erzeugt. Die Zahlungen lassen sich abhaken, offene Posten
werden aufgelistet.

Das Programm ist eine einzelne `.exe` ohne Installation. Es startet einen kleinen Webserver, der **nur auf dem
eigenen Rechner** (`127.0.0.1`) lauscht, und öffnet die Oberfläche im Browser. Es werden keine Daten ins
Internet gesendet, es gibt keine Konten, Schlüssel oder Cloud-Anbindung.

## Funktionen

- **Admin-Bereich:** Pächter anlegen, ändern, löschen (Nummer, Name, Anschrift, Gartengröße, Umlage, Zählernummern),
  Preise und Regeln einstellen (Wasser, Strom, Pacht, Arbeitsstunden, Bankverbindung, Rechnungsdatum), optionales Passwort
- **Schnelleingabe:** Nummer eintippen, Stammdaten und Vorjahresstände erscheinen automatisch, nur neue Zählerstände eingeben
- **Rechnungen:** PDF pro Pächter oder alle auf einmal, ausgestellte Rechnungen werden mit den damaligen Werten
  festgeschrieben und archiviert (spätere Änderungen verändern sie nicht)
- **Zahlungen:** bezahlt abhaken (mit Datum), Teilzahlungen, Liste der offenen Posten, Excel-Export
- **Jahreswechsel**, Import der Mitglieder aus Excel/CSV, Excel-Export, automatische Sicherungen

## Abrechnungsregeln (Beispielwerte, im Admin-Bereich änderbar)

- Wasser und Energie: Grundpreis + Verbrauch × Preis
- Arbeitsstunden: unter den Pflichtstunden wird nachberechnet, darüber bis zu einer Obergrenze vergütet
- Pacht: Gartenfläche × Preis je m², zuzüglich Anteile für Vereinsfläche und freie Gärten
- Gesamtbetrag = Kosten + Pacht/Beiträge/Umlage − Vergütung − Abschlag (negativ = Guthaben)

Alle Namen, Adressen und die Bankverbindung in den Voreinstellungen sind Platzhalter.

## Starten

`Gartenabrechnung.exe` in einen eigenen Ordner legen und doppelklicken. Das Konsolenfenster offen lassen.
Die Daten liegen im selben Ordner:

| Datei / Ordner | Inhalt |
| --- | --- |
| `gartenabrechnung-daten.json` | alle Daten (Pächter, Zählerstände, Rechnungsarchiv, Zahlungen) |
| `Rechnungen/<Jahr>/` | fertige PDF-Rechnungen |
| `Sicherungen/` | automatische Tages- und Ereignissicherungen |

Optionen: `--data <Ordner>`, `--port <Zahl>`, `--no-browser`, `--reset-admin` (Admin-Passwort entfernen).

## Selbst bauen

Benötigt [Go](https://go.dev/dl/) 1.24 oder neuer.

```
go test ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o Gartenabrechnung.exe .
```

Unter Windows (PowerShell):

```
go test ./...
go build -trimpath -ldflags "-s -w" -o Gartenabrechnung.exe .
```

## Sicherheit

- Der Server lauscht nur auf `127.0.0.1`. Schutz gegen fremde Webseiten: Host-/Origin-Prüfung, Pflicht-Header bei
  Änderungen, Content-Security-Policy ohne Inline-Skripte
- Admin-Passwort optional (mindestens 8 Zeichen), gespeichert als PBKDF2-SHA256-Hash (600.000 Runden, Salt).
  Nach 5 Fehlversuchen wird gesperrt, die Sperrzeit verdoppelt sich mit jeder Serie (30 Sekunden bis 15 Minuten)
- Die Oberfläche fügt Daten nie als HTML ein (kein `innerHTML`), Namen o. Ä. können keinen Code ausführen
- Keine Verbindung ins Internet, keine Telemetrie, keine Schlüssel oder Zugangsdaten im Code
- Die Datendatei enthält personenbezogene Daten und gehört nicht in ein Repository (`.gitignore` schließt sie aus)

**Grenzen:** Wer sich am selben Rechner anmelden kann, erreicht auch die Programmoberfläche und die Datendatei.
Nur der Admin-Bereich (Pächter, Preise, Import, Jahreswechsel) ist passwortgeschützt, die Eingabe der Zählerstände
und die Zahlungsübersicht sind es nicht. Das Programm gehört auf einen Rechner, zu dem nur berechtigte Personen Zugang haben.
Die Datendatei ist nicht verschlüsselt, dafür sind Festplattenverschlüsselung (z. B. BitLocker) und ein Windows-Kennwort zuständig.
Sicherheitslücken bitte nicht öffentlich melden, sondern direkt an den Autor.

## Verwendete Bestandteile

- [go-pdf/fpdf](https://github.com/go-pdf/fpdf) (MIT) für die PDF-Erzeugung
- Liberation Sans (SIL Open Font License, siehe `fonts/LICENSE-Liberation.txt`) als eingebettete Schrift

Copyright © Derek
