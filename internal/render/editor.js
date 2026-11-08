// dsheets-editor: the side panel of `dsheets serve --edit`. It holds the
// draft in memory, asks the server to check it when typing pauses, and
// saves it on request. The server applies and re-validates everything it
// writes; this script holds form state only.
(function () {
  var stateEl = document.getElementById("edit-state");
  if (!stateEl) return;
  var S = JSON.parse(stateEl.textContent);

  var SOURCE_KINDS = ["request", "context", "db", "config", "call"];
  var TYPES = ["string", "number", "boolean", "date", "datetime"];
  var OTHERWISE = ["do-not-send", "sender-discretion"];
  var ID_PATTERN = /^[a-z][a-z0-9_]*$/;
  var FLASH_KEY = "dsheets-editor-flash";

  // ---------- Draft state ----------

  function clone(x) { return JSON.parse(JSON.stringify(x)); }

  var orig = {}, rows = {}, kinds = {}, added = {}, deleted = {};
  var order = S.ids.slice();
  var saved = {};
  S.saved_ids.forEach(function (id) { saved[id] = true; });
  S.rows.forEach(function (r) {
    orig[r.id] = clone(r);
    rows[r.id] = clone(r);
    kinds[r.id] = kindOf(r);
  });
  var title = S.title;
  var problems = [], refs = { uses: {}, unknown: {}, cycle: {} };
  // Rows whose uses the server fills from their rule.
  var FILLED_USES = { formula: true, table: true, lookup: true };
  var selected = null; // a row ID, "#title", or null
  var leaving = false, newCount = 0;

  function kindOf(r) {
    if (r.lookup) return "lookup";
    if (r.table) return "table";
    if (r.source !== undefined && r.source !== "") return "input";
    if (r.text) return "text";
    return "formula";
  }

  // norm drops what the file would not write: empty text, empty lists,
  // false flags, and a default that is not set.
  function norm(r) {
    var out = {};
    Object.keys(r).sort().forEach(function (k) {
      var v = r[k];
      if (k === "default") { if (r.has_default) out[k] = v; return; }
      if (v === "" || v === null || v === undefined || v === false) return;
      if (Array.isArray(v) && v.length === 0) return;
      if (k === "sends" || k === "rejects") v = v.map(norm);
      if (k === "table") v = { columns: v.columns, hit_policy: v.hit_policy || undefined, rows: v.rows };
      out[k] = v;
    });
    return out;
  }
  function same(a, b) { return JSON.stringify(norm(a)) === JSON.stringify(norm(b)); }

  function changedIds() {
    return order.filter(function (id) {
      return !deleted[id] && (added[id] || !same(rows[id], orig[id]));
    });
  }
  function changeCount() {
    return changedIds().length + Object.keys(deleted).length + (title !== S.title ? 1 : 0);
  }

  function draft() {
    var d = { file: S.file, token: S.token, rows: {}, deleted: Object.keys(deleted) };
    changedIds().forEach(function (id) { d.rows[id] = norm(rows[id]); });
    if (title !== S.title) d.title = title;
    return d;
  }

  // ---------- DOM helpers ----------

  function h(tag, attrs, kids) {
    var el = document.createElement(tag);
    Object.keys(attrs || {}).forEach(function (k) {
      var v = attrs[k];
      if (v === undefined || v === null || v === false) return;
      if (k === "text") el.textContent = v;
      else if (k.slice(0, 2) === "on") el.addEventListener(k.slice(2), v);
      else if (k === "value") el.value = v;
      else if (k === "checked" || k === "disabled") el[k] = v;
      else el.setAttribute(k, v === true ? "" : v);
    });
    (kids || []).forEach(function (c) {
      if (c === null || c === undefined || c === false) return;
      el.appendChild(typeof c === "string" ? document.createTextNode(c) : c);
    });
    return el;
  }
  var uid = 0;
  function field(label, control, hint) {
    var id = control.id || ("ed-f" + (++uid));
    control.id = id;
    return h("div", { "class": "ed-field" }, [
      h("label", { "for": id, text: label }), control,
      hint ? h("div", { "class": "ed-hint", text: hint }) : null
    ]);
  }
  function input(value, oninput, attrs) {
    var a = attrs || {};
    a.type = a.type || "text";
    a.value = value || "";
    a.oninput = function (e) { oninput(e.target.value, e.target); };
    return h("input", a);
  }
  function textarea(value, oninput, attrs) {
    var a = attrs || {};
    a.rows = a.rows || 2;
    a.oninput = function (e) { oninput(e.target.value, e.target); };
    var el = h("textarea", a);
    el.value = value || "";
    return el;
  }
  function select(options, value, onchange, attrs) {
    var a = attrs || {};
    a.onchange = function (e) { onchange(e.target.value); };
    var el = h("select", a, options.map(function (o) {
      var v = typeof o === "string" ? o : o[0], label = typeof o === "string" ? o : o[1];
      return h("option", { value: v, text: label });
    }));
    el.value = value;
    return el;
  }
  function button(label, onclick, cls, attrs) {
    var a = attrs || {};
    a.type = "button";
    a["class"] = "ed-btn" + (cls ? " " + cls : "");
    a.onclick = onclick;
    a.text = label;
    return h("button", a);
  }

  // ---------- Page chrome: actions, bar, panel ----------

  var head = document.querySelector(".head-top");
  var actions = h("div", { "class": "ed-actions" }, [
    button("Add row", function () { addRow({}); }),
    button("Edit title", function () { open("#title"); })
  ]);
  if (head) head.appendChild(actions);

  var countEl = h("span", { "class": "ed-count" });
  var msgEl = h("span", { "class": "ed-msg", role: "status", "aria-live": "polite" });
  var newRowsEl = h("span", { "class": "ed-new-rows" });
  var staleEl = h("span", { "class": "ed-stale", hidden: true });
  var discardBtn = button("Discard", discard);
  var saveBtn = button("Save", save, "ed-primary");
  var bar = h("div", { "class": "ed-bar" }, [
    h("div", { "class": "ed-bar-left" }, [countEl, newRowsEl, msgEl, staleEl]),
    h("div", { "class": "ed-bar-right" }, [discardBtn, saveBtn])
  ]);
  var detailsEl = h("div", { "class": "ed-details", hidden: true });
  bar.appendChild(detailsEl);
  var panel = h("aside", { "class": "ed-panel", "aria-label": "Edit row", hidden: true });
  document.body.appendChild(panel);
  document.body.appendChild(bar);
  document.body.classList.add("editing");

  var flash = null;
  try { flash = JSON.parse(sessionStorage.getItem(FLASH_KEY) || "null"); sessionStorage.removeItem(FLASH_KEY); } catch (e) { flash = null; }

  // The panel ends where the bar begins, however tall the bar's message is.
  function fitBar() { document.documentElement.style.setProperty("--ed-bar", bar.offsetHeight + "px"); }
  window.addEventListener("resize", fitBar);

  function say(text, cls, details) {
    msgEl.textContent = text || "";
    msgEl.className = "ed-msg" + (cls ? " " + cls : "");
    detailsEl.textContent = "";
    detailsEl.hidden = !details || !details.length;
    (details || []).forEach(function (l) { detailsEl.appendChild(h("div", { text: l })); });
    fitBar();
  }

  function updateBar() {
    var n = changeCount();
    countEl.textContent = n === 0 ? "No unsaved changes" : n === 1 ? "1 unsaved change" : n + " unsaved changes";
    bar.classList.toggle("dirty", n > 0);
    saveBtn.disabled = n === 0;
    discardBtn.disabled = n === 0;
    newRowsEl.textContent = "";
    Object.keys(added).forEach(function (id) {
      newRowsEl.appendChild(button("New: " + id, function () { open(id); }, "ed-chip"));
    });
    markRows();
  }

  function markRows() {
    var changed = {};
    changedIds().forEach(function (id) { changed[id] = true; });
    var byRow = problemsByRow();
    document.querySelectorAll("tr.row").forEach(function (tr) {
      var id = tr.getAttribute("data-id");
      var ps = byRow[id] || [];
      tr.classList.toggle("ed-selected", id === selected);
      tr.classList.toggle("ed-changed", !!changed[id]);
      tr.classList.toggle("ed-deleted", !!deleted[id]);
      tr.classList.toggle("ed-problem", ps.some(function (p) { return p.severity === "error" && !p.new; }));
      tr.classList.toggle("ed-new-problem", ps.some(function (p) { return p.new; }));
      tr.querySelectorAll("table.dt tr[data-rule]").forEach(function (rt) {
        var n = Number(rt.getAttribute("data-rule"));
        rt.classList.toggle("ed-rule-new", ruleProblems(ps, n, true));
        rt.classList.toggle("ed-rule-bad", ruleProblems(ps, n, false));
      });
    });
  }

  // ruleProblems says whether an error names decision-table rule n: one the
  // edit introduces (fresh) or one already there.
  function ruleProblems(ps, n, fresh) {
    return ps.some(function (p) {
      return p.severity === "error" && !!p.new === fresh && (p.rules || []).indexOf(n) >= 0;
    });
  }

  function problemsByRow() {
    var out = {};
    problems.forEach(function (p) { (out[p.row || ""] = out[p.row || ""] || []).push(p); });
    return out;
  }

  // A message from the last save stays until the next edit.
  var sticky = false, refused = false;

  function summarize() {
    if (refused) return;
    if (sticky && !problems.some(function (p) { return p.new; })) return;
    var fresh = problems.filter(function (p) { return p.new; }).length;
    var old = problems.filter(function (p) { return p.severity === "error" && !p.new; }).length;
    if (fresh) return say(fresh === 1 ? "1 error this edit introduces; it cannot be saved yet." : fresh + " errors this edit introduces; it cannot be saved yet.", "danger");
    if (old) return say(old === 1 ? "1 error was already in the sheet; it does not block saving." : old + " errors were already in the sheet; they do not block saving.", "muted");
    say("");
  }

  // ---------- Checking and saving ----------

  var checkTimer = null, checking = 0;
  function changed() {
    sticky = refused = false;
    updateBar();
    clearTimeout(checkTimer);
    checkTimer = setTimeout(check, 400);
  }

  function post(path, body) {
    return fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      credentials: "same-origin"
    }).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (j) {
        if (!res.ok) throw new Error(j.error || res.status + " " + res.statusText);
        return j;
      });
    });
  }

  function check() {
    var seq = ++checking, clean = changeCount() === 0;
    return post("/edit/check", draft()).then(function (res) {
      if (seq !== checking) return;
      if (res.conflict && res.conflict.length) return conflict(res.conflict);
      showDraft(res.preview);
      takeCases(res.preview, clean);
      problems = res.problems || [];
      refs = { uses: res.uses || {}, unknown: res.unknown || {}, cycle: res.cycle || {} };
      var again = false;
      Object.keys(refs.uses).forEach(function (id) {
        var r = rows[id];
        if (!r || !FILLED_USES[kinds[id]]) return;
        if (JSON.stringify(r.uses || []) !== JSON.stringify(refs.uses[id])) {
          r.uses = refs.uses[id].slice();
          again = true;
        }
      });
      updateBar();
      summarize();
      refreshPanelStatus();
      if (again) changed();
    }).catch(function (err) {
      if (seq === checking) say("Checking failed: " + err.message, "danger");
    });
  }

  // cases are what saving would write to cases.toml, with every row's
  // result in each. baseCases are the same for the file as loaded, by
  // input (an edit can rename a case), so the panel can say what an edit
  // changes. When the draft's cases cannot be
  // worked out, the last ones stay and casesNote says why.
  var cases = null, unreached = [], casesNote = "", baseCases = null;
  function takeCases(pv, clean) {
    if (!pv) return;
    casesNote = pv.stale || pv.cases_stale || "";
    if (casesNote) return;
    cases = pv.cases || [];
    unreached = pv.unreached || [];
    if (clean) {
      baseCases = {};
      cases.forEach(function (c) { baseCases[c.key] = c; });
    }
  }

  // ---------- The sheet, drawn from the draft ----------

  // showDraft puts the sheet as the draft would render it in place of the
  // one on the page, so an edit shows in the grid, rule and send columns
  // before it is saved. The server draws it with the same renderer; nothing
  // is written. When the draft cannot be drawn (it does not parse, or makes
  // a cycle), the page keeps the last sheet it drew and says so.
  var drawn = false; // the page shows a draft, not the file
  function showDraft(pv) {
    if (!pv) return;
    staleEl.textContent = pv.stale ? "The sheet shows your last edit that could be drawn: " + pv.stale + "." : "";
    staleEl.hidden = !pv.stale;
    if (!pv.page || (!drawn && changeCount() === 0)) return fitBar();
    var doc = new DOMParser().parseFromString(pv.page, "text/html");
    var oldT = document.querySelector("table.sheet"), newT = doc.querySelector("table.sheet");
    if (!oldT || !newT) return;
    var ae = document.activeElement, fr = ae && ae.closest ? ae.closest("table.sheet tr.row") : null;
    var focusId = fr ? fr.getAttribute("data-id") : null;
    var top = oldT.scrollTop, left = oldT.scrollLeft;
    newT = document.adoptNode(newT);
    oldT.replaceWith(newT);
    newT.scrollTop = top;
    newT.scrollLeft = left;
    var oldN = document.querySelector("section.notes"), newN = doc.querySelector("section.notes");
    if (newN) newN = document.adoptNode(newN);
    if (oldN && newN) oldN.replaceWith(newN);
    else if (oldN) oldN.remove();
    else if (newN) newT.insertAdjacentElement("afterend", newN);
    var h1 = document.querySelector("header.head h1"), newH1 = doc.querySelector("header.head h1");
    if (h1 && newH1) h1.textContent = newH1.textContent;
    drawn = changeCount() > 0;
    decorate();
    markRows();
    if (focusId) { var f = sheetRow(focusId); if (f) f.focus(); }
    if (headEl && selected && selected !== "#title" && !added[selected]) {
      var num = sheetRow(selected);
      headEl.textContent = "Row " + (num ? num.querySelector("td.num").textContent : "");
    }
    fitBar();
  }

  function sheetRow(id) {
    return document.querySelector('table.sheet tr.row[data-id="' + (window.CSS && CSS.escape ? CSS.escape(id) : id) + '"]');
  }

  function save() {
    clearTimeout(checkTimer);
    saveBtn.disabled = true;
    say("Saving…");
    post("/edit/save", draft()).then(function (res) {
      if (res.conflict && res.conflict.length) return conflict(res.conflict);
      problems = res.problems || [];
      if (res.unchanged) { updateBar(); return say("Nothing to save: the draft matches the file."); }
      if (!res.saved) {
        updateBar();
        refreshPanelStatus();
        check(); // the save cancelled the pending check; refresh uses and unknown names
        var fresh = problems.filter(function (p) { return p.new; }).length;
        refused = true;
        return say("Not saved: this edit introduces " + (fresh === 1 ? "1 error" : fresh + " errors") + ". Fix the rows marked in red.", "danger");
      }
      var text = res.version_raised ? "Saved. Version raised to " + res.version + "." : "Saved.";
      try { sessionStorage.setItem(FLASH_KEY, JSON.stringify({ text: text, details: res.cases || [] })); } catch (e) { /* the message is lost, the save is not */ }
      leaving = true;
      location.reload();
    }).catch(function (err) {
      updateBar();
      say("Saving failed: " + err.message, "danger");
    });
  }

  function conflict(paths) {
    updateBar();
    say(paths.join(", ") + " changed on disk since this page loaded. Reload to get the new version; your unsaved changes here will be lost.", "danger");
    msgEl.appendChild(document.createTextNode(" "));
    msgEl.appendChild(button("Reload", function () { leaving = true; location.reload(); }));
  }

  function discard() {
    if (!window.confirm("Discard " + changeCount() + " unsaved change(s)?")) return;
    leaving = true;
    location.reload();
  }

  window.addEventListener("beforeunload", function (e) {
    if (leaving || changeCount() === 0) return;
    e.preventDefault();
    e.returnValue = "";
  });

  // ---------- Rows ----------

  function rowName(id) {
    var r = rows[id];
    return r && r.name ? r.name : id;
  }

  function addRow(r) {
    var id = r.id;
    if (!id) {
      do { id = "new_row_" + (++newCount); } while (rows[id]);
    }
    rows[id] = { id: id, name: r.name || "", source: r.source === undefined ? "request." : r.source, type: r.type || "" };
    kinds[id] = "input";
    added[id] = true;
    order.push(id);
    open(id);
    changed();
  }

  function renameNew(oldId, newId) {
    rows[newId] = rows[oldId]; rows[newId].id = newId; delete rows[oldId];
    kinds[newId] = kinds[oldId]; delete kinds[oldId];
    added[newId] = true; delete added[oldId];
    order[order.indexOf(oldId)] = newId;
    selected = newId;
  }

  var KIND_NAMES = { input: "input", formula: "formula", table: "decision table", lookup: "lookup", text: "text rule" };
  function aKind(kind) { return (/^[aeiou]/.test(KIND_NAMES[kind]) ? "an " : "a ") + KIND_NAMES[kind]; }

  // hasRule says whether switching away from kind would lose something
  // the person wrote.
  function hasRule(r, kind) {
    switch (kind) {
      case "input": return !!(r.source && r.source !== "request.") || !!r.type || !!r.usage || !!r.when || !!r.has_default;
      case "formula": return !!(r.formula && r.formula.trim());
      case "text": return !!(r.text && r.text.trim());
      case "lookup": return !!r.lookup;
      case "table": return !!r.table && (r.table.columns.length > 1 || r.table.rows.some(function (row) {
        return row.some(function (c) { return c !== "-" && c !== ""; });
      }));
    }
    return false;
  }

  function setKind(id, kind) {
    var r = rows[id], had = kinds[id];
    if (had === kind) return;
    if (hasRule(r, had) && !window.confirm("Replace the " + KIND_NAMES[had] + " with " + aKind(kind) + "? The current rule will be removed.")) { render(); return; }
    ["source", "type", "usage", "when", "has_default", "default", "formula", "text", "table", "lookup"].forEach(function (k) { delete r[k]; });
    if (kind === "input") { r.source = "request."; delete r.uses; }
    if (kind === "formula") r.formula = "";
    if (kind === "text") r.text = "";
    if (kind === "table") r.table = { columns: ["=>"], rows: [[""]] };
    kinds[id] = kind;
    render();
    changed();
  }

  // ---------- Panel ----------

  function open(id) {
    selected = id;
    panel.hidden = false;
    document.body.classList.add("panel-open");
    render();
    markRows();
    var first = panel.querySelector("input, textarea, select");
    if (first) first.focus();
  }

  // show opens a row the panel points to (a Uses ID) and brings it into
  // view on the sheet.
  function show(id) {
    open(id);
    var tr = sheetRow(id);
    if (tr) reveal(tr);
  }

  // reveal centres a row in the part of the screen nothing covers: below the
  // sticky column header, above the bar, and above the panel where the panel
  // is a bottom sheet (narrow screens). Below 900px the table scrolls in its
  // own box, so that box moves first and the page takes what is left.
  function reveal(tr) {
    var table = tr.closest("table");
    var head = table.querySelector("thead th"); // the cells are what stick
    var top = Math.max(0, head ? head.getBoundingClientRect().bottom : 0);
    var bottom = window.innerHeight - bar.offsetHeight;
    var p = panel.getBoundingClientRect();
    if (!panel.hidden && p.left <= 0 && p.top > 0) bottom = Math.min(bottom, p.top);
    var delta = function () {
      var r = tr.getBoundingClientRect();
      if (r.height >= bottom - top) return r.top - top;
      return (r.top + r.bottom) / 2 - (top + bottom) / 2;
    };
    if (table.scrollHeight > table.clientHeight) table.scrollTop += delta();
    window.scrollBy(0, delta());
  }

  function close() {
    selected = null;
    panel.hidden = true;
    document.body.classList.remove("panel-open", "panel-wide");
    markRows();
  }

  // Rows are found through the document, as the sheet is redrawn from the
  // draft (see showDraft); decorate marks the ones the panel can open.
  function decorate() {
    document.querySelectorAll("table.sheet tr.row").forEach(function (tr) {
      if (!rows[tr.getAttribute("data-id")]) return;
      tr.classList.add("ed-selectable");
      tr.setAttribute("tabindex", "0");
    });
  }
  function rowFrom(e) {
    var tr = e.target.closest ? e.target.closest("table.sheet tr.ed-selectable") : null;
    return tr && rows[tr.getAttribute("data-id")] ? tr.getAttribute("data-id") : null;
  }
  document.addEventListener("click", function (e) { var id = rowFrom(e); if (id) open(id); });
  document.addEventListener("keydown", function (e) { var id = e.key === "Enter" && rowFrom(e); if (id) open(id); });
  decorate();
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && !panel.hidden && !document.querySelector(".ed-suggest:not([hidden])")) close();
  });

  var statusEl = null, usesEl = null, headEl = null, casesEl = null, gridEl = null;

  function render() {
    panel.textContent = "";
    statusEl = usesEl = headEl = casesEl = gridEl = null;
    var wide = selected !== "#title" && kinds[selected] === "table" && !deleted[selected];
    panel.classList.toggle("ed-wide", wide);
    document.body.classList.toggle("panel-wide", wide);
    if (selected === "#title") return renderTitle();
    var id = selected, r = rows[id];
    if (!r) return close();
    var num = document.querySelector('tr.row[data-id="' + id + '"] td.num');
    headEl = h("h2", { text: (added[id] ? "New row" : "Row " + (num ? num.textContent : "")) });
    panel.appendChild(h("div", { "class": "ed-panel-head" }, [
      headEl,
      button("Close", close, "ed-quiet", { "aria-label": "Close the panel" })
    ]));
    if (deleted[id]) {
      panel.appendChild(h("p", { "class": "ed-note", text: "This row will be deleted when you save." }));
      panel.appendChild(button("Keep this row", function () { delete deleted[id]; render(); changed(); }));
      return;
    }
    var kind = kinds[id];
    if (kind === "lookup") {
      panel.appendChild(h("p", { "class": "ed-note", text: "Editing lookups comes later. This row is shown read-only." }));
      panel.appendChild(h("dl", { "class": "ed-readonly" }, [
        h("dt", { text: "Name" }), h("dd", { text: r.name || "" }),
        h("dt", { text: "ID" }), h("dd", {}, [h("code", { text: id })])
      ]));
      statusEl = h("div", { "class": "ed-status" });
      panel.appendChild(statusEl);
      casesEl = h("section", { "class": "ed-list ed-cases" });
      panel.appendChild(casesEl);
      refreshPanelStatus();
      return;
    }

    var s = function (k) { return function (v) { r[k] = v; changed(); }; };
    panel.appendChild(field("Name", input(r.name, s("name"))));

    var idInput = input(id, function (v, el) {
      var err = !ID_PATTERN.test(v) ? "Use lower-case letters, digits and _, starting with a letter."
        : v !== selected && rows[v] ? "Another row already has this ID." : "";
      el.setCustomValidity(err);
      idHint.textContent = err || "You can change it until the row is saved.";
      idHint.classList.toggle("danger", !!err);
      if (!err && v !== selected) { renameNew(selected, v); changed(); }
    }, { "class": "mono", spellcheck: "false", autocomplete: "off", disabled: !!saved[id] });
    var idHint = h("div", { "class": "ed-hint", text: saved[id] ? "Row IDs are fixed once saved." : "You can change it until the row is saved." });
    panel.appendChild(h("div", { "class": "ed-field" }, [h("label", { "for": "ed-id", text: "ID" }), idInput, idHint]));
    idInput.id = "ed-id";
    panel.appendChild(field("Description", textarea(r.description, s("description"))));

    var group = h("div", { "class": "ed-seg", role: "radiogroup", "aria-label": "Rule" });
    [["input", "Input"], ["formula", "Formula"], ["table", "Table"], ["text", "Text"]].forEach(function (k) {
      var rid = "ed-kind-" + k[0];
      group.appendChild(h("input", { type: "radio", name: "ed-kind", id: rid, value: k[0], checked: kind === k[0], onchange: function () { setKind(id, k[0]); } }));
      group.appendChild(h("label", { "for": rid, text: k[1] }));
    });
    panel.appendChild(h("div", { "class": "ed-field" }, [h("div", { "class": "ed-label", text: "Rule" }), group]));

    if (kind === "input") renderInput(id, r);
    if (kind === "formula") renderFormula(id, r);
    if (kind === "table") renderTable(id, r);
    if (kind === "text") panel.appendChild(field("Rule in words", textarea(r.text, s("text"), { rows: 3 }), "Shown as an open item until someone writes the rule."));
    casesEl = h("section", { "class": "ed-list ed-cases" });
    panel.appendChild(casesEl);

    renderSends(id, r);
    renderRejects(id, r);
    renderQuestions(id, r);

    statusEl = h("div", { "class": "ed-status" });
    panel.appendChild(statusEl);
    panel.appendChild(h("div", { "class": "ed-panel-foot" }, [
      button(added[id] ? "Remove new row" : "Delete row", function () {
        if (added[id]) {
          delete rows[id]; delete kinds[id]; delete added[id];
          order.splice(order.indexOf(id), 1);
          close();
        } else {
          deleted[id] = true;
          render();
        }
        changed();
      }, "ed-danger")
    ]));
    refreshPanelStatus();
  }

  function renderTitle() {
    panel.appendChild(h("div", { "class": "ed-panel-head" }, [
      h("h2", { text: "Sheet title" }),
      button("Close", close, "ed-quiet", { "aria-label": "Close the panel" })
    ]));
    panel.appendChild(field("Title", input(title, function (v) { title = v; changed(); })));
  }

  function renderInput(id, r) {
    var src = r.source || "request.";
    var dot = src.indexOf(".");
    var kind = dot < 0 ? src : src.slice(0, dot), path = dot < 0 ? "" : src.slice(dot + 1);
    var pathInput = input(path, function (v) { r.source = kindSel.value + "." + v; changed(); }, { "class": "mono", spellcheck: "false", placeholder: "customer.signupDate" });
    var kindSel = select(SOURCE_KINDS.map(function (k) { return [k, k + "."]; }), kind, function (v) { r.source = v + "." + pathInput.value; changed(); }, { "aria-label": "Source kind", "class": "mono" });
    panel.appendChild(h("div", { "class": "ed-field" }, [
      h("label", { "for": "ed-src", text: "Source" }),
      h("div", { "class": "ed-row" }, [kindSel, pathInput])
    ]));
    pathInput.id = "ed-src";
    panel.appendChild(h("div", { "class": "ed-row" }, [
      field("Type", select([["", "—"]].concat(TYPES), r.type || "", function (v) { r.type = v; changed(); })),
      field("Usage", select([["", "R (default)"], ["R", "R, always required"], ["S", "S, situational"]], r.usage || "", function (v) { r.usage = v; render(); changed(); }))
    ]));
    if (r.usage === "S") panel.appendChild(field("Required when", input(r.when, function (v) { r.when = v; changed(); }, { "class": "mono", spellcheck: "false" })));
    var defVal = input(r.has_default ? String(r["default"]) : "", function (v) { r["default"] = typed(v, r.type); changed(); }, { disabled: !r.has_default, "aria-label": "Default value" });
    panel.appendChild(h("div", { "class": "ed-field" }, [
      h("label", { "class": "ed-check" }, [
        h("input", { type: "checkbox", checked: !!r.has_default, onchange: function (e) {
          r.has_default = e.target.checked;
          if (r.has_default) r["default"] = typed(defVal.value, r.type); else delete r["default"];
          defVal.disabled = !r.has_default;
          changed();
        } }), "Default when missing"]),
      defVal
    ]));
  }

  function typed(v, type) {
    if (type === "number" && v.trim() !== "" && !isNaN(Number(v))) return Number(v);
    if (type === "boolean" && (v === "true" || v === "false")) return v === "true";
    return v;
  }

  // ---------- Formula with suggestions ----------

  // rowCands are the rows an expression on row id may name: every row
  // left on the sheet, with the ones that would make a cycle blocked. An
  // input's reject may name only the input and other inputs (D-9).
  function rowCands(id, inputReject) {
    var cycle = {};
    (refs.cycle[id] || [id]).forEach(function (c) { cycle[c] = true; });
    return order.filter(function (x) {
      return !deleted[x] && (!inputReject || x === id || kinds[x] === "input");
    }).map(function (x) {
      return { name: x, label: x, note: rowName(x), blocked: !inputReject && !!cycle[x] };
    });
  }

  // suggester offers row IDs and functions while typing an expression in
  // el, and returns the list to place after it. cands gives the rows to
  // offer; onpick takes the text after a suggestion is picked.
  var suggestN = 0;
  function suggester(el, owner, cands, onpick) {
    var n = ++suggestN;
    var list = h("ul", { "class": "ed-suggest", role: "listbox", hidden: true, id: "ed-suggest-" + n });
    el.setAttribute("aria-controls", list.id);
    el.setAttribute("aria-autocomplete", "list");
    el.setAttribute("autocomplete", "off");
    var active = -1, items = [];

    function prefixAt() {
      var m = /[A-Za-z_][A-Za-z0-9_]*$/.exec(el.value.slice(0, el.selectionStart));
      return m ? m[0] : "";
    }
    function suggest() {
      var p = prefixAt().toLowerCase();
      list.textContent = "";
      items = [];
      active = -1;
      if (!p) { list.hidden = true; return; }
      var all = cands().concat((S.functions || []).map(function (f) {
        return { name: f.name, label: f.signature, note: f.description, fn: true };
      }));
      var starts = all.filter(function (c) { return c.name.toLowerCase().indexOf(p) === 0; });
      var has = all.filter(function (c) { return c.name.toLowerCase().indexOf(p) > 0; });
      items = starts.concat(has).slice(0, 8);
      if (!items.length || (items.length === 1 && items[0].name.toLowerCase() === p)) { list.hidden = true; return; }
      items.forEach(function (c, i) {
        list.appendChild(h("li", {
          role: "option", id: "ed-sg-" + n + "-" + i, "aria-disabled": c.blocked ? "true" : null,
          "class": c.blocked ? "blocked" : null,
          title: c.blocked ? "Using this row would make a cycle: it depends on " + rowName(owner) + "." : null,
          onmousedown: function (e) { e.preventDefault(); pick(i); }
        }, [h("code", { text: c.label }), h("span", { text: c.note || "" })]));
      });
      list.hidden = false;
    }
    function move(d) {
      if (!items.length) return;
      var i = active;
      do { i = (i + d + items.length) % items.length; } while (items[i].blocked && i !== active && items.some(function (c) { return !c.blocked; }));
      active = i;
      Array.prototype.forEach.call(list.children, function (li, j) { li.classList.toggle("active", j === active); li.setAttribute("aria-selected", j === active); });
      el.setAttribute("aria-activedescendant", "ed-sg-" + n + "-" + active);
    }
    function pick(i) {
      var c = items[i];
      if (!c || c.blocked) return;
      var before = el.value.slice(0, el.selectionStart), after = el.value.slice(el.selectionStart);
      var p = prefixAt();
      var ins = c.fn ? c.name + "(" : c.name;
      el.value = before.slice(0, before.length - p.length) + ins + after;
      var at = before.length - p.length + ins.length;
      el.setSelectionRange(at, at);
      list.hidden = true;
      onpick(el.value);
    }
    el.addEventListener("input", suggest);
    el.addEventListener("keydown", function (e) {
      if (list.hidden) return;
      if (e.key === "ArrowDown") { e.preventDefault(); move(1); }
      else if (e.key === "ArrowUp") { e.preventDefault(); move(-1); }
      else if ((e.key === "Enter" || e.key === "Tab") && active >= 0) { e.preventDefault(); pick(active); }
      else if (e.key === "Escape") { e.preventDefault(); list.hidden = true; }
    });
    el.addEventListener("blur", function () { list.hidden = true; });
    return list;
  }

  // exprField is a one-line expression input with suggestions.
  function exprField(label, value, onchange, cands, owner, hint) {
    var el = input(value, function (v) { onchange(v); }, { "class": "mono", spellcheck: "false" });
    var list = suggester(el, owner, cands, onchange);
    var f = field(label, el, hint);
    f.classList.add("ed-formula");
    f.insertBefore(list, el.nextSibling);
    return f;
  }

  // ---------- Formula ----------

  function renderFormula(id, r) {
    var set = function (v) { r.formula = v; changed(); };
    var ta = textarea(r.formula, set, { "class": "mono", rows: 3, spellcheck: "false" });
    var list = suggester(ta, id, function () { return rowCands(id, false); }, set);
    panel.appendChild(h("div", { "class": "ed-field ed-formula" }, [h("label", { "for": "ed-formula", text: "Formula" }), ta, list]));
    ta.id = "ed-formula";
    usesEl = h("div", { "class": "ed-uses" });
    panel.appendChild(usesEl);
  }

  // ---------- Decision table ----------

  function cellText(v) { return v === null || v === undefined ? "" : String(v); }

  // cellValue reads a cell as typed: true and false are booleans, numbers
  // are numbers, and anything else (a condition such as ">= 2", "-" for
  // any value, a name) is text. A cell whose text is unchanged keeps the
  // value it had, so 0.10 stays as the file writes it.
  function cellValue(text, was) {
    if (was !== undefined && cellText(was) === text) return was;
    var t = text.trim();
    if (t === "true" || t === "false") return t === "true";
    if (/^-?(\d+(\.\d*)?|\.\d+)$/.test(t)) return Number(t);
    return t;
  }

  function renderTable(id, r) {
    var tb = r.table;
    var n = tb.columns.length - 1; // input columns; the last is "=>"
    var origPolicy = orig[id] && orig[id].table ? orig[id].table.hit_policy || "" : "";
    panel.appendChild(field("Hit policy", select([["", "One rule per case (unique)"], ["first", "First matching rule wins"]],
      tb.hit_policy === "first" ? "first" : "", function (v) {
        // A file that spells out "unique" keeps it.
        tb.hit_policy = v === "first" ? "first" : origPolicy === "unique" ? "unique" : "";
        render();
        changed();
      }), tb.hit_policy === "first" ? "Rules are tried top to bottom; order matters." : "Rules must not overlap: each case matches exactly one."));

    var redraw = function () { render(); changed(); };
    var cycle = {};
    (refs.cycle[id] || [id]).forEach(function (c) { cycle[c] = true; });
    var pickable = order.filter(function (x) { return !deleted[x] && x !== id; });

    var head = [h("th", { "class": "ed-dt-n", scope: "col", text: "#" })];
    tb.columns.slice(0, n).forEach(function (col, j) {
      var opts = pickable.map(function (x) { return [x, x]; });
      if (pickable.indexOf(col) < 0) opts.unshift([col, col ? col + " (not a row)" : "—"]);
      var sel = select(opts, col, function (v) { tb.columns[j] = v; redraw(); }, { "class": "mono", "aria-label": "Column " + (j + 1) + ": input row" });
      Array.prototype.forEach.call(sel.options, function (o) {
        if (cycle[o.value] && o.value !== col) { o.disabled = true; o.textContent += " (cycle)"; }
      });
      head.push(h("th", { scope: "col" }, [sel, h("div", { "class": "ed-dt-tools" }, [
        button("←", function () { moveColumn(tb, j, j - 1); redraw(); }, "ed-quiet", { disabled: j === 0, "aria-label": "Move column " + col + " left", title: "Move left" }),
        button("→", function () { moveColumn(tb, j, j + 1); redraw(); }, "ed-quiet", { disabled: j === n - 1, "aria-label": "Move column " + col + " right", title: "Move right" }),
        button("×", function () { removeColumn(tb, j); redraw(); }, "ed-quiet ed-danger", { "aria-label": "Remove column " + col, title: "Remove column" })
      ])]));
    });
    head.push(h("th", { "class": "ed-dt-res", scope: "col", text: "Result" }));
    head.push(h("th", { "class": "ed-dt-act" }, [h("span", { "class": "ed-sr", text: "Rule actions" })]));

    var body = tb.rows.map(function (row, i) {
      var cells = [h("td", { "class": "ed-dt-n", text: String(i + 1) })];
      for (var j = 0; j <= n; j++) {
        (function (j) {
          var was = row[j];
          var name = j < n ? tb.columns[j] : "result";
          cells.push(h("td", { "class": j === n ? "ed-dt-res" : null }, [input(cellText(was), function (v) {
            while (row.length <= n) row.push(row.length === n ? "" : "-");
            row[j] = cellValue(v, was);
            changed();
          }, { "class": "mono", spellcheck: "false", autocomplete: "off", "aria-label": "Rule " + (i + 1) + ", " + name, placeholder: j < n ? "-" : "" })]));
        })(j);
      }
      cells.push(h("td", { "class": "ed-dt-act" }, [
        button("↑", function () { tb.rows.splice(i - 1, 0, tb.rows.splice(i, 1)[0]); redraw(); }, "ed-quiet", { disabled: i === 0, "aria-label": "Move rule " + (i + 1) + " up", title: "Move up" }),
        button("↓", function () { tb.rows.splice(i + 1, 0, tb.rows.splice(i, 1)[0]); redraw(); }, "ed-quiet", { disabled: i === tb.rows.length - 1, "aria-label": "Move rule " + (i + 1) + " down", title: "Move down" }),
        button("×", function () { tb.rows.splice(i, 1); redraw(); }, "ed-quiet ed-danger", { "aria-label": "Remove rule " + (i + 1), title: "Remove rule" })
      ]));
      return h("tr", { "data-rule": String(i + 1) }, cells);
    });

    gridEl = h("table", { "class": "ed-dt" }, [h("thead", {}, [h("tr", {}, head)]), h("tbody", {}, body)]);
    var unused = pickable.filter(function (x) { return tb.columns.indexOf(x) < 0 && !cycle[x]; });
    var addCol = select([["", "Add column…"]].concat(unused.map(function (x) { return [x, x + " · " + rowName(x)]; })), "", function (v) {
      if (!v) return;
      tb.columns.splice(n, 0, v);
      tb.rows.forEach(function (row) { while (row.length < n) row.push("-"); row.splice(n, 0, "-"); });
      redraw();
    }, { "aria-label": "Add a column for an input row", "class": "ed-dt-addcol" });
    panel.appendChild(h("div", { "class": "ed-field" }, [
      h("div", { "class": "ed-label", text: "Rules" }),
      h("div", { "class": "ed-dt-wrap" }, [gridEl]),
      h("div", { "class": "ed-row ed-dt-foot" }, [
        button("Add rule", function () {
          var row = [];
          for (var j = 0; j < n; j++) row.push("-");
          row.push("");
          tb.rows.push(row);
          redraw();
          var cellsNow = gridEl.querySelectorAll("tbody tr:last-child input");
          if (cellsNow.length) cellsNow[0].focus();
        }),
        addCol
      ]),
      h("div", { "class": "ed-hint", text: "A cell is a value (true, 46, Gold), a comparison (< 18, >= 2, != High), or - for any value." })
    ]));
    usesEl = h("div", { "class": "ed-uses" });
    panel.appendChild(usesEl);
  }

  function moveColumn(tb, from, to) {
    tb.columns.splice(to, 0, tb.columns.splice(from, 1)[0]);
    tb.rows.forEach(function (row) { row.splice(to, 0, row.splice(from, 1)[0]); });
  }
  function removeColumn(tb, j) {
    tb.columns.splice(j, 1);
    tb.rows.forEach(function (row) { row.splice(j, 1); });
  }

  // refreshPanelStatus redraws the parts of the panel the server's check
  // fills: uses, unknown names, and this row's problems.
  function refreshPanelStatus() {
    var id = selected;
    if (!id || id === "#title" || !rows[id]) return;
    if (usesEl) {
      usesEl.textContent = "";
      var uses = rows[id].uses || [];
      usesEl.appendChild(h("span", { "class": "ed-label", text: "Uses" }));
      if (!uses.length) usesEl.appendChild(h("span", { "class": "ed-hint", text: kinds[id] === "table" ? "Filled from the columns." : "Filled from the formula as you type." }));
      uses.forEach(function (u) {
        usesEl.appendChild(rows[u]
          ? h("button", { type: "button", "class": "ed-use", title: "Open " + rowName(u), onclick: function () { show(u); } }, [h("code", { text: u })])
          : h("span", { "class": "ed-use-plain" }, [h("code", { text: u })]));
      });
      (refs.unknown[id] || []).forEach(function (name) {
        usesEl.appendChild(h("div", { "class": "ed-unknown" }, [
          h("code", { text: name }), " is not a row. ",
          ID_PATTERN.test(name) && !rows[name] ? button("Add it as a request input", function () { addRow({ id: name, name: "", source: "request." }); }) : null
        ]));
      });
    }
    var ps = problemsByRow()[id] || [];
    if (gridEl) {
      gridEl.querySelectorAll("tbody tr").forEach(function (tr) {
        var n = Number(tr.getAttribute("data-rule"));
        tr.classList.toggle("ed-rule-new", ruleProblems(ps, n, true));
        tr.classList.toggle("ed-rule-bad", ruleProblems(ps, n, false));
      });
    }
    refreshCases(id);
    if (statusEl) {
      statusEl.textContent = "";
      ps.forEach(function (p) {
        statusEl.appendChild(h("div", { "class": "ed-problem " + (p.new ? "new" : p.severity) }, [
          h("span", { "class": "ed-tag", text: p.new ? "New error" : p.severity === "error" ? "Existing error" : "Warning" }),
          " ", p.message
        ]));
      });
    }
  }

  // ---------- Cases ----------

  function caseText(cr) {
    if (!cr) return "—";
    if (cr.none) return cr.note || "no value";
    return cr.value + (cr.rule ? " (rule " + cr.rule + ")" : "") + (cr.note ? ", " + cr.note : "");
  }

  // unreachedText says which part of row id no case reaches. A table rule
  // is named by its number when the panel's table has it.
  function unreachedText(id, u) {
    if (u.kind === "table") {
      var tb = rows[id].table, n = 0;
      (tb ? tb.rows : []).forEach(function (row, i) {
        if (!n && "[" + row.slice(0, -1).map(cellText).join(",") + "]" === u.target) n = i + 1;
      });
      return "No case reaches " + (n ? "rule " + n : "the rule " + u.target) +
        ". Usually a later row cannot use its result, such as a lookup with no entry for it.";
    }
    if (u.kind === "reject") return "No case triggers this reject (" + u.target.replace(/^reject:/, "") + ").";
    return "No case reaches " + u.target + " (" + u.kind + ").";
  }

  // refreshCases lists every case saving would write, with the values of
  // the rows this row uses and its result, marking results the edit
  // changes.
  function refreshCases(id) {
    if (!casesEl) return;
    casesEl.textContent = "";
    casesEl.appendChild(h("div", { "class": "ed-list-head" }, [h("h3", { text: cases ? "Cases (" + cases.length + ")" : "Cases" })]));
    if (casesNote) {
      casesEl.appendChild(h("p", { "class": "ed-hint ed-warn", text: (cases ? "These are from your last edit that could be worked out: " : "The cases cannot be worked out: ") + casesNote }));
    }
    if (!cases) {
      if (!casesNote) casesEl.appendChild(h("p", { "class": "ed-hint", text: "Working out the cases…" }));
      return;
    }
    if (!cases.length) {
      casesEl.appendChild(h("p", { "class": "ed-hint ed-warn", text: "No case can be worked out: every input the generator tried stops at an error. Fix the errors on the sheet first." }));
      return;
    }
    casesEl.appendChild(h("p", { "class": "ed-hint", text: "The cases saving writes to cases.toml, and this row's result in each." }));
    var uses = (rows[id].uses || []).filter(function (u) { return !deleted[u]; });
    var changedN = 0;
    var items = cases.map(function (c) {
      var cr = c.rows[id];
      var was = baseCases && baseCases[c.key] ? baseCases[c.key].rows[id] : undefined;
      // A result changes when its value does, or why there is none; not
      // when another rule gives the same value.
      var diff = !!(cr && was && (!was.none !== !cr.none || (was.none ? was.note : was.value) !== (cr.none ? cr.note : cr.value)));
      if (diff) changedN++;
      var ins = uses.map(function (u) {
        var x = c.rows[u];
        return h("span", {}, [h("code", { text: u }), " " + (x ? (x.none ? "—" : x.value) : "—")]);
      });
      var inEl = h("span", { "class": "ed-case-in" });
      ins.forEach(function (x, i) { if (i) inEl.appendChild(document.createTextNode(" · ")); inEl.appendChild(x); });
      return h("li", { "class": "ed-case" + (diff ? " changed" : "") + (cr && cr.none ? " none" : "") }, [
        h("code", { "class": "ed-case-id", text: c.id }),
        uses.length ? inEl : null,
        h("span", { "class": "ed-case-out" }, ["→ ", h("b", { text: caseText(cr) })]),
        diff ? h("span", { "class": "ed-case-was", text: "was " + caseText(was) }) : null
      ]);
    });
    if (changedN) casesEl.appendChild(h("p", { "class": "ed-hint ed-case-sum", text: changedN === 1 ? "1 result changes with your edits." : changedN + " results change with your edits." }));
    casesEl.appendChild(h("ul", { "class": "ed-case-list" }, items));
    var now = {};
    cases.forEach(function (c) { now[c.key] = true; });
    unreached.filter(function (u) { return u.row === id; }).forEach(function (u) {
      casesEl.appendChild(h("p", { "class": "ed-hint ed-warn", text: unreachedText(id, u) }));
    });
    var gone = Object.keys(baseCases || {}).filter(function (k) { return !now[k]; }).map(function (k) { return baseCases[k].id; });
    if (gone.length) casesEl.appendChild(h("p", { "class": "ed-hint ed-warn", text: "No longer generated: " + gone.join(", ") + "." }));
  }

  // ---------- Sends, rejects, open questions ----------

  function listSection(titleText, addLabel, onadd, kids) {
    return h("section", { "class": "ed-list" }, [
      h("div", { "class": "ed-list-head" }, [h("h3", { text: titleText }), button(addLabel, onadd, "ed-quiet")])
    ].concat(kids));
  }

  function mapText(m) { return (m || []).map(function (p) { return p[0] + " = " + p[1]; }).join("\n"); }
  function parseMap(text, old) {
    var prev = {};
    (old || []).forEach(function (p) { prev[p[0]] = p[1]; });
    return text.split("\n").map(function (l) { return l.trim(); }).filter(Boolean).map(function (l) {
      var i = l.indexOf("=");
      var k = (i < 0 ? l : l.slice(0, i)).trim(), v = i < 0 ? "" : l.slice(i + 1).trim();
      return [k, prev.hasOwnProperty(k) && String(prev[k]) === v ? prev[k] : v];
    });
  }

  function renderSends(id, r) {
    r.sends = r.sends || [];
    var kids = r.sends.map(function (sd, i) {
      var s = function (k) { return function (v) { sd[k] = v; changed(); }; };
      return h("div", { "class": "ed-item" }, [
        h("div", { "class": "ed-row" }, [
          field("Field", input(sd.field, s("field"), { "class": "mono", spellcheck: "false" })),
          field("Usage", select([["R", "R"], ["S", "S"]], sd.usage || "R", function (v) { sd.usage = v; render(); changed(); }))
        ]),
        field("Call to", input(sd.target, s("target"), { "class": "mono", spellcheck: "false", placeholder: "the sheet's own target" }), "Name a call made on the way, or leave empty for the sheet's target."),
        sd.usage === "S" ? exprField("Sent when", sd.when, s("when"), function () { return rowCands(id, false); }, id) : null,
        sd.usage === "S" ? field("Otherwise", select([["", "—"]].concat(OTHERWISE), sd.otherwise || "", s("otherwise"))) : null,
        field("Value map", textarea(mapText(sd.map), function (v) { sd.map = parseMap(v, sd.map); changed(); }, { "class": "mono", rows: 2, spellcheck: "false", placeholder: "Gold = GLD" }), "One per line: value = code the target expects."),
        button("Remove send", function () { r.sends.splice(i, 1); render(); changed(); }, "ed-quiet ed-danger")
      ]);
    });
    panel.appendChild(listSection("Sent as", "Add send", function () { r.sends.push({ field: "", usage: "R" }); render(); changed(); }, kids));
  }

  function renderRejects(id, r) {
    r.rejects = r.rejects || [];
    var kids = r.rejects.map(function (rj, i) {
      var s = function (k) { return function (v) { rj[k] = v; changed(); }; };
      var isInput = kinds[id] === "input";
      return h("div", { "class": "ed-item" }, [
        exprField("Reject when", rj.when, s("when"), function () { return rowCands(id, isInput); }, id,
          isInput ? "May name this input and other inputs. A check that needs a derived value goes on that row." : null),
        h("div", { "class": "ed-row" }, [
          field("Code", input(rj.code, s("code"), { "class": "mono", spellcheck: "false", placeholder: "UPPER_SNAKE_CASE" })),
          field("Message", input(rj.message, s("message")))
        ]),
        button("Remove reject", function () { r.rejects.splice(i, 1); render(); changed(); }, "ed-quiet ed-danger")
      ]);
    });
    panel.appendChild(listSection("Rejects", "Add reject", function () { r.rejects.push({ when: "", code: "", message: "" }); render(); changed(); }, kids));
  }

  function renderQuestions(id, r) {
    r.open_questions = r.open_questions || [];
    var kids = r.open_questions.map(function (q, i) {
      return h("div", { "class": "ed-row ed-question" }, [
        input(q, function (v) { r.open_questions[i] = v; changed(); }, { "aria-label": "Open question " + (i + 1) }),
        button("Remove", function () { r.open_questions.splice(i, 1); render(); changed(); }, "ed-quiet ed-danger")
      ]);
    });
    panel.appendChild(listSection("Open questions", "Add question", function () { r.open_questions.push(""); render(); changed(); }, kids));
  }

  // ---------- Start ----------

  updateBar();
  if (flash) { say(flash.text, "success", flash.details); sticky = true; }
  check();
})();
