// dsheets flow editor: the side panel of `dsheets serve --edit` on a flow.
// It holds the draft of flow.toml and of interface fields in memory, asks
// the server to check it after each change, and redraws the flow and its
// review marks from the server's answer. The server applies and
// re-validates everything it writes; this script holds form state only.
(function () {
  var stateEl = document.getElementById("flow-edit-state");
  if (!stateEl) return;
  var S = JSON.parse(stateEl.textContent);

  var TYPES = ["string", "number", "boolean", "date", "datetime", "object", "array"];
  var ID_PATTERN = /^[a-z0-9][a-z0-9-]*$/;
  var FLASH_KEY = "dsheets-flow-editor-flash";

  function clone(x) { return JSON.parse(JSON.stringify(x)); }

  // ---------- Draft state ----------

  // base is flow.toml as loaded; M is the draft. fields are interface
  // edits; deleted are the file indexes of deleted stages.
  var base = { input: S.input || {}, stages: S.stages || [], output: S.output || {} };
  var M = clone(base);
  var fields = [], deleted = [];
  var tokens = clone(S.tokens || {});
  var panel = S.panel, problems = S.problems || [], warnings = [];
  var selected = null; // { row: id } | { stage: i } | { output: true } | { input: true }
  // After a stage is added or removed, the panel's steps describe the flow
  // before the change until the next check answers; actions that name a
  // step wait for it.
  var reshaped = false;

  // norm drops empty values, so a key the file lacks and an empty one
  // compare equal.
  function norm(x) {
    if (Array.isArray(x)) return x.length ? x.map(norm) : undefined;
    if (x && typeof x === "object") {
      var out = {}, any = false;
      Object.keys(x).sort().forEach(function (k) {
        var v = norm(x[k]);
        if (v !== undefined && v !== "" && v !== null) { out[k] = v; any = true; }
      });
      return any ? out : undefined;
    }
    return x;
  }
  function same(a, b) { return JSON.stringify(norm(a)) === JSON.stringify(norm(b)); }

  function changeCount() {
    var n = fields.length + deleted.length;
    if (!same(M.input, base.input)) n++;
    if (!same(M.output, base.output)) n++;
    M.stages.forEach(function (sd) {
      if (sd.orig === null || sd.orig === undefined || !same(sd, base.stages[sd.orig])) n++;
    });
    return n;
  }

  function draft() {
    return {
      file: S.file, token: S.token, input: M.input, stages: M.stages, deleted: deleted,
      output: M.output, fields: fields, tokens: tokens
    };
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
    var id = control.id || ("fe-f" + (++uid));
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
  function code(text) { return h("code", { text: text }); }

  // ---------- Page chrome: bar and panel ----------

  var main = document.getElementById("flow-main");
  var countEl = h("span", { "class": "ed-count" });
  var msgEl = h("span", { "class": "ed-msg", role: "status", "aria-live": "polite" });
  var staleEl = h("span", { "class": "ed-stale", hidden: true });
  var discardBtn = button("Discard", discard);
  var saveBtn = button("Save", save, "ed-primary");
  var bar = h("div", { "class": "ed-bar" }, [
    h("div", { "class": "ed-bar-left" }, [countEl, msgEl, staleEl]),
    h("div", { "class": "ed-bar-right" }, [discardBtn, saveBtn])
  ]);
  var detailsEl = h("div", { "class": "ed-details", hidden: true });
  bar.appendChild(detailsEl);
  var panelEl = h("aside", { "class": "ed-panel", "aria-label": "Edit the flow", hidden: true });
  document.body.appendChild(panelEl);
  document.body.appendChild(bar);
  document.body.classList.add("editing");
  // Interface files a stage can name, offered while typing.
  var list = h("datalist", { id: "fe-ifaces" }, (S.interfaces || []).map(function (c) {
    return h("option", { value: c.ref, label: c.name });
  }));
  document.body.appendChild(list);

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
  }

  var sticky = false;
  function summarize() {
    if (sticky) return;
    var fresh = problems.filter(function (p) { return p.new; });
    var old = problems.filter(function (p) { return p.severity === "error" && !p.new; });
    var lines = fresh.concat(old).map(function (p) { return (p.new ? "✕ " : "") + p.message; })
      .concat(warnings.map(function (w) { return "⚠ " + w; }));
    if (fresh.length) return say(fresh.length === 1 ? "1 error this edit introduces; it cannot be saved yet." : fresh.length + " errors this edit introduces; it cannot be saved yet.", "danger", lines);
    if (old.length) return say(old.length === 1 ? "1 error was already in the flow; it does not block saving." : old.length + " errors were already in the flow; they do not block saving.", "muted", lines);
    say(warnings.length ? "Saving will also change an imported interface; see below." : "", "muted", lines);
  }

  // ---------- Checking and saving ----------

  var checkTimer = null, checking = 0;
  function changed(reshape, typing) {
    sticky = false;
    if (reshape) reshaped = true;
    updateBar();
    if (!typing) render();
    clearTimeout(checkTimer);
    checkTimer = setTimeout(check, typing ? 500 : 300);
  }
  // typed is a change made by typing in the panel: the panel stays as it
  // is, so the field keeps its focus and caret.
  function typed(reshape) { changed(reshape, true); }

  // refresh redraws the panel after a check, unless someone is typing in
  // it; then it waits until they leave the field.
  var deferred = false;
  function refresh() {
    var a = document.activeElement;
    if (a && panelEl.contains(a) && (a.tagName === "INPUT" && a.type !== "checkbox" && a.type !== "radio" || a.tagName === "TEXTAREA")) {
      deferred = true;
      return;
    }
    deferred = false;
    render();
  }
  panelEl.addEventListener("focusout", function () {
    setTimeout(function () { if (deferred) refresh(); }, 0);
  });

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

  function take(res) {
    problems = res.problems || [];
    warnings = res.warnings || [];
    if (res.panel) panel = res.panel;
    // A file first seen in this check (a stage now names it) is edited
    // from the version this check read.
    Object.keys(res.iface_tokens || {}).forEach(function (p) {
      if (!(p in tokens)) tokens[p] = res.iface_tokens[p];
    });
    if (res.page) {
      var doc = new DOMParser().parseFromString(res.page, "text/html");
      var fresh = doc.getElementById("flow-main");
      if (fresh) main.innerHTML = fresh.innerHTML;
    }
    staleEl.hidden = !res.stale;
    staleEl.textContent = res.stale ? "Not redrawn: " + res.stale : "";
    reshaped = false;
    decorate();
  }

  function check() {
    var seq = ++checking;
    return post("/edit/flow/check", draft()).then(function (res) {
      if (seq !== checking) return;
      if (res.conflict && res.conflict.length) return conflict(res.conflict);
      take(res);
      updateBar();
      summarize();
      refresh();
    }).catch(function (err) {
      if (seq === checking) say("Checking failed: " + err.message, "danger");
    });
  }

  function save() {
    clearTimeout(checkTimer);
    saveBtn.disabled = true;
    say("Saving…");
    post("/edit/flow/save", draft()).then(function (res) {
      if (res.conflict && res.conflict.length) return conflict(res.conflict);
      if (!res.saved) {
        take(res);
        updateBar();
        summarize();
        render();
        return;
      }
      var msg = "Saved." + (res.version_raised ? " Version raised to " + res.version + "." : "");
      try { sessionStorage.setItem(FLASH_KEY, JSON.stringify({ msg: msg, details: (res.warnings || []).map(function (w) { return "⚠ " + w; }) })); } catch (e) { /* no storage: no message */ }
      leaving = true;
      location.reload();
    }).catch(function (err) {
      updateBar();
      say("Saving failed: " + err.message, "danger");
    });
  }

  function conflict(paths) {
    say("Changed on disk since this page loaded: " + paths.join(", ") + ". Nothing was saved. Reload to start from the files as they are now; your unsaved edits will be lost.", "danger");
    msgEl.appendChild(document.createTextNode(" "));
    msgEl.appendChild(button("Reload", function () { leaving = true; location.reload(); }));
    fitBar();
  }

  function discard() {
    if (changeCount() && !confirm("Discard every unsaved change to this flow?")) return;
    M = clone(base);
    fields = []; deleted = [];
    selected = null;
    changed(true);
  }

  var leaving = false;
  window.addEventListener("beforeunload", function (e) {
    if (leaving || changeCount() === 0) return;
    e.preventDefault();
    e.returnValue = "";
  });

  // ---------- Changes ----------

  function computesLists() {
    var out = [M.output.computes = M.output.computes || []];
    M.stages.forEach(function (sd) {
      sd.computes = sd.computes || [];
      out.push(sd.computes);
      if (sd.kind === "call") out.push(sd.callee_computes = sd.callee_computes || []);
    });
    return out;
  }

  function unpin(id) {
    computesLists().forEach(function (l) {
      var i = l.indexOf(id);
      if (i >= 0) l.splice(i, 1);
    });
  }

  function pin(id, step) {
    var st = panel.steps[step];
    unpin(id);
    var target = st.stage < 0 ? M.output : M.stages[st.stage];
    (target[st.key] = target[st.key] || []).push(id);
    changed();
  }

  // carriesOf is the carries table of flow.toml that a boundary reads.
  function carriesOf(b) {
    var t = null;
    if (b.part === "input") t = M.input;
    else if (b.part === "output") t = M.output;
    else if (b.stage >= 0 && M.stages[b.stage]) {
      var sd = M.stages[b.stage];
      t = b.part === "handoff" ? sd : sd[b.part] = sd[b.part] || { interface: "" };
    }
    if (!t) return null;
    t.carries = t.carries || {};
    return t.carries;
  }

  function mapField(b, name, row) {
    var c = carriesOf(b);
    if (!c) return;
    Object.keys(c).forEach(function (f) { if (c[f] === row) delete c[f]; });
    c[name] = row;
    changed();
  }

  function fieldEdit(b, name) {
    for (var i = 0; i < fields.length; i++) {
      if (fields[i].interface === b.path && fields[i].name === name) return fields[i];
    }
    return null;
  }

  // Field edits remember the row whose panel made them, to list them there
  // after the mark they fixed is gone.
  function addField(b, name, type, required, row) {
    var fe = fieldEdit(b, name);
    if (fe) fields.splice(fields.indexOf(fe), 1);
    fields.push({ interface: b.path, name: name, type: type, required: required, add: true, row: row, label: b.name || b.ref });
    changed();
  }

  function fixField(b, fd, change, row) {
    var fe = fieldEdit(b, fd.name);
    if (!fe) {
      fe = { interface: b.path, name: fd.name, type: fd.type || "", required: !!fd.required, row: row, label: b.name || b.ref };
      fields.push(fe);
    }
    if (change.type) fe.type = change.type;
    if (change.required !== undefined) fe.required = change.required;
    changed();
  }

  function undoField(fe) {
    fields.splice(fields.indexOf(fe), 1);
    changed();
  }

  function addStage(step) {
    var st = panel.steps[step];
    var at = st.stage < 0 ? M.stages.length : st.stage;
    M.stages.splice(at, 0, { orig: null, id: "", kind: "handoff", interface: "", producer: st.team === "No team named" ? "" : st.team, computes: [] });
    selected = { stage: at };
    changed(true);
  }

  function deleteStage(i) {
    var sd = M.stages[i];
    var pinned = (sd.computes || []).concat(sd.callee_computes || []);
    var msg = "Delete stage " + (i + 1) + "?" + (pinned.length ? " " + pinned.join(", ") + (pinned.length === 1 ? " goes" : " go") + " back to automatic placement." : "");
    if (!confirm(msg)) return;
    if (sd.orig !== null && sd.orig !== undefined) deleted.push(sd.orig);
    M.stages.splice(i, 1);
    selected = null;
    changed(true);
  }

  function setKind(i, kind) {
    var sd = M.stages[i];
    if (sd.kind === kind) return;
    if (kind === "call") {
      if ((sd.interface || sd.carries && Object.keys(sd.carries).length) &&
        !confirm("Make this stage a call? Its interface and carries will be removed.")) return render();
      sd.caller = sd.caller || sd.producer || "";
      sd.producer = ""; sd.interface = ""; sd.carries = null;
      sd.request = sd.request || { interface: "" };
      sd.response = sd.response || { interface: "" };
      sd.callee_computes = sd.callee_computes || [];
    } else {
      var lost = (sd.request && sd.request.interface) || (sd.response && sd.response.interface) || sd.callee || (sd.callee_computes || []).length;
      if (lost && !confirm("Make this stage a handoff? Its request, response, callee and the rows the callee computes will be removed.")) return render();
      sd.producer = sd.producer || sd.caller || "";
      sd.caller = ""; sd.callee = ""; sd.request = null; sd.response = null; sd.callee_computes = [];
    }
    sd.kind = kind;
    changed(true);
  }

  // ---------- The page: rows and headers open the panel ----------

  function decorate() {
    main.querySelectorAll("tr[data-row]").forEach(function (tr) {
      var id = tr.getAttribute("data-row");
      tr.classList.add("ed-selectable");
      tr.classList.toggle("ed-selected", !!(selected && selected.row === id));
      tr.tabIndex = 0;
      tr.onclick = function () { open({ row: id }); };
      tr.onkeydown = function (e) { if (e.key === "Enter") open({ row: id }); };
    });
    main.querySelectorAll("th[data-step]").forEach(function (th) {
      var i = Number(th.getAttribute("data-step"));
      var st = panel && panel.steps[i];
      th.classList.add("ed-selectable");
      th.classList.toggle("ed-selected", !!st && !!selected && (st.stage < 0 ? !!selected.output : selected.stage === st.stage));
      th.onclick = function () { if (st) open(st.stage < 0 ? { output: true } : { stage: st.stage }); };
      if (st && st.key === "computes" && !th.querySelector(".fe-add-stage")) {
        th.appendChild(h("div", {}, [button("+ stage", function (e) { e.stopPropagation(); addStage(i); }, "ed-quiet fe-add-stage",
          { title: "Add a stage before " + (st.stage < 0 ? "the output" : "stage " + (st.stage + 1)) + ": a new boundary in this step" })]));
      }
    });
    main.querySelectorAll("th[data-boundary]").forEach(function (th) {
      var b = panel && panel.boundaries[Number(th.getAttribute("data-boundary"))];
      th.classList.add("ed-selectable");
      if (!b) return;
      var sel = b.stage >= 0 ? { stage: b.stage } : b.part === "output" ? { output: true } : { input: true };
      th.classList.toggle("ed-selected", !!selected && JSON.stringify(sel) === JSON.stringify(selected));
      th.onclick = function () { open(sel); };
    });
    var head = main.querySelector(".head");
    if (head && !head.querySelector(".ed-actions")) {
      head.appendChild(h("div", { "class": "ed-actions" }, [
        S.sheet ? h("a", { "class": "ed-btn", href: S.sheet, text: "Edit the end-to-end sheet" }) : h("span", { "class": "ed-note", text: S.sheet_note || "" }),
        h("span", { "class": "ed-note", text: "Select a value to place it or fix its fields; select a column header to edit its stage." })
      ]));
    }
  }

  function open(sel) {
    selected = sel;
    decorate();
    render();
  }

  function close() {
    selected = null;
    decorate();
    render();
  }

  // ---------- The panel ----------

  function render() {
    var keepScroll = panelEl.scrollTop;
    panelEl.textContent = "";
    panelEl.hidden = !selected;
    document.body.classList.toggle("panel-open", !!selected);
    if (!selected) return fitBar();
    if (selected.row) renderRow(selected.row);
    else if (selected.stage !== undefined) renderStage(selected.stage);
    else if (selected.output) renderOutput();
    else renderInput();
    panelEl.scrollTop = keepScroll;
    fitBar();
  }

  function head(title, sub) {
    panelEl.appendChild(h("div", { "class": "ed-panel-head" }, [
      h("h2", { text: title }), button("Close", close, "ed-quiet")
    ]));
    if (sub) panelEl.appendChild(sub);
  }

  function section(title, kids) {
    panelEl.appendChild(h("div", { "class": "fe-section" }, [h("h3", { text: title })].concat(kids)));
  }

  function stepText(st) { return st.label + " · " + st.team + " (" + st.role.toLowerCase() + ")"; }

  function renderRow(id) {
    var pr = panel && panel.rows[id];
    if (!pr) {
      head(id);
      panelEl.appendChild(h("p", { "class": "ed-note", text: "This value is not in the flow as drawn now." }));
      return;
    }
    head(pr.name, h("div", { "class": "ed-note" }, [code(id), " · ",
      S.sheet ? h("a", { href: S.sheet + "#row-" + id, text: "open its rule on the sheet" }) : (S.sheet_note || "")]));

    if (pr.input) {
      section("Computed by", [h("p", { "class": "ed-note", text: "Nobody: it comes from the request, at the input." })]);
    } else {
      var st = panel.steps[pr.step];
      var kids = [h("p", {}, [st ? stepText(st) : "No step", h("span", { "class": "ed-note", text: pr.pinned ? " — pinned in flow.toml" : " — placed automatically" })])];
      if (reshaped) {
        kids.push(h("p", { "class": "ed-note", text: "Checking the new stages…" }));
      } else {
        kids.push(h("ul", { "class": "fe-steps" }, (pr.can || []).map(function (s) {
          var here = s === pr.step;
          return h("li", { "class": here ? "here" : "" }, [
            h("span", { text: stepText(panel.steps[s]) }),
            here && pr.pinned ? h("span", { "class": "fe-here", text: "computed here" })
              : button(here ? "Pin here" : "Compute here", function () { pin(id, s); }, "ed-quiet")
          ]);
        })));
        if (!(pr.can || []).length) kids.push(h("p", { "class": "ed-note", text: "No step can compute it before it is needed; check where the rows it uses are pinned." }));
        if (pr.pinned) kids.push(button("Unpin: place it automatically", function () { unpin(id); changed(); }));
      }
      section("Computed by", kids);
    }

    var marks = (pr.marks || []).map(function (m) { return renderMark(pr, m); });
    section("Crossing boundaries", marks.length ? marks : [h("p", { "class": "ed-note", text: "Every interface it crosses carries it." })]);
    var mine = fields.filter(function (fe) { return fe.row === id; });
    if (mine.length) section("Unsaved interface changes", mine.map(function (fe) {
      return h("p", {}, [(fe.add ? "Add " : "Change ") , code(fe.name), " (" + (fe.type || "no type") + (fe.required ? ", required" : ", optional") + ") in ", code(fe.label), " ",
        button("Undo", function () { undoField(fe); }, "ed-quiet")]);
    }));
  }

  function renderMark(pr, m) {
    var b = panel.boundaries[m.boundary];
    var sym = m.status === "missing" ? "✕" : "⚠";
    var kids = [
      h("div", { "class": "fe-mark-head" }, [h("span", { "class": "fe-sym", text: sym + " " }), code(b.name || b.ref), " · " + b.label]),
      h("div", { "class": "ed-note", text: m.note })
    ];
    if (!b.path) {
      kids.push(h("div", { "class": "ed-hint", text: "Read-only: " + (b.read_only || "not in this repository") + ". It stays on " + (b.owner || "its owner") + "'s list under To agree." }));
      return h("div", { "class": "fe-mark " + m.status }, kids);
    }
    var pending = fieldEdit(b, m.field);
    if (pending) {
      kids.push(h("div", { "class": "fe-done" }, [
        (pending.add ? "Adding " : "Changing ") + pending.name + " (" + (pending.type || "no type") + (pending.required ? ", required" : ", optional") + ") when saved. ",
        button("Undo", function () { undoField(pending); }, "ed-quiet")
      ]));
    }
    if (m.status === "missing" && !pending) {
      var name = m.field, type = TYPES.indexOf(pr.type) >= 0 ? pr.type : "string", req = true;
      kids.push(h("div", { "class": "fe-sub", text: "Add the field to " + (b.name || b.ref) }));
      kids.push(h("div", { "class": "ed-row" }, [
        field("Name", input(name, function (v) { name = v.trim(); }, { "class": "mono", spellcheck: "false" })),
        field("Type", select(TYPES, type, function (v) { type = v; }))
      ]));
      var cb = h("input", { type: "checkbox", checked: req, onchange: function (e) { req = e.target.checked; } });
      kids.push(h("label", { "class": "ed-check" }, [cb, "Required"]));
      if (b.imported) {
        kids.push(h("div", { "class": "ed-hint warn", text: (b.name || b.ref) + " is imported from " + b.imported + ". The field is written with local = true, so importing again keeps it; add it to " + b.imported + " too." }));
      }
      kids.push(button("Add field", function () { if (name) addField(b, name, type, req, pr.id); }, "ed-primary"));
    }
    if (m.status === "warning" && !pending) {
      var fd = (b.fields || []).filter(function (f) { return f.name === m.field; })[0];
      if (fd && m.fix === "required") kids.push(button("Make " + fd.name + " required", function () { fixField(b, fd, { required: true }, pr.id); }));
      if (fd && m.fix && m.fix.indexOf("type:") === 0) {
        var t = m.fix.slice(5);
        kids.push(button("Change the type of " + fd.name + " to " + t, function () { fixField(b, fd, { type: t }, pr.id); }));
      }
      if (fd && m.fix && b.imported) {
        kids.push(h("div", { "class": "ed-hint warn", text: (b.name || b.ref) + " is imported from " + b.imported + ": change it there too, or importing again undoes this." }));
      }
      if (!m.fix) kids.push(h("div", { "class": "ed-hint", text: "Changing the field would not fit it; change the row's values or its send on the sheet." }));
    }
    // Output fields match sends by name; there, a send on the sheet maps
    // the row, not carries.
    if (b.part !== "output") {
      var free = (b.fields || []).filter(function (f) { return f.type !== "object" && (!f.row || f.row === pr.id) && f.name !== m.field; });
      if (free.length) {
        var pick = free[0].name;
        kids.push(h("div", { "class": "fe-sub", text: m.status === "missing" ? "Or map it to a field " + (b.name || b.ref) + " has" : "Or map it to another field" }));
        kids.push(h("div", { "class": "ed-row" }, [
          select(free.map(function (f) { return [f.name, f.name + (f.type ? " (" + f.type + (f.required ? ", required" : "") + ")" : "")]; }), pick, function (v) { pick = v; }, { "aria-label": "Field" }),
          button("Map", function () { mapField(b, pick, pr.id); })
        ]));
      }
    }
    return h("div", { "class": "fe-mark " + m.status }, kids);
  }

  function ifaceInput(value, onchange) {
    return input(value, function (v) { onchange(v.trim()); }, { list: "fe-ifaces", "class": "mono", spellcheck: "false", placeholder: "../../interfaces/team/name.toml or alias:path" });
  }

  function renderStage(i) {
    var sd = M.stages[i];
    if (!sd) return close();
    var isNew = sd.orig === null || sd.orig === undefined;
    head("Stage " + (i + 1) + (isNew ? " (new)" : ""));
    var derived = function () {
      var ref = sd.kind === "call" ? (sd.request && sd.request.interface) : sd.interface;
      return (ref || "").replace(/^.*[\/:]/, "").replace(/\.[^.]*$/, "");
    };
    var idIn = input(sd.id, function (v, el) {
      sd.id = v.trim();
      el.setCustomValidity(sd.id && !ID_PATTERN.test(sd.id) ? "Lowercase letters, digits and dashes" : "");
      typed();
    }, { "class": "mono", spellcheck: "false", placeholder: derived() || "from the interface's file name", disabled: !isNew });
    panelEl.appendChild(field("ID", idIn, isNew ? "Calls are named by their ID in call.<id>.<field> and send targets. It cannot change once saved." : "Fixed once saved: flows, sheets and other teams refer to it."));
    var kind = h("div", { "class": "ed-seg", role: "radiogroup", "aria-label": "Kind" });
    [["handoff", "Handoff"], ["call", "Call"]].forEach(function (k) {
      var rid = "fe-kind-" + k[0];
      kind.appendChild(h("input", { type: "radio", name: "fe-kind", id: rid, checked: sd.kind === k[0], onchange: function () { setKind(i, k[0]); } }));
      kind.appendChild(h("label", { "for": rid, text: k[1] }));
    });
    panelEl.appendChild(h("div", { "class": "ed-field" }, [h("span", { "class": "ed-label", text: "Kind" }), kind,
      h("div", { "class": "ed-hint", text: sd.kind === "call" ? "A request and a response; the caller keeps its own state across the call." : "The next team takes over and sees only what the interface carries." })]));
    if (sd.kind === "call") {
      panelEl.appendChild(field("Caller", input(sd.caller, function (v) { sd.caller = v; typed(); }), "The team that computes the rows before the call, and continues after it."));
      panelEl.appendChild(field("Request interface", ifaceInput(sd.request && sd.request.interface, function (v) { sd.request = sd.request || {}; sd.request.interface = v; typed(true); })));
      panelEl.appendChild(field("Callee", input(sd.callee, function (v) { sd.callee = v; typed(); })));
      panelEl.appendChild(field("Response interface", ifaceInput(sd.response && sd.response.interface, function (v) { sd.response = sd.response || {}; sd.response.interface = v; typed(true); })));
    } else {
      panelEl.appendChild(field("Interface", ifaceInput(sd.interface, function (v) { sd.interface = v; typed(true); }), "An interface file that exists; a file from another team's repository is written alias:path."));
      panelEl.appendChild(field("Producer", input(sd.producer, function (v) { sd.producer = v; typed(); }), "The team that computes the rows before this handoff."));
    }
    var pinned = (sd.computes || []).concat(sd.callee_computes || []);
    section("Rows pinned to this stage", [pinned.length ? h("p", {}, pinned.map(function (id, k) { return h("span", {}, [k ? ", " : "", code(id)]); }))
      : h("p", { "class": "ed-note", text: "None; rows are placed automatically. Pin a row from its own panel." })]);
    section("Delete", [button("Delete stage " + (i + 1), function () { deleteStage(i); }, "ed-danger")]);
  }

  function renderOutput() {
    head("Output");
    panelEl.appendChild(h("p", { "class": "ed-note" }, ["Interface ", code(M.output.interface || "")]));
    panelEl.appendChild(field("Producer", input(M.output.producer, function (v) { M.output.producer = v; typed(); }), "The team that builds the output."));
    var pinned = M.output.computes || [];
    section("Rows pinned to the output's step", [pinned.length ? h("p", {}, pinned.map(function (id, k) { return h("span", {}, [k ? ", " : "", code(id)]); }))
      : h("p", { "class": "ed-note", text: "None." })]);
  }

  function renderInput() {
    head("Input");
    panelEl.appendChild(h("p", { "class": "ed-note" }, ["Interface ", code(M.input.interface || ""), ". Request rows match its fields by path."]));
  }

  // ---------- Start ----------

  decorate();
  updateBar();
  var flash = null;
  try { flash = JSON.parse(sessionStorage.getItem(FLASH_KEY) || "null"); sessionStorage.removeItem(FLASH_KEY); } catch (e) { flash = null; }
  if (flash) {
    sticky = true;
    say(flash.msg, "success", flash.details);
  } else {
    summarize();
  }
  fitBar();
})();
