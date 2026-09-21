'use strict';
/* Gartenabrechnung – Oberfläche. Alle Texte aus Daten werden nur über textContent/DOM-Knoten
   eingefügt (kein innerHTML), damit Namen o. Ä. nie als Code ausgeführt werden können. */

const S = { state: null, year: null, tab: 'eingabe', adminTab: 'paechter', filter: '', selInvoice: null, invTab: 'pruefen', invView: 'archiv', eingabeMode: 'schnell', quickId: null, quickQ: '', payFilter: 'offen', payQ: '', importPreview: null };

// ------------------------------------------------------------------ Hilfsfunktionen

function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  let value;
  for (const [k, v] of Object.entries(props || {})) {
    if (v === false || v == null) continue;
    if (k === 'class') el.className = v;
    else if (k === 'value') value = v;
    else if (k === 'checked') el.checked = !!v;
    else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : String(v));
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid == null || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  if (value !== undefined) el.value = value;
  return el;
}

const nf2 = new Intl.NumberFormat('de-DE', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const nfFlex = new Intl.NumberFormat('de-DE', { maximumFractionDigits: 2 });
const nfIn = new Intl.NumberFormat('de-DE', { maximumFractionDigits: 4, useGrouping: false });
const eur = (x) => nf2.format(x) + ' €';
const numIn = (v) => (v == null ? '' : nfIn.format(v));

// Zahlen deutsch oder englisch schreibbar: "2,5" "2.5" "1.234,5"; leer -> null; Unsinn -> NaN
function parseNum(s) {
  s = String(s ?? '').trim().replace(/[€\s ]/g, '');
  if (s === '') return null;
  if (s.includes(',')) s = s.replace(/\./g, '').replace(',', '.');
  else if ((s.match(/\./g) || []).length > 1) s = s.replace(/\./g, '');
  if (!/^-?\d*\.?\d+$|^-?\d+\.$/.test(s)) return NaN;
  return Number(s);
}

const cmpNr = (a, b) => a.mitgliedsnr.localeCompare(b.mitgliedsnr, 'de', { numeric: true });

async function api(method, url, body, isForm) {
  const opt = { method, headers: { 'X-GA-Request': '1' } };
  if (isForm) opt.body = body;
  else if (body !== undefined) {
    opt.headers['Content-Type'] = 'application/json';
    opt.body = JSON.stringify(body);
  }
  let r;
  try {
    r = await fetch(url, opt);
  } catch (e) {
    const err = new Error('Keine Verbindung zum Programm. Läuft das Fenster mit der Gartenabrechnung noch?');
    err.network = true;
    throw err;
  }
  const isJson = (r.headers.get('content-type') || '').includes('json');
  const data = isJson ? await r.json() : null;
  if (!r.ok) {
    const err = new Error((data && data.error) || `Fehler ${r.status}`);
    err.status = r.status;
    throw err;
  }
  return data;
}

function toast(msg, kind) {
  const box = document.getElementById('toasts');
  const t = h('div', { class: 'toast ' + (kind || '') }, msg);
  box.append(t);
  setTimeout(() => t.remove(), kind === 'err' ? 7000 : 3500);
}

function handleErr(e) {
  if (e.status === 401) {
    S.state.loggedIn = false;
    render();
  }
  toast(e.message, 'err');
}

function modal(title, body, buttons) {
  return new Promise((resolve) => {
    const dlg = h('dialog');
    const close = (val) => { dlg.close(); dlg.remove(); resolve(val); };
    const foot = h('div', { class: 'dfoot' });
    for (const b of buttons) {
      foot.append(h('button', {
        class: 'btn ' + (b.cls || ''), type: 'button',
        onclick: async () => {
          if (b.action && (await b.action()) === false) return;
          close(b.value);
        },
      }, b.label));
    }
    dlg.append(h('div', { class: 'dhead' }, title), h('div', { class: 'dbody' }, body), foot);
    dlg.addEventListener('cancel', (e) => { e.preventDefault(); close(undefined); });
    document.body.append(dlg);
    dlg.showModal();
  });
}

const confirmBox = (msg, okLabel, danger) =>
  modal('Bitte bestätigen', h('p', null, msg), [
    { label: 'Abbrechen', value: false },
    { label: okLabel || 'OK', cls: danger ? 'solid-danger' : 'primary', value: true },
  ]);

// ------------------------------------------------------------------ Laden

async function load() {
  const q = S.year ? `?year=${S.year}` : '';
  S.state = await api('GET', '/api/state' + q);
  S.year = S.state.year;
}

async function reload() {
  try { await load(); render(); } catch (e) { handleErr(e); }
}

// ------------------------------------------------------------------ Grundgerüst

function render() {
  const st = S.state;
  const app = document.getElementById('app');
  app.replaceChildren();

  const yearSel = h('select', {
    onchange: async (e) => { S.year = Number(e.target.value); await reload(); },
    title: 'Abrechnungsjahr',
  }, st.years.map((y) => h('option', { value: y, selected: y === st.year },
    y === st.currentYear ? `${y} (aktuell)` : `${y} (abgeschlossen)`)));
  if (S.tab === 'admin') yearSel.disabled = true;

  const tabBtn = (id, label) => h('button', {
    class: S.tab === id ? 'active' : '',
    onclick: async () => {
      S.tab = id;
      if (id === 'admin' && S.year !== st.currentYear) S.year = st.currentYear;
      await reload();
    },
  }, label);

  app.append(
    h('header', { class: 'top' },
      h('div', { class: 'brand' }, h('b', null, 'Gartenabrechnung'), h('span', null, st.settings.vereinName)),
      h('nav', { class: 'tabs' }, tabBtn('eingabe', 'Zählerstände'), tabBtn('rechnungen', 'Rechnungen'), tabBtn('zahlungen', 'Zahlungen'), tabBtn('admin', 'Admin')),
      h('div', { class: 'spacer' }),
      h('div', null, h('label', null, 'Jahr'), yearSel),
      h('button', { class: 'quit', onclick: quitApp, title: 'Programm beenden' }, 'Beenden'),
    ),
    h('main', null,
      st.readOnly && S.tab !== 'admin' && S.tab !== 'zahlungen'
        ? h('div', { class: 'banner info' }, `Das Jahr ${st.year} ist abgeschlossen. Du kannst es ansehen und Rechnungen neu ausdrucken, aber nichts mehr ändern.`)
        : null,
      S.tab === 'eingabe' ? viewEingabe() : S.tab === 'rechnungen' ? viewRechnungen() : S.tab === 'zahlungen' ? viewZahlungen() : viewAdmin(),
      h('div', { class: 'footer' }, `Gartenabrechnung ${st.version} · Copyright © ${new Date().getFullYear()} ${st.autor || ''} · Daten liegen in: `, h('span', { class: 'mono' }, st.dataDir)),
    ),
  );
}

async function quitApp() {
  if (!(await confirmBox('Soll das Programm beendet werden? Alle Eingaben sind bereits gespeichert.', 'Beenden'))) return;
  try { await api('POST', '/api/quit'); } catch (e) { /* Programm ist weg */ }
  document.getElementById('app').replaceChildren(
    h('div', { class: 'center card' }, h('h2', null, 'Programm beendet'),
      h('p', null, 'Du kannst dieses Browserfenster jetzt schließen. Zum erneuten Starten die Gartenabrechnung.exe öffnen.')));
}

// ------------------------------------------------------------------ Tab: Zählerstände

function statusBadge(res) {
  if (res.vollstaendig) return h('span', { class: 'badge ok' }, '✓ OK');
  return h('span', { class: 'badge ' + (res.status === 'Angaben fehlen' ? 'warn' : 'err'), title: res.status }, res.status === 'Angaben fehlen' ? 'unvollständig' : res.status);
}

function updateCalc(tr, res) {
  tr.querySelector('.c-wv').textContent = res.wasserVerbrauch == null ? '' : nfFlex.format(res.wasserVerbrauch);
  tr.querySelector('.c-sv').textContent = res.energieVerbrauch == null ? '' : nfFlex.format(res.energieVerbrauch);
  const g = tr.querySelector('.gesamt');
  g.className = 'gesamt' + (res.vollstaendig && res.gesamt < 0 ? ' guthaben' : '');
  g.title = res.vollstaendig && res.gesamt < 0 ? 'Guthaben zu Gunsten des Pächters' : '';
  g.textContent = res.vollstaendig ? eur(res.gesamt) : '–';
  const inf = ((S.state && S.state.issued) || {})[tr.dataset.id];
  tr.querySelector('.status').replaceChildren(...[statusBadge(res),
    inf ? h('span', { class: 'badge ' + (inf.geaendert ? 'warn' : 'neu'), title: inf.geaendert ? 'Rechnung wurde ausgestellt, danach wurden Werte geändert' : 'Rechnung ist ausgestellt' }, inf.geaendert ? '⚠ nach Ausstellung geändert' : 'ausgestellt') : null].filter(Boolean));
}

// Nach einer Änderung neu vom Programm holen, ob eine ausgestellte Rechnung davon betroffen ist.
async function refreshIssued(tr) {
  try {
    const st = await api('GET', `/api/state?year=${S.year}`);
    S.state.issued = st.issued; S.state.archive = st.archive;
    updateCalc(tr, S.state.results[tr.dataset.id]);
  } catch (e) { /* nicht kritisch */ }
}

let savedTimer;
function showSaved() {
  const el = document.querySelector('.saveind');
  if (!el) return;
  el.classList.add('show');
  clearTimeout(savedTimer);
  savedTimer = setTimeout(() => el.classList.remove('show'), 1800);
}

function hasDetails(a) {
  return !!(a && (a.versicherung || a.grundsteuer || a.auslagen || (a.hinweis && a.hinweis.trim())));
}

async function saveRow(tr, p) {
  const f = (k) => tr.querySelector(`[data-f="${k}"]`);
  const keys = ['wasserVJ', 'wasserAkt', 'stromVJ', 'stromAkt', 'stunden', 'abschlag'];
  const vals = {};
  let bad = false;
  for (const k of keys) {
    const v = parseNum(f(k).value);
    const wrong = Number.isNaN(v) || (v !== null && v < 0);
    f(k).classList.toggle('err', wrong);
    bad = bad || wrong;
    vals[k] = v;
  }
  if (bad) { toast('Bitte nur Zahlen ab 0 eingeben (Komma ist erlaubt).', 'err'); return; }
  try {
    const wz = f('wz').value.trim(), sz = f('sz').value.trim();
    if (wz !== p.wasserzaehlerNr || sz !== p.stromzaehlerNr) {
      const np = await api('PUT', `/api/zaehler/${p.id}`, { wasserzaehlerNr: wz, stromzaehlerNr: sz });
      Object.assign(p, np);
    }
    const cur = S.state.ablesungen[p.id] || {};
    const body = { ...cur, wasserVJ: vals.wasserVJ, wasserAkt: vals.wasserAkt, stromVJ: vals.stromVJ, stromAkt: vals.stromAkt,
      stunden: vals.stunden, abschlag: vals.abschlag ?? 0 };
    const res = await api('PUT', `/api/ablesung/${p.id}?year=${S.year}`, body);
    S.state.ablesungen[p.id] = body;
    S.state.results[p.id] = res;
    updateCalc(tr, res);
    showSaved();
    if ((S.state.issued || {})[p.id]) refreshIssued(tr);
  } catch (e) { handleErr(e); }
}

async function detailsDialog(tr, p) {
  const cur = S.state.ablesungen[p.id] || {};
  const num = (val, id) => h('input', { type: 'text', inputmode: 'decimal', id, value: numIn(val || null), autocomplete: 'off' });
  const vers = num(cur.versicherung, 'd-vers'), grund = num(cur.grundsteuer, 'd-grund'), ausl = num(cur.auslagen, 'd-ausl');
  const hint = h('textarea', { rows: 3, maxlength: 300, style: 'width:100%', id: 'd-hint' }, cur.hinweis || '');
  const field = (label, input, unit) => h('div', { class: 'field' }, h('label', null, label),
    unit ? h('div', { class: 'inputunit' }, input, h('span', null, unit)) : input);
  const body = h('div', { class: 'form' },
    field('Versicherung', vers, '€'), field('Grundsteuer', grund, '€'), field('Sonstige Auslagen', ausl, '€'),
    h('div', { class: 'field wide' }, h('label', null, 'Hinweis / Erläuterung (steht auf der Rechnung)'), hint));
  await modal(`Weitere Angaben – ${p.name}`, body, [
    { label: 'Abbrechen', value: false },
    { label: 'Speichern', cls: 'primary', value: true, action: async () => {
      const vs = [vers, grund, ausl].map((i) => parseNum(i.value));
      if (vs.some((v) => Number.isNaN(v) || (v !== null && v < 0))) { toast('Bitte nur Beträge ab 0 eingeben.', 'err'); return false; }
      const body2 = { ...cur, versicherung: vs[0] ?? 0, grundsteuer: vs[1] ?? 0, auslagen: vs[2] ?? 0, hinweis: hint.value.trim() };
      try {
        const res = await api('PUT', `/api/ablesung/${p.id}?year=${S.year}`, body2);
        S.state.ablesungen[p.id] = body2;
        S.state.results[p.id] = res;
        updateCalc(tr, res);
        tr.querySelector('.detailbtn').classList.toggle('primary', hasDetails(body2));
        showSaved();
        if ((S.state.issued || {})[p.id]) refreshIssued(tr);
      } catch (e) { handleErr(e); return false; }
    } },
  ]);
}

function eingabeRow(p, ro) {
  const a = S.state.ablesungen[p.id] || {};
  const res = S.state.results[p.id];
  const tr = h('tr', { 'data-id': p.id });
  const save = () => saveRow(tr, p);
  const inp = (key, val, cls) => h('input', { type: 'text', inputmode: 'decimal', class: cls || 'num', autocomplete: 'off',
    'data-f': key, value: numIn(val), disabled: ro, onchange: save });
  const txt = (key, val) => h('input', { type: 'text', class: 'nr', autocomplete: 'off', maxlength: 40, 'data-f': key,
    value: val || '', disabled: ro, onchange: save });
  tr.append(
    h('td', { class: 'sticky s1' }, p.mitgliedsnr),
    h('td', { class: 'name sticky s2', title: p.name }, p.name),
    h('td', null, txt('wz', p.wasserzaehlerNr)),
    h('td', null, inp('wasserVJ', a.wasserVJ)),
    h('td', null, inp('wasserAkt', a.wasserAkt)),
    h('td', { class: 'calc c-wv' }),
    h('td', null, txt('sz', p.stromzaehlerNr)),
    h('td', null, inp('stromVJ', a.stromVJ)),
    h('td', null, inp('stromAkt', a.stromAkt)),
    h('td', { class: 'calc c-sv' }),
    h('td', null, inp('stunden', a.stunden, 'small')),
    h('td', null, inp('abschlag', a.abschlag || null, 'small')),
    h('td', { class: 'gesamt' }),
    h('td', { class: 'status mid' }),
    h('td', null, h('button', { class: 'btn small detailbtn' + (hasDetails(a) ? ' primary' : ''), disabled: ro,
      title: 'Versicherung, Grundsteuer, Auslagen, Hinweis', onclick: () => detailsDialog(tr, p) }, 'Weitere')),
  );
  updateCalc(tr, res);
  // Enter springt in die Zeile darunter (gleiche Spalte)
  tr.addEventListener('keydown', (e) => {
    if (e.key !== 'Enter' || !e.target.dataset || !e.target.dataset.f) return;
    e.preventDefault();
    e.target.dispatchEvent(new Event('change'));
    let next = tr.nextElementSibling;
    while (next && next.hidden) next = next.nextElementSibling;
    const t = next && next.querySelector(`[data-f="${e.target.dataset.f}"]`);
    if (t) { t.focus(); t.select(); }
  });
  return tr;
}

function viewEingabe() {
  const st = S.state;
  return h('div', null,
    h('h2', null, `Zählerstände ${st.year}`),
    h('div', { class: 'subtabs' },
      h('button', { class: S.eingabeMode === 'schnell' ? 'active' : '', onclick: () => { S.eingabeMode = 'schnell'; render(); } }, 'Schnelleingabe (Nummer eintippen)'),
      h('button', { class: S.eingabeMode === 'tabelle' ? 'active' : '', onclick: () => { S.eingabeMode = 'tabelle'; render(); } }, 'Tabelle (alle Pächter)')),
    S.eingabeMode === 'schnell' ? viewSchnell() : viewTabelle());
}

// Schnelleingabe: Nummer eintippen, Name/Adresse/Größe/Zählernummern kommen aus den Stammdaten.
function viewSchnell() {
  const st = S.state;
  const ro = st.readOnly;
  const list = [...st.paechter].sort(cmpNr);
  const done = list.filter((p) => st.results[p.id] && st.results[p.id].vollstaendig).length;
  const box = h('div');
  const sugg = h('div', { class: 'sugg' });
  const find = (q) => {
    q = q.trim().toLowerCase();
    if (!q) return [];
    const exact = list.filter((p) => (p.mitgliedsnr || '').toLowerCase() === q);
    if (exact.length) return exact;
    const g = list.filter((p) => (p.gartennr || '').toLowerCase() === q);
    if (g.length) return g;
    return list.filter((p) => [p.mitgliedsnr, p.gartennr, p.name].some((x) => (x || '').toLowerCase().includes(q))).slice(0, 8);
  };
  const choose = (p) => { S.quickId = p.id; S.quickQ = ''; input.value = ''; sugg.replaceChildren(); drawCard(true); };
  const input = h('input', { type: 'search', class: 'bignr', placeholder: 'Nummer oder Name', autocomplete: 'off', style: 'width:280px', id: 'quicknr',
    oninput: () => {
      const m = find(input.value);
      sugg.replaceChildren();
      if (input.value.trim() && !m.length) sugg.append(h('span', { class: 'hint' }, 'Nichts gefunden.'));
      if (m.length > 1 || (m.length === 1 && input.value.trim().toLowerCase() !== (m[0].mitgliedsnr || '').toLowerCase()))
        for (const p of m) sugg.append(h('button', { class: 'btn small', onclick: () => choose(p) }, `${p.mitgliedsnr}  ${p.name}`));
    },
    onkeydown: (e) => {
      if (e.key !== 'Enter') return;
      e.preventDefault();
      const m = find(input.value);
      if (m.length === 1) choose(m[0]);
      else if (m.length > 1) toast('Mehrere Treffer – bitte einen anklicken oder die Nummer genauer eingeben.', 'err');
      else if (input.value.trim()) toast('Nummer nicht gefunden.', 'err');
    } });

  const drawCard = (focusFirst) => {
    box.replaceChildren();
    const p = list.find((x) => x.id === S.quickId);
    if (!p) {
      box.append(h('div', { class: 'card empty' }, list.length ? 'Tippe oben eine Nummer ein und drücke Enter. Alle Angaben des Pächters erscheinen dann automatisch.' : 'Noch keine Pächter angelegt. Das geht im Bereich »Admin« → Pächter.'));
      return;
    }
    const a = st.ablesungen[p.id] || {};
    const card = h('div', { class: 'card quick', 'data-id': p.id });
    const save = () => saveRow(card, p);
    const inp = (key, val, cls) => h('input', { type: 'text', inputmode: 'decimal', class: cls || 'num', autocomplete: 'off', 'data-f': key, value: numIn(val), disabled: ro, onchange: save });
    const txt = (key, val) => h('input', { type: 'text', class: 'nr', autocomplete: 'off', maxlength: 40, 'data-f': key, value: val || '', disabled: ro, onchange: save });
    const fld = (label, el, unit) => h('div', { class: 'field' }, h('label', null, label), unit ? h('div', { class: 'inputunit' }, el, h('span', null, unit)) : el);
    const info = (k, v) => v ? h('span', { class: 'chip' }, h('i', null, k + ' '), v) : null;
    card.append(
      h('div', { class: 'qhead' },
        h('div', null, h('h3', null, `${p.mitgliedsnr} – ${p.name}`), h('div', { class: 'hint' }, [p.strasse, p.plzOrt].filter(Boolean).join(', '))),
        h('div', { class: 'chips' }, info('Garten', p.gartennr), info('Größe', p.gartengroesse ? `${nfFlex.format(p.gartengroesse)} m²` : ''),
          info('Umlage', eur(p.umlageAbweichend != null ? p.umlageAbweichend : st.settings.umlageStandard)))),
      h('div', { class: 'qgrid' },
        h('fieldset', null, h('legend', null, 'Wasser'), fld('Zähler-Nr.', txt('wz', p.wasserzaehlerNr)),
          fld('Stand Vorjahr', inp('wasserVJ', a.wasserVJ), 'm³'), fld('Stand aktuell', inp('wasserAkt', a.wasserAkt), 'm³'),
          h('div', { class: 'field' }, h('label', null, 'Verbrauch'), h('div', { class: 'calc c-wv qv' }))),
        h('fieldset', null, h('legend', null, 'Strom'), fld('Zähler-Nr.', txt('sz', p.stromzaehlerNr)),
          fld('Stand Vorjahr', inp('stromVJ', a.stromVJ), 'kWh'), fld('Stand aktuell', inp('stromAkt', a.stromAkt), 'kWh'),
          h('div', { class: 'field' }, h('label', null, 'Verbrauch'), h('div', { class: 'calc c-sv qv' }))),
        h('fieldset', null, h('legend', null, 'Sonstiges'), fld('Arbeitsstunden', inp('stunden', a.stunden, 'small'), 'h'),
          fld('Abschlag', inp('abschlag', a.abschlag || null, 'small'), '€'),
          h('button', { class: 'btn small detailbtn' + (hasDetails(a) ? ' primary' : ''), disabled: ro, onclick: () => detailsDialog(card, p) }, 'Weitere Angaben'))),
      h('div', { class: 'qfoot' },
        h('div', null, h('span', { class: 'hint' }, 'Rechnungsbetrag: '), h('b', { class: 'gesamt' }), ' ', h('span', { class: 'status' })),
        h('div', { class: 'spacer' }),
        h('button', { class: 'btn', onclick: () => { S.tab = 'rechnungen'; S.selInvoice = p.id; S.invTab = 'pruefen'; S.invView = 'archiv'; render(); } }, 'Rechnung ansehen'),
        h('button', { class: 'btn primary', onclick: () => { input.focus(); } }, 'Fertig – nächste Nummer')));
    updateCalc(card, st.results[p.id]);
    box.append(card);

    // Enter: nächstes Feld; Pflichtfelder immer, Zählernummer/Vorjahresstände nur wenn noch leer
    const seq = ['wz', 'wasserVJ', 'wasserAkt', 'sz', 'stromVJ', 'stromAkt', 'stunden', 'abschlag'];
    const must = new Set(['wasserAkt', 'stromAkt', 'stunden', 'abschlag']);
    const want = (k) => { const el = card.querySelector(`[data-f="${k}"]`); return el && !el.disabled && (must.has(k) || el.value.trim() === ''); };
    const goto = (from) => {
      for (let i = from; i < seq.length; i++) if (want(seq[i])) { const el = card.querySelector(`[data-f="${seq[i]}"]`); el.focus(); el.select(); return; }
      input.focus();
    };
    card.addEventListener('keydown', (e) => {
      if (e.key !== 'Enter' || !e.target.dataset || !e.target.dataset.f) return;
      e.preventDefault();
      e.target.dispatchEvent(new Event('change'));
      goto(seq.indexOf(e.target.dataset.f) + 1);
    });
    if (focusFirst && !ro) goto(0);
  };
  drawCard(false);

  return h('div', null,
    h('p', { class: 'hint' }, 'Nummer eintippen, Enter drücken: Name, Adresse, Gartengröße, Umlage und Zählernummern kommen automatisch aus den Stammdaten, der Vorjahresstand ist schon eingetragen. Du gibst nur noch die neuen Stände ein. Mit Enter springst du zum nächsten Feld, nach dem letzten wieder zur Nummer. Alles wird sofort gespeichert.'),
    h('div', { class: 'toolbar' }, input, h('span', { class: 'stat' }, `${done} von ${list.length} vollständig`), h('span', { class: 'saveind' }, '✓ gespeichert')),
    sugg, box);
}

function viewTabelle() {
  const st = S.state;
  const ro = st.readOnly;
  const list = [...st.paechter].sort(cmpNr);
  const done = list.filter((p) => st.results[p.id] && st.results[p.id].vollstaendig).length;

  const tbody = h('tbody');
  const fill = () => {
    tbody.replaceChildren();
    const q = S.filter.trim().toLowerCase();
    const shown = list.filter((p) => !q || [p.mitgliedsnr, p.name, p.gartennr].some((x) => (x || '').toLowerCase().includes(q)));
    for (const p of shown) tbody.append(eingabeRow(p, ro));
    if (!shown.length) tbody.append(h('tr', null, h('td', { colspan: 15, class: 'empty' },
      list.length ? 'Keine Treffer.' : 'Noch keine Pächter angelegt. Das geht im Bereich »Admin« → Pächter.')));
  };
  fill();

  const search = h('input', { type: 'search', placeholder: 'Suchen (Nummer oder Name)', value: S.filter, style: 'width:240px',
    oninput: (e) => { S.filter = e.target.value; fill(); } });

  const th = (t, cls, extra) => h('th', { class: cls || '', ...(extra || {}) }, t);
  return h('div', null,
    h('p', { class: 'hint' }, 'Hier trägst du pro Pächter die Zählernummern, Zählerstände und Arbeitsstunden ein. Alles wird sofort gespeichert und die Beträge werden automatisch berechnet.'),
    h('div', { class: 'toolbar' }, search,
      h('span', { class: 'stat' }, `${list.length} Pächter · ${done} vollständig`),
      h('span', { class: 'saveind' }, '✓ gespeichert'),
      h('div', { class: 'spacer' }),
      h('a', { class: 'btn', href: `/api/export?year=${st.year}` }, 'Übersicht als Excel'),
    ),
    h('div', { class: 'tablewrap' }, h('table', null,
      h('thead', null,
        h('tr', { class: 'group' }, th('', 'sticky s1', { colspan: 2 }), th('Wasser', '', { colspan: 4 }), th('Strom / Energie', '', { colspan: 4 }),
          th('', '', { colspan: 5 })),
        h('tr', { class: 'sub' }, th('Nr.', 'sticky s1'), th('Name', 'sticky s2'),
          th('Zähler-Nr.'), th('Stand Vorjahr'), th('Stand aktuell'), th('Verbrauch m³'),
          th('Zähler-Nr.'), th('Stand Vorjahr'), th('Stand aktuell'), th('Verbrauch kWh'),
          th('Arbeits-stunden'), th('Abschlag €'), th('Gesamt-betrag'), th('Status'), th('')),
      ), tbody)),
  );
}

// ------------------------------------------------------------------ Tab: Rechnungen

const fmtWhen = (iso) => { const d = new Date(iso); return isNaN(d) ? '' : d.toLocaleString('de-DE', { dateStyle: 'medium', timeStyle: 'short' }); };

function issueSummary(r) {
  return h('div', null,
    h('p', null, `${r.created.length} Rechnung(en) ausgestellt und gespeichert. Die PDF-Dateien liegen hier:`), h('p', { class: 'mono' }, r.folder),
    (r.skipped || []).length ? h('div', { class: 'banner warn' }, h('b', null, `${r.skipped.length} nicht ausgestellt:`),
      h('ul', { class: 'plain' }, r.skipped.map((x) => h('li', null, `${x.name}: ${x.reason}`)))) : null);
}

async function issue(ids, replace) {
  const st = S.state;
  const r = await api('POST', `/api/invoices/issue?year=${st.year}`, { ids, replace: !!replace });
  const open = await modal('Fertig', issueSummary(r), [{ label: 'Schließen', value: false }, { label: 'Ordner öffnen', cls: 'primary', value: true }]);
  if (open) await api('POST', '/api/open-folder', { which: 'rechnungen', year: st.year });
  await reload();
}

function viewRechnungen() {
  const st = S.state;
  const sub = h('div', { class: 'subtabs' },
    h('button', { class: S.invTab !== 'archiv' ? 'active' : '', onclick: () => { S.invTab = 'pruefen'; render(); } }, 'Rechnungen prüfen & ausstellen'),
    h('button', { class: S.invTab === 'archiv' ? 'active' : '', onclick: () => { S.invTab = 'archiv'; render(); } }, `Archiv (${(st.archive || []).length})`));
  return h('div', null, h('h2', null, `Rechnungen ${st.year}`), sub, S.invTab === 'archiv' ? viewArchiv() : viewAusstellen());
}

function viewArchiv() {
  const st = S.state;
  const rows = st.archive || [];
  const rowEls = rows.map((a) => {
    const url = `/api/archive/${a.id}.pdf`;
    return h('tr', { class: a.status === 'ersetzt' ? 'ersetzt' : '' },
      h('td', null, a.mitgliedsnr), h('td', null, a.name), h('td', { class: 'mono' }, a.nummer), h('td', { class: 'mid' }, a.version),
      h('td', { class: 'r' }, eur(a.gesamt)), h('td', null, fmtWhen(a.ausgestellt)),
      h('td', null, h('span', { class: 'badge ' + (a.status === 'gueltig' ? 'ok' : 'warn') }, a.status === 'gueltig' ? 'gültig' : 'ersetzt')),
      h('td', null, h('a', { class: 'btn small', href: url, target: '_blank' }, 'Ansehen'), ' ',
        h('a', { class: 'btn small', href: url, download: a.datei ? a.datei.split('/').pop() : 'Rechnung.pdf' }, 'Speichern')));
  });
  return h('div', null,
    h('p', { class: 'hint' }, `Hier liegen alle ausgestellten Rechnungen des Jahres ${st.year}. Sie sind mit den Preisen und Angaben von damals gespeichert und ändern sich nie, auch wenn du später Preise, Namen oder Zählerstände änderst. Wurde eine Rechnung neu ausgestellt, bleibt die alte als »ersetzt« erhalten.`),
    h('div', { class: 'toolbar' },
      h('button', { class: 'btn', onclick: () => api('POST', '/api/open-folder', { which: 'rechnungen', year: st.year }).catch(handleErr) }, 'Rechnungsordner öffnen')),
    rows.length ? h('div', { class: 'card tablewrap' }, h('table', { class: 'data' },
      h('thead', null, h('tr', null, ['Nr.', 'Name', 'Rechnungs-Nr.', 'Version', 'Betrag', 'Ausgestellt am', 'Status', ''].map((t) => h('th', null, t)))),
      h('tbody', null, rowEls)))
      : h('div', { class: 'card empty' }, 'In diesem Jahr wurde noch keine Rechnung ausgestellt.'));
}

function viewAusstellen() {
  const st = S.state;
  const list = [...st.paechter].sort(cmpNr);
  if (!list.some((p) => p.id === S.selInvoice)) S.selInvoice = list.length ? list[0].id : null;
  const right = h('div');
  const issuedOf = (p) => (st.issued || {})[p.id];

  const drawPreview = () => {
    right.replaceChildren();
    const p = list.find((x) => x.id === S.selInvoice);
    if (!p) { right.append(h('div', { class: 'card empty' }, 'Noch keine Pächter vorhanden.')); return; }
    const res = st.results[p.id];
    const inf = issuedOf(p);
    const showLive = !inf || S.invView === 'live';
    const url = showLive ? `/api/invoice/${p.id}.pdf?year=${st.year}` : `/api/archive/${inf.id}.pdf`;
    const canShow = showLive ? res.vollstaendig : true;

    const bar = h('div', { class: 'toolbar' }, h('b', null, `${p.mitgliedsnr} – ${p.name}`), h('div', { class: 'spacer' }));
    if (!inf && res.vollstaendig) {
      bar.append(h('button', { class: 'btn primary', onclick: async () => { try { await issue([p.id], false); } catch (e) { handleErr(e); } } }, 'Rechnung ausstellen & speichern'));
    }
    if (canShow) {
      bar.append(h('a', { class: 'btn', href: url, target: '_blank' }, 'PDF öffnen / drucken'),
        h('a', { class: 'btn', href: url, download: `Rechnung_${p.mitgliedsnr}.pdf` }, 'PDF speichern'));
    }
    right.append(bar);

    if (inf) {
      right.append(h('div', { class: 'banner info' },
        `Ausgestellt am ${fmtWhen(inf.ausgestellt)} · Rechnungs-Nr. ${inf.nummer}${inf.version > 1 ? ` (Version ${inf.version})` : ''} · ${eur(inf.gesamt)}. Diese Rechnung ist gespeichert und ändert sich nicht mehr.`));
      if (inf.geaendert) {
        right.append(h('div', { class: 'banner warn' },
          'Seit der Ausstellung wurden Werte geändert (Zählerstände, Preise oder Pächterdaten). Die ausgestellte Rechnung bleibt unverändert. ',
          h('button', { class: 'btn small', onclick: () => { S.invView = showLive ? 'archiv' : 'live'; drawPreview(); } }, showLive ? 'Ausgestellte Rechnung ansehen' : 'Mit aktuellen Werten ansehen'), ' ',
          res.vollstaendig ? h('button', { class: 'btn small', onclick: async () => {
            const ok = await confirmBox(`Für ${p.name} wird eine neue Rechnung mit den aktuellen Werten ausgestellt. Die bisherige bleibt als »ersetzt« im Archiv. Fortfahren?`, 'Neu ausstellen');
            if (ok) { try { S.invView = 'archiv'; await issue([p.id], true); } catch (e) { handleErr(e); } }
          } }, 'Neu ausstellen (ersetzt die alte)') : null));
      }
    }
    if (!canShow) {
      right.append(h('div', { class: 'banner warn' }, `Für diese Rechnung fehlt noch etwas: ${res.status}. Bitte im Tab »Zählerstände« ergänzen.`,
        ' ', h('button', { class: 'btn small', onclick: () => { S.tab = 'eingabe'; S.filter = p.mitgliedsnr; render(); } }, 'Zu den Zählerständen')));
      return;
    }
    right.append(h('div', { class: 'preview' }, h('iframe', { src: `${url}${showLive ? '&t=' + Date.now() : ''}#view=FitH`, title: 'Rechnungsvorschau' })));
  };

  const items = h('div', { class: 'card list' });
  const drawList = () => {
    items.replaceChildren();
    for (const p of list) {
      const ok = st.results[p.id] && st.results[p.id].vollstaendig;
      const inf = issuedOf(p);
      const cls = inf ? (inf.geaendert ? 'chg' : 'iss') : ok ? 'ok' : '';
      const tip = inf ? (inf.geaendert ? 'ausgestellt, danach geändert' : 'ausgestellt') : ok ? 'bereit zum Ausstellen' : 'unvollständig';
      items.append(h('button', { class: 'item' + (p.id === S.selInvoice ? ' active' : ''), onclick: () => { S.selInvoice = p.id; S.invView = 'archiv'; drawList(); drawPreview(); } },
        h('span', { class: 'dot ' + cls, title: tip }),
        h('span', { class: 'nr' }, p.mitgliedsnr), h('span', null, p.name)));
    }
    if (!list.length) items.append(h('div', { class: 'empty' }, 'Keine Pächter.'));
  };
  drawList();
  drawPreview();

  const open = list.filter((p) => st.results[p.id] && st.results[p.id].vollstaendig && !issuedOf(p));
  const nIss = list.filter((p) => issuedOf(p)).length;
  const allBtn = h('button', { class: 'btn primary', disabled: !open.length, onclick: async () => {
    const ok = await confirmBox(`${open.length} Rechnung(en) werden jetzt ausgestellt und als PDF gespeichert. Unvollständige Rechnungen werden übersprungen. Fortfahren?`, 'Ausstellen');
    if (!ok) return;
    allBtn.disabled = true;
    try { await issue([], false); } catch (e) { handleErr(e); }
    allBtn.disabled = false;
  } }, `Alle offenen ausstellen (${open.length})`);

  return h('div', null,
    h('p', { class: 'hint' }, 'Wähle links einen Pächter und prüfe die Rechnung. Mit »Ausstellen« wird sie festgeschrieben, als PDF gespeichert und ins Archiv gelegt. Punkte: grau = unvollständig, grün = bereit, blau = ausgestellt, orange = ausgestellt, danach geändert.'),
    h('div', { class: 'toolbar' }, allBtn, h('span', { class: 'hint' }, `${nIss} von ${list.length} ausgestellt`), h('div', { class: 'spacer' }),
      h('button', { class: 'btn', onclick: () => api('POST', '/api/open-folder', { which: 'rechnungen', year: st.year }).catch(handleErr) }, 'Rechnungsordner öffnen')),
    h('div', { class: 'split' }, items, right));
}

// ------------------------------------------------------------------ Tab: Zahlungen

const deDate = (iso) => (iso ? iso.split('-').reverse().join('.') : '');
const todayIso = () => { const d = new Date(); const p = (n) => String(n).padStart(2, '0'); return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`; };
const payTotal = (e) => Math.abs(e.gesamt);
const payPaid = (e) => (e.bezahltAm ? (e.bezahltBetrag != null ? e.bezahltBetrag : payTotal(e)) : 0);
const payOpen = (e) => Math.round((payTotal(e) - payPaid(e)) * 100) / 100;
const payIsOpen = (e) => payOpen(e) > 0.004;

async function savePayment(e, patch) {
  const body = { bezahltAm: e.bezahltAm || '', bezahltBetrag: e.bezahltBetrag ?? null, notiz: e.notiz || '', ...patch };
  await api('PUT', `/api/payment/${e.id}`, body);
  Object.assign(e, { bezahltAm: body.bezahltAm, bezahltBetrag: body.bezahltAm ? body.bezahltBetrag : null, notiz: body.notiz });
  // volle Zahlung: Betrag nicht separat führen
  if (e.bezahltBetrag != null && Math.round(e.bezahltBetrag * 100) === Math.round(payTotal(e) * 100)) e.bezahltBetrag = null;
}

async function paymentDialog(e) {
  const date = h('input', { type: 'date', value: e.bezahltAm || todayIso() });
  const amt = h('input', { type: 'text', inputmode: 'decimal', value: numIn(e.bezahltAm ? payPaid(e) : payTotal(e)), style: 'width:120px' });
  const note = h('input', { type: 'text', maxlength: 200, value: e.notiz || '', style: 'width:100%' });
  const body = h('div', { class: 'form' },
    h('p', null, `${e.mitgliedsnr} – ${e.name}: Rechnungsbetrag ${eur(payTotal(e))}${e.gesamt < 0 ? ' (Guthaben, wird an den Pächter ausgezahlt)' : ''}`),
    h('div', { class: 'field' }, h('label', null, 'Bezahlt am'), date),
    h('div', { class: 'field' }, h('label', null, 'Bezahlter Betrag (bei Teilzahlung anpassen)'), h('div', { class: 'inputunit' }, amt, h('span', null, '€'))),
    h('div', { class: 'field wide' }, h('label', null, 'Notiz (z. B. bar, Überweisung, Ratenzahlung)'), note));
  return modal('Zahlung vermerken', body, [
    { label: 'Abbrechen', value: false },
    e.bezahltAm ? { label: 'Zahlung zurücknehmen', cls: 'danger', value: 'undo', action: async () => { try { await savePayment(e, { bezahltAm: '', bezahltBetrag: null }); } catch (er) { handleErr(er); return false; } } } : null,
    { label: 'Speichern', cls: 'primary', value: true, action: async () => {
      const v = parseNum(amt.value);
      if (!date.value) { toast('Bitte ein Datum angeben.', 'err'); return false; }
      if (v === null || Number.isNaN(v) || v < 0) { toast('Bitte einen gültigen Betrag angeben.', 'err'); return false; }
      try { await savePayment(e, { bezahltAm: date.value, bezahltBetrag: v, notiz: note.value.trim() }); } catch (er) { handleErr(er); return false; }
    } },
  ].filter(Boolean));
}

function viewZahlungen() {
  const st = S.state;
  const all = (st.archive || []).filter((e) => e.status === 'gueltig');
  const today = todayIso();
  const summary = h('div', { class: 'cards' });
  const tbody = h('tbody');
  const filters = h('div', { class: 'subtabs' });

  const draw = () => {
    const sum = (arr, f) => arr.reduce((a, e) => a + f(e), 0);
    const claims = all.filter((e) => e.gesamt >= 0), credits = all.filter((e) => e.gesamt < 0);
    const openClaims = claims.filter(payIsOpen), overdue = openClaims.filter((e) => e.faellig && e.faellig < today);
    const card = (t, big, small, cls) => h('div', { class: 'card stat ' + (cls || '') }, h('div', { class: 'hint' }, t), h('div', { class: 'big' }, big), h('div', { class: 'hint' }, small));
    summary.replaceChildren(...[
      card('Forderungen gesamt', eur(sum(claims, payTotal)), `${claims.length} Rechnungen`),
      card('Bereits bezahlt', eur(sum(claims, payPaid)), `${claims.filter((e) => !payIsOpen(e)).length} vollständig bezahlt`, 'ok'),
      card('Noch offen', eur(sum(openClaims, payOpen)), `${openClaims.length} Rechnungen`, openClaims.length ? 'warn' : 'ok'),
      card('Überfällig', String(overdue.length), overdue.length ? eur(sum(overdue, payOpen)) : 'nichts überfällig', overdue.length ? 'err' : 'ok'),
      credits.length ? card('Guthaben auszuzahlen', eur(sum(credits.filter(payIsOpen), payOpen)), `${credits.filter(payIsOpen).length} von ${credits.length} noch offen`) : null].filter(Boolean));

    const nOpen = all.filter(payIsOpen).length;
    filters.replaceChildren(
      ...[['offen', `Noch offen (${nOpen})`], ['bezahlt', `Bezahlt (${all.length - nOpen})`], ['alle', `Alle (${all.length})`]].map(([k, t]) =>
        h('button', { class: S.payFilter === k ? 'active' : '', onclick: () => { S.payFilter = k; draw(); } }, t)));

    const q = S.payQ.trim().toLowerCase();
    const shown = all.filter((e) => (S.payFilter === 'alle' || (S.payFilter === 'offen') === payIsOpen(e)) &&
      (!q || [e.mitgliedsnr, e.name, e.nummer].some((x) => (x || '').toLowerCase().includes(q))))
      .sort((a, b) => a.mitgliedsnr.localeCompare(b.mitgliedsnr, 'de', { numeric: true }));
    tbody.replaceChildren();
    for (const e of shown) {
      const open = payIsOpen(e);
      const late = open && e.gesamt >= 0 && e.faellig && e.faellig < today;
      const partial = e.bezahltAm && open;
      const chk = h('input', { type: 'checkbox', checked: !!e.bezahltAm && !open, title: 'als bezahlt markieren',
        onchange: async (ev) => {
          try {
            if (ev.target.checked) await savePayment(e, { bezahltAm: todayIso(), bezahltBetrag: null });
            else await savePayment(e, { bezahltAm: '', bezahltBetrag: null });
            draw();
          } catch (er) { ev.target.checked = !ev.target.checked; handleErr(er); }
        } });
      const date = h('input', { type: 'date', value: e.bezahltAm || '', disabled: !e.bezahltAm, title: 'Zahldatum',
        onchange: async (ev) => {
          if (!ev.target.value) { ev.target.value = e.bezahltAm; return; }
          try { await savePayment(e, { bezahltAm: ev.target.value }); draw(); } catch (er) { handleErr(er); }
        } });
      tbody.append(h('tr', { class: late ? 'late' : '' },
        h('td', null, e.mitgliedsnr), h('td', null, e.name), h('td', { class: 'mono' }, e.nummer),
        h('td', { class: 'r' }, (e.gesamt < 0 ? '−' : '') + eur(payTotal(e))),
        h('td', null, deDate(e.faellig)),
        h('td', null, h('span', { class: 'badge ' + (partial ? 'warn' : !open ? 'ok' : late ? 'err' : 'neu') },
          partial ? `Teilzahlung, offen ${eur(payOpen(e))}` : !open ? (e.gesamt < 0 ? 'ausgezahlt' : 'bezahlt') : late ? 'überfällig' : e.gesamt < 0 ? 'Guthaben offen' : 'offen')),
        h('td', { class: 'paycell' }, chk, date),
        h('td', { class: 'note', title: e.notiz || '' }, e.notiz || ''),
        h('td', null, h('button', { class: 'btn small', onclick: async () => { const r = await paymentDialog(e); if (r !== false && r !== undefined) draw(); } }, 'Details'))));
    }
    if (!shown.length) tbody.append(h('tr', null, h('td', { colspan: 9, class: 'empty' },
      !all.length ? `Für ${st.year} wurde noch keine Rechnung ausgestellt. Das machst du im Tab »Rechnungen«.` : S.payFilter === 'offen' && !q ? 'Alles bezahlt – nichts mehr offen.' : 'Keine Treffer.')));
  };
  draw();

  const notIssued = st.paechter.length - Object.keys(st.issued || {}).length;
  return h('div', null,
    h('h2', null, `Zahlungen ${st.year}`),
    h('p', { class: 'hint' }, 'Hier siehst du, welche Rechnungen bezahlt sind und welche noch offen. Haken setzen = bezahlt heute (das Datum kannst du ändern). Über »Details« trägst du Teilzahlungen oder eine Notiz ein. Rechnungen mit Guthaben erscheinen hier auch, damit du siehst, was noch an Pächter auszuzahlen ist.'),
    notIssued > 0 ? h('div', { class: 'banner info' }, `${notIssued} Pächter haben in ${st.year} noch keine ausgestellte Rechnung und tauchen deshalb hier noch nicht auf.`) : null,
    summary,
    h('div', { class: 'toolbar' },
      h('input', { type: 'search', placeholder: 'Suchen (Nummer oder Name)', value: S.payQ, style: 'width:240px', oninput: (e) => { S.payQ = e.target.value; draw(); } }),
      h('div', { class: 'spacer' }),
      h('a', { class: 'btn', href: `/api/export-payments?year=${st.year}&nur=offen` }, 'Offene Posten als Excel'),
      h('a', { class: 'btn', href: `/api/export-payments?year=${st.year}` }, 'Alle Zahlungen als Excel')),
    filters,
    h('div', { class: 'card tablewrap' }, h('table', { class: 'data' },
      h('thead', null, h('tr', null, ['Nr.', 'Name', 'Rechnungs-Nr.', 'Betrag', 'Fällig am', 'Status', 'Bezahlt / am', 'Notiz', ''].map((t) => h('th', null, t)))),
      tbody)));
}

// ------------------------------------------------------------------ Tab: Admin

function viewAdmin() {
  const st = S.state;
  if (st.hasPassword && !st.loggedIn) return viewLogin();
  const subs = [['paechter', 'Pächter'], ['einstellungen', 'Preise & Einstellungen'], ['jahreswechsel', 'Jahreswechsel'],
    ['daten', 'Import / Export / Sicherung'], ['sicherheit', 'Passwort']];
  const body = S.adminTab === 'paechter' ? adminPaechter() : S.adminTab === 'einstellungen' ? adminSettings()
    : S.adminTab === 'jahreswechsel' ? adminYear() : S.adminTab === 'daten' ? adminData() : adminSecurity();
  return h('div', null,
    h('h2', null, 'Admin-Bereich'),
    !st.hasPassword ? h('div', { class: 'banner warn' }, 'Für den Admin-Bereich ist noch kein Passwort gesetzt – jeder kann hier Pächter und Preise ändern. ',
      h('button', { class: 'btn small', onclick: () => { S.adminTab = 'sicherheit'; render(); } }, 'Passwort festlegen')) : null,
    h('div', { class: 'subtabs' }, subs.map(([id, label]) => h('button', { class: S.adminTab === id ? 'active' : '',
      onclick: () => { S.adminTab = id; S.importPreview = null; render(); } }, label))),
    body);
}

function viewLogin() {
  const pw = h('input', { type: 'password', autocomplete: 'current-password', style: 'width:100%', autofocus: true });
  const go = async () => {
    try { await api('POST', '/api/admin/login', { password: pw.value }); await reload(); } catch (e) { handleErr(e); pw.select(); }
  };
  pw.addEventListener('keydown', (e) => { if (e.key === 'Enter') go(); });
  setTimeout(() => pw.focus(), 50);
  return h('div', { class: 'center card' }, h('h2', null, 'Admin-Anmeldung'),
    h('p', { class: 'hint' }, 'Der Admin-Bereich ist durch ein Passwort geschützt.'), pw,
    h('div', { class: 'actions' }, h('button', { class: 'btn primary', onclick: go }, 'Anmelden')));
}

// ---- Pächter verwalten

function paechterDialog(p) {
  const isNew = !p;
  const cur = p || { mitgliedsnr: '', gartennr: '', anrede: 'Herr', name: '', strasse: '', plzOrt: S.state.settings.ort, versand: 'Emailsendung',
    gartengroesse: 0, umlageAbweichend: null, wasserzaehlerNr: '', stromzaehlerNr: '' };
  const t = (val, ph) => h('input', { type: 'text', value: val || '', placeholder: ph || '', maxlength: 100, autocomplete: 'off' });
  const f = {
    mitgliedsnr: t(cur.mitgliedsnr, 'z. B. 35-95'), gartennr: t(cur.gartennr, 'z. B. 35'),
    anrede: h('select', { value: cur.anrede }, ['Herr', 'Frau', 'Familie', 'Herr und Frau', ''].map((a) => h('option', { value: a }, a || '(keine)'))),
    name: t(cur.name), strasse: t(cur.strasse), plzOrt: t(cur.plzOrt),
    versand: h('select', { value: cur.versand }, ['Emailsendung', 'Postversand'].map((a) => h('option', { value: a }, a))),
    gartengroesse: t(numIn(cur.gartengroesse || null)),
    umlage: t(numIn(cur.umlageAbweichend), `leer = Standard (${eur(S.state.settings.umlageStandard)})`),
    wz: t(cur.wasserzaehlerNr), sz: t(cur.stromzaehlerNr),
  };
  f.gartengroesse.setAttribute('inputmode', 'decimal');
  f.umlage.setAttribute('inputmode', 'decimal');
  const fld = (label, input, hint, wide) => h('div', { class: 'field' + (wide ? ' wide' : '') }, h('label', null, label), input, hint ? h('span', { class: 'fhint' }, hint) : null);
  const body = h('div', { class: 'form' },
    fld('Mitgliedsnummer *', f.mitgliedsnr), fld('Gartennummer', f.gartennr), fld('Anrede', f.anrede),
    fld('Name *', f.name), fld('Straße und Hausnummer', f.strasse), fld('PLZ und Ort', f.plzOrt),
    fld('Versandart', f.versand, 'Steht oben rechts auf der Rechnung'),
    fld('Gartengröße in m² *', f.gartengroesse), fld('Umlage abweichend in €', f.umlage, 'Nur ausfüllen, wenn dieser Pächter nicht die Standard-Umlage zahlt'),
    fld('Wasserzähler-Nr.', f.wz), fld('Stromzähler-Nr.', f.sz));
  return modal(isNew ? 'Pächter anlegen' : `Pächter bearbeiten – ${cur.name}`, body, [
    { label: 'Abbrechen', value: false },
    { label: 'Speichern', cls: 'primary', value: true, action: async () => {
      const gg = parseNum(f.gartengroesse.value), um = parseNum(f.umlage.value);
      if (Number.isNaN(gg) || Number.isNaN(um) || (gg !== null && gg < 0) || (um !== null && um < 0)) { toast('Gartengröße und Umlage müssen Zahlen ab 0 sein.', 'err'); return false; }
      const data = { mitgliedsnr: f.mitgliedsnr.value, gartennr: f.gartennr.value, anrede: f.anrede.value, name: f.name.value,
        strasse: f.strasse.value, plzOrt: f.plzOrt.value, versand: f.versand.value, gartengroesse: gg ?? 0, umlageAbweichend: um,
        wasserzaehlerNr: f.wz.value, stromzaehlerNr: f.sz.value };
      try {
        if (isNew) await api('POST', '/api/admin/paechter', data);
        else await api('PUT', `/api/admin/paechter/${cur.id}`, data);
        toast('Gespeichert', 'ok');
      } catch (e) { handleErr(e); return false; }
    } },
  ]);
}

function adminPaechter() {
  const st = S.state;
  const list = [...st.paechter].sort(cmpNr);
  const tbody = h('tbody');
  const fill = () => {
    tbody.replaceChildren();
    const q = S.filter.trim().toLowerCase();
    const shown = list.filter((p) => !q || [p.mitgliedsnr, p.name, p.gartennr, p.strasse].some((x) => (x || '').toLowerCase().includes(q)));
    for (const p of shown) {
      tbody.append(h('tr', null,
        h('td', null, p.mitgliedsnr), h('td', null, p.gartennr), h('td', { class: 'name', title: p.name }, p.name),
        h('td', null, [p.strasse, p.plzOrt].filter(Boolean).join(', ')),
        h('td', { class: 'num' }, nfFlex.format(p.gartengroesse) + ' m²'),
        h('td', { class: 'num' }, p.umlageAbweichend == null ? 'Standard' : eur(p.umlageAbweichend)),
        h('td', null, p.wasserzaehlerNr), h('td', null, p.stromzaehlerNr),
        h('td', null,
          h('button', { class: 'btn small', onclick: async () => { if (await paechterDialog(p)) await reload(); } }, 'Bearbeiten'), ' ',
          h('button', { class: 'btn small danger', onclick: async () => {
            const ok = await confirmBox(`Pächter „${p.name}“ (${p.mitgliedsnr}) wirklich löschen? Die Zählerstände des laufenden Jahres gehen dabei verloren. Vorher wird automatisch eine Sicherung angelegt. Bereits ausgestellte Rechnungen bleiben im Archiv erhalten, abgeschlossene Jahre bleiben unverändert.`, 'Löschen', true);
            if (!ok) return;
            try { await api('DELETE', `/api/admin/paechter/${p.id}`); toast('Gelöscht', 'ok'); await reload(); } catch (e) { handleErr(e); }
          } }, 'Löschen'))));
    }
    if (!shown.length) tbody.append(h('tr', null, h('td', { colspan: 9, class: 'empty' }, list.length ? 'Keine Treffer.' : 'Noch keine Pächter. Lege den ersten mit »Pächter anlegen« an oder importiere eine Liste.')));
  };
  fill();
  return h('div', null,
    h('p', { class: 'hint' }, 'Hier pflegst du die Stammdaten der Pächter. Sie ändern sich selten und müssen nur einmal angelegt werden.'),
    h('div', { class: 'toolbar' },
      h('button', { class: 'btn primary', onclick: async () => { if (await paechterDialog(null)) await reload(); } }, '+ Pächter anlegen'),
      h('input', { type: 'search', placeholder: 'Suchen', value: S.filter, style: 'width:220px', oninput: (e) => { S.filter = e.target.value; fill(); } }),
      h('span', { class: 'stat' }, `${list.length} Pächter`)),
    h('div', { class: 'tablewrap' }, h('table', null,
      h('thead', null, h('tr', null, ['Mitgl.-Nr.', 'Garten', 'Name', 'Anschrift', 'Größe', 'Umlage', 'Wasserzähler', 'Stromzähler', ''].map((x) => h('th', null, x)))), tbody)));
}

// ---- Einstellungen

const SETTINGS_FORM = [
  ['Verein und Rechnung', [
    ['vereinName', 'Vereinsname', 'text', '', true], ['ort', 'PLZ und Ort', 'text'], ['absenderzeile', 'Absenderzeile (kleine Zeile über der Anschrift)', 'text', '', true],
    ['rechnungsdatum', 'Rechnungsdatum', 'date'], ['zahlungsziel', 'Zahlbar bis', 'date'],
    ['rechnungsnrPraefix', 'Rechnungsnummer-Vorsatz', 'text', 'Rechnungsnr. = Vorsatz + Mitgliedsnr., z. B. 100-35-95'],
    ['einspruchTage', 'Einspruchsfrist', 'num', 'Tage']]],
  ['Bankverbindung', [['bankName', 'Bank', 'text'], ['iban', 'IBAN', 'text'], ['bic', 'BIC', 'text']]],
  ['Wasser', [['wasserGrundpreis', 'Grundpreis pro Jahr', 'num', '€'], ['wasserPreis', 'Preis je m³', 'num', '€']]],
  ['Strom / Energie', [['energieGrundpreis', 'Grundpreis pro Jahr', 'num', '€'], ['energiePreis', 'Preis je kWh', 'num', '€']]],
  ['Arbeitsstunden', [
    ['pflichtstunden', 'Pflichtstunden', 'num', 'Stunden'], ['stundenObergrenze', 'Vergütung bis zu dieser Stundenzahl', 'num', 'Stunden'],
    ['verguetungJeStd', 'Vergütung je Stunde über der Pflicht', 'num', '€'], ['nachzahlungJeStd', 'Nachzahlung je fehlende Stunde', 'num', '€']]],
  ['Pacht und Beiträge (für das Folgejahr)', [
    ['pachtJeQm', 'Pacht je m²', 'num', '€'], ['vereinsflaecheQm', 'Anteil Vereinsfläche', 'num', 'm²'], ['freieGaertenQm', 'Pachtanteil freie Gärten', 'num', 'm²'],
    ['vereinsbeitrag', 'Vereinsbeitrag', 'num', '€'], ['territorialverband', 'Territorialverband', 'num', '€'], ['umlageStandard', 'Umlage (Standard für alle)', 'num', '€']]],
];

function adminSettings() {
  const st = S.state;
  const inputs = {};
  const form = h('div', { class: 'form' });
  for (const [title, fields] of SETTINGS_FORM) {
    form.append(h('div', { class: 'section-title' }, title));
    for (const [key, label, type, hint, wide] of fields) {
      const val = st.settings[key];
      const input = type === 'date' ? h('input', { type: 'date', value: val })
        : type === 'num' ? h('input', { type: 'text', inputmode: 'decimal', value: numIn(val), autocomplete: 'off' })
          : h('input', { type: 'text', value: val, maxlength: 250, autocomplete: 'off' });
      inputs[key] = { input, type };
      const unit = type === 'num' ? hint : null;
      form.append(h('div', { class: 'field' + (wide ? ' wide' : '') }, h('label', null, label),
        unit ? h('div', { class: 'inputunit' }, input, h('span', null, unit)) : input,
        type !== 'num' && hint ? h('span', { class: 'fhint' }, hint) : null));
    }
  }
  const save = async () => {
    const data = { jahr: st.settings.jahr };
    for (const [key, { input, type }] of Object.entries(inputs)) {
      if (type === 'num') {
        const v = parseNum(input.value);
        if (v === null || Number.isNaN(v) || v < 0) { input.classList.add('err'); toast('Bitte in allen Zahlenfeldern eine Zahl ab 0 eintragen.', 'err'); return; }
        input.classList.remove('err');
        data[key] = v;
      } else data[key] = input.value;
    }
    data.einspruchTage = Math.round(data.einspruchTage);
    try { await api('PUT', '/api/admin/settings', data); toast('Einstellungen gespeichert', 'ok'); await reload(); } catch (e) { handleErr(e); }
  };
  return h('div', null,
    h('p', { class: 'hint' }, `Diese Werte gelten für das laufende Abrechnungsjahr ${st.settings.jahr} und für alle Rechnungen. Nach einer Änderung rechnen alle Beträge automatisch neu.`),
    h('div', { class: 'card' }, form, h('div', { class: 'actions' }, h('button', { class: 'btn primary', onclick: save }, 'Speichern'))));
}

// ---- Jahreswechsel

function adminYear() {
  const st = S.state;
  const total = st.paechter.length;
  const done = st.paechter.filter((p) => st.results[p.id] && st.results[p.id].vollstaendig).length;
  return h('div', null,
    h('div', { class: 'card narrow', style: 'max-width:760px' },
      h('h3', { style: 'margin-top:0' }, `Abrechnungsjahr ${st.currentYear} abschließen`),
      h('p', null, 'Am Ende der Abrechnung schließt du das Jahr ab. Dabei passiert Folgendes:'),
      h('ul', { class: 'plain' },
        h('li', null, `Das Jahr ${st.currentYear} wird eingefroren. Preise und Pächterdaten bleiben so gespeichert, dass du alte Rechnungen jederzeit unverändert neu drucken kannst.`),
        h('li', null, `Es startet das Jahr ${st.currentYear + 1}: Die aktuellen Zählerstände werden zu den Vorjahresständen. Stunden, Abschlagszahlungen und weitere Angaben sind wieder leer.`),
        h('li', null, 'Rechnungsdatum und Zahlungsziel rücken um ein Jahr weiter. Prüfe sie danach unter »Preise & Einstellungen«.'),
        h('li', null, 'Vorher wird automatisch eine Sicherung angelegt.')),
      done < total ? h('div', { class: 'banner warn', style: 'margin-top:12px' }, `Achtung: Bei ${total - done} von ${total} Pächtern fehlen noch Angaben.`) : null,
      h('div', { class: 'actions' }, h('button', { class: 'btn primary', onclick: async () => {
        if (!(await confirmBox(`Jahr ${st.currentYear} jetzt abschließen und ${st.currentYear + 1} beginnen? Das kann nicht rückgängig gemacht werden (die Sicherung vorher bleibt aber erhalten).`, 'Jahr abschließen', true))) return;
        try { await api('POST', '/api/admin/jahreswechsel'); toast('Neues Jahr gestartet', 'ok'); S.year = null; await reload(); } catch (e) { handleErr(e); }
      } }, `Jahr ${st.currentYear} abschließen`))));
}

// ---- Import / Export / Sicherung

function adminData() {
  const st = S.state;
  const wrap = h('div');
  const file = h('input', { type: 'file', accept: '.xlsx,.csv,.txt' });
  const previewBox = h('div');

  const drawPreview = () => {
    previewBox.replaceChildren();
    const pv = S.importPreview;
    if (!pv) return;
    const checks = [];
    const tb = h('tbody');
    for (const r of pv.rows) {
      const c = h('input', { type: 'checkbox', checked: true });
      checks.push([c, r]);
      tb.append(h('tr', null, h('td', null, c), h('td', null, h('span', { class: 'badge ' + (r.aktion === 'neu' ? 'neu' : 'warn') }, r.aktion === 'neu' ? 'neu' : 'aktualisieren')),
        h('td', null, r.paechter.mitgliedsnr), h('td', null, r.paechter.name), h('td', null, [r.paechter.strasse, r.paechter.plzOrt].filter(Boolean).join(', ')),
        h('td', { class: 'num' }, r.paechter.gartengroesse ? nfFlex.format(r.paechter.gartengroesse) + ' m²' : ''),
        h('td', null, r.ablesung ? '✓ Zählerstände' : ''), h('td', null, (r.warnungen || []).join('; '))));
    }
    previewBox.append(h('div', null,
      h('h3', null, `Vorschau: ${pv.rows.length} Zeilen gefunden`),
      (pv.warnings || []).length ? h('div', { class: 'banner warn' }, h('ul', { class: 'plain', style: 'margin:0' }, pv.warnings.map((w) => h('li', null, w)))) : null,
      h('p', { class: 'hint' }, 'Erkannte Spalten: ', (pv.rows[0].felder || []).join(', ') || 'nur Mitgliedsnr. und Name',
        '. Bereits vorhandene Pächter (gleiche Mitgliedsnummer) werden aktualisiert, alle anderen neu angelegt. Angaben, die in der Datei fehlen, bleiben unverändert.'),
      h('div', { class: 'tablewrap', style: 'max-height:360px' }, h('table', null,
        h('thead', null, h('tr', null, ['', 'Aktion', 'Mitgl.-Nr.', 'Name', 'Anschrift', 'Größe', 'Zählerstände', 'Hinweis'].map((x) => h('th', null, x)))), tb)),
      h('div', { class: 'actions' }, h('button', { class: 'btn primary', onclick: async () => {
        const rows = checks.filter(([c]) => c.checked).map(([, r]) => r);
        if (!rows.length) { toast('Keine Zeile ausgewählt.', 'err'); return; }
        try {
          const r = await api('POST', '/api/admin/import/apply', { rows });
          toast(`Import fertig: ${r.neu} neu, ${r.aktualisiert} aktualisiert`, 'ok');
          S.importPreview = null;
          await reload();
        } catch (e) { handleErr(e); }
      } }, 'Ausgewählte Zeilen importieren'), h('button', { class: 'btn', onclick: () => { S.importPreview = null; drawPreview(); file.value = ''; } }, 'Abbrechen'))));
  };
  file.addEventListener('change', async () => {
    if (!file.files.length) return;
    const fd = new FormData();
    fd.append('file', file.files[0]);
    try { S.importPreview = await api('POST', '/api/admin/import/preview', fd, true); drawPreview(); } catch (e) { S.importPreview = null; drawPreview(); handleErr(e); }
  });
  drawPreview();

  wrap.append(
    h('div', { class: 'card', style: 'margin-bottom:16px' },
      h('h3', { style: 'margin-top:0' }, 'Pächter aus Excel oder CSV importieren'),
      h('p', { class: 'hint' }, 'Die Datei braucht eine Kopfzeile mit mindestens »Mitgliedsnr.« und »Name«. Weitere Spalten werden automatisch erkannt: Gartennr., Anrede, Straße, PLZ Ort, Versandart, Gartengröße, Umlage abweichend, Wasserzähler-Nr., Stromzähler-Nr., Wasser/Energie Stand Vorjahr und aktuell, Arbeitsstunden, Versicherung, Grundsteuer, Auslagen, Hinweis, Abschlag. Die Excel-Vorlage »Gartenabrechnung.xlsx« (Blatt »Mitglieder«) funktioniert direkt.'),
      file, previewBox),
    h('div', { class: 'card', style: 'margin-bottom:16px' },
      h('h3', { style: 'margin-top:0' }, 'Export'),
      h('p', { class: 'hint' }, `Alle Pächter des Jahres ${st.year} mit Zählerständen und Beträgen als Excel-Datei, zum Beispiel für die Kassenprüfung.`),
      h('a', { class: 'btn', href: `/api/export?year=${st.year}` }, `Jahresübersicht ${st.year} als Excel`)),
    h('div', { class: 'card' },
      h('h3', { style: 'margin-top:0' }, 'Datensicherung'),
      h('p', { class: 'hint' }, 'Das Programm legt bei Änderungen automatisch eine Tagessicherung an (die letzten 60 Tage) und vor dem Jahreswechsel, Löschen und Import zusätzlich eine eigene. Du kannst außerdem den gesamten Datenbestand herunterladen. Zum Wiederherstellen die gewünschte Sicherungsdatei in »gartenabrechnung-daten.json« umbenennen und im Datenordner ersetzen (Programm vorher beenden).'),
      h('div', { class: 'actions', style: 'margin-top:8px' },
        h('a', { class: 'btn', href: '/api/admin/backup' }, 'Gesamten Datenbestand herunterladen'),
        h('button', { class: 'btn', onclick: () => api('POST', '/api/open-folder', { which: 'sicherungen' }).catch(handleErr) }, 'Sicherungsordner öffnen'),
        h('button', { class: 'btn', onclick: () => api('POST', '/api/open-folder', { which: 'daten' }).catch(handleErr) }, 'Datenordner öffnen'))));
  return wrap;
}

// ---- Passwort

function adminSecurity() {
  const st = S.state;
  const old = h('input', { type: 'password', autocomplete: 'current-password' });
  const n1 = h('input', { type: 'password', autocomplete: 'new-password' });
  const n2 = h('input', { type: 'password', autocomplete: 'new-password' });
  const fld = (label, input, hint) => h('div', { class: 'field' }, h('label', null, label), input, hint ? h('span', { class: 'fhint' }, hint) : null);
  const save = async () => {
    if (n1.value !== n2.value) { toast('Die beiden neuen Passwörter sind nicht gleich.', 'err'); return; }
    if (!st.hasPassword && !n1.value) { toast('Bitte ein Passwort eingeben.', 'err'); return; }
    try {
      await api('POST', '/api/admin/password', { old: old.value, new: n1.value });
      toast(n1.value ? 'Passwort gespeichert' : 'Passwortschutz entfernt', 'ok');
      await reload();
    } catch (e) { handleErr(e); }
  };
  return h('div', { class: 'card narrow' },
    h('h3', { style: 'margin-top:0' }, st.hasPassword ? 'Admin-Passwort ändern oder entfernen' : 'Admin-Passwort festlegen'),
    h('p', { class: 'hint' }, 'Das Passwort schützt Pächter, Preise, Jahreswechsel und Import. Die Zählerstände kann weiterhin jeder eintragen, der das Programm öffnet.'),
    h('div', { class: 'form', style: 'grid-template-columns:1fr' },
      st.hasPassword ? fld('Bisheriges Passwort', old) : null,
      fld('Neues Passwort', n1, st.hasPassword ? 'Leer lassen, um den Passwortschutz zu entfernen' : 'Mindestens 8 Zeichen'),
      fld('Neues Passwort wiederholen', n2)),
    h('div', { class: 'actions' },
      h('button', { class: 'btn primary', onclick: save }, 'Speichern'),
      st.hasPassword ? h('button', { class: 'btn', onclick: async () => { try { await api('POST', '/api/admin/logout'); await reload(); } catch (e) { handleErr(e); } } }, 'Abmelden') : null),
    h('p', { class: 'hint', style: 'margin-top:16px' }, 'Passwort vergessen? Das Programm mit dem Zusatz "--reset-admin" starten (in der Eingabeaufforderung: Gartenabrechnung.exe --reset-admin). Dann ist der Passwortschutz entfernt und du kannst ein neues Passwort festlegen.'));
}

// ------------------------------------------------------------------ Start

(async function start() {
  document.body.append(h('div', { id: 'toasts' }));
  try { await load(); render(); } catch (e) {
    document.getElementById('app').replaceChildren(h('div', { class: 'center card' }, h('h2', null, 'Verbindung fehlgeschlagen'), h('p', null, e.message)));
  }
})();
