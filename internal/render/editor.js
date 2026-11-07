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
    var seq = ++checking;
    return post("/edit/check", draft()).then(function (res) {
      if (seq !== checking) return;
      if (res.conflict && res.conflict.length) return conflict(res.conflict);
      showDraft(res.preview);
      problems = res.problems || [];
      refs = { uses: res.uses || {}, unknown: res.unknown || {}, cycle: res.cycle || {} };
      var again = false;
      Object.keys(refs.uses).forEach(function (id) {
        var r = rows[id];
        if (!r || kinds[id] !== "formula") return;
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

  function setKind(id, kind) {
    var r = rows[id], had = kinds[id];
    if (had === kind) return;
    var loses = had === "input" ? r.source && r.source !== "request." : had === "formula" ? r.formula : had === "text" ? r.text : false;
    if (loses && !window.confirm("Switching the rule kind discards the current " + had + ". Continue?")) { render(); return; }
    ["source", "type", "usage", "when", "has_default", "default", "formula", "text"].forEach(function (k) { delete r[k]; });
    if (kind === "input") { r.source = "request."; delete r.uses; }
    if (kind === "formula") r.formula = "";
    if (kind === "text") r.text = "";
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
    document.body.classList.remove("panel-open");
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

  var statusEl = null, usesEl = null, headEl = null;

  function render() {
    panel.textContent = "";
    statusEl = usesEl = headEl = null;
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
    if (kind === "table" || kind === "lookup") {
      panel.appendChild(h("p", { "class": "ed-note", text: "Editing decision tables and lookups comes later. This row is shown read-only." }));
      panel.appendChild(h("dl", { "class": "ed-readonly" }, [
        h("dt", { text: "Name" }), h("dd", { text: r.name || "" }),
        h("dt", { text: "ID" }), h("dd", {}, [h("code", { text: id })])
      ]));
      statusEl = h("div", { "class": "ed-status" });
      panel.appendChild(statusEl);
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
    [["input", "Input"], ["formula", "Formula"], ["text", "Text"]].forEach(function (k) {
      var rid = "ed-kind-" + k[0];
      group.appendChild(h("input", { type: "radio", name: "ed-kind", id: rid, value: k[0], checked: kind === k[0], onchange: function () { setKind(id, k[0]); } }));
      group.appendChild(h("label", { "for": rid, text: k[1] }));
    });
    panel.appendChild(h("div", { "class": "ed-field" }, [h("div", { "class": "ed-label", text: "Rule" }), group]));

    if (kind === "input") renderInput(id, r);
    if (kind === "formula") renderFormula(id, r);
    if (kind === "text") panel.appendChild(field("Rule in words", textarea(r.text, s("text"), { rows: 3 }), "Shown as an open item until someone writes the rule."));

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

  function renderFormula(id, r) {
    var ta = textarea(r.formula, function (v, el) { r.formula = v; changed(); suggest(el); }, { "class": "mono", rows: 3, spellcheck: "false", autocomplete: "off", "aria-autocomplete": "list" });
    var list = h("ul", { "class": "ed-suggest", role: "listbox", hidden: true });
    ta.setAttribute("aria-controls", "ed-suggest");
    list.id = "ed-suggest";
    var active = -1, items = [];

    function prefixAt(el) {
      var m = /[A-Za-z_][A-Za-z0-9_]*$/.exec(el.value.slice(0, el.selectionStart));
      return m ? m[0] : "";
    }
    function suggest(el) {
      var p = prefixAt(el).toLowerCase();
      list.textContent = "";
      items = [];
      active = -1;
      if (!p) { list.hidden = true; return; }
      var cycle = {};
      (refs.cycle[id] || [id]).forEach(function (c) { cycle[c] = true; });
      var cands = order.filter(function (x) { return !deleted[x]; }).map(function (x) {
        return { name: x, label: x, note: rowName(x), blocked: !!cycle[x] };
      }).concat((S.functions || []).map(function (f) {
        return { name: f.name, label: f.signature, note: f.description, fn: true };
      }));
      var starts = cands.filter(function (c) { return c.name.toLowerCase().indexOf(p) === 0; });
      var has = cands.filter(function (c) { return c.name.toLowerCase().indexOf(p) > 0; });
      items = starts.concat(has).slice(0, 8);
      if (!items.length || (items.length === 1 && items[0].name.toLowerCase() === p)) { list.hidden = true; return; }
      items.forEach(function (c, i) {
        var li = h("li", {
          role: "option", id: "ed-sg-" + i, "aria-disabled": c.blocked ? "true" : null,
          "class": c.blocked ? "blocked" : null,
          title: c.blocked ? "Using this row would make a cycle: it depends on " + rowName(id) + "." : null,
          onmousedown: function (e) { e.preventDefault(); pick(i); }
        }, [h("code", { text: c.label }), h("span", { text: c.note || "" })]);
        list.appendChild(li);
      });
      list.hidden = false;
    }
    function move(d) {
      if (!items.length) return;
      var i = active;
      do { i = (i + d + items.length) % items.length; } while (items[i].blocked && i !== active && items.some(function (c) { return !c.blocked; }));
      active = i;
      Array.prototype.forEach.call(list.children, function (li, j) { li.classList.toggle("active", j === active); li.setAttribute("aria-selected", j === active); });
      ta.setAttribute("aria-activedescendant", "ed-sg-" + active);
    }
    function pick(i) {
      var c = items[i];
      if (!c || c.blocked) return;
      var before = ta.value.slice(0, ta.selectionStart), after = ta.value.slice(ta.selectionStart);
      var p = prefixAt(ta);
      var ins = c.fn ? c.name + "(" : c.name;
      ta.value = before.slice(0, before.length - p.length) + ins + after;
      var at = before.length - p.length + ins.length;
      ta.setSelectionRange(at, at);
      r.formula = ta.value;
      list.hidden = true;
      changed();
    }
    ta.addEventListener("keydown", function (e) {
      if (list.hidden) return;
      if (e.key === "ArrowDown") { e.preventDefault(); move(1); }
      else if (e.key === "ArrowUp") { e.preventDefault(); move(-1); }
      else if ((e.key === "Enter" || e.key === "Tab") && active >= 0) { e.preventDefault(); pick(active); }
      else if (e.key === "Escape") { e.preventDefault(); list.hidden = true; }
    });
    ta.addEventListener("blur", function () { list.hidden = true; });
    panel.appendChild(h("div", { "class": "ed-field ed-formula" }, [h("label", { "for": "ed-formula", text: "Formula" }), ta, list]));
    ta.id = "ed-formula";
    usesEl = h("div", { "class": "ed-uses" });
    panel.appendChild(usesEl);
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
      if (!uses.length) usesEl.appendChild(h("span", { "class": "ed-hint", text: "Filled from the formula as you type." }));
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
    if (statusEl) {
      statusEl.textContent = "";
      (problemsByRow()[id] || []).forEach(function (p) {
        statusEl.appendChild(h("div", { "class": "ed-problem " + (p.new ? "new" : p.severity) }, [
          h("span", { "class": "ed-tag", text: p.new ? "New error" : p.severity === "error" ? "Existing error" : "Warning" }),
          " ", p.message
        ]));
      });
    }
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
        sd.usage === "S" ? field("Sent when", input(sd.when, s("when"), { "class": "mono", spellcheck: "false" })) : null,
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
      return h("div", { "class": "ed-item" }, [
        field("Reject when", input(rj.when, s("when"), { "class": "mono", spellcheck: "false" })),
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
