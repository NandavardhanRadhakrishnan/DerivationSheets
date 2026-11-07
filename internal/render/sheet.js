// Hovering a row tints what it depends on (transitively) and what depends on
// it. The sheet reads fully without this script. Listeners sit on the
// document, so a sheet the editor redraws keeps working.
(function () {
  // graph reads a table's rows once, and again after the table is replaced.
  function graph(table) {
    if (table.dsGraph) return table.dsGraph;
    var g = { rows: [], byId: {}, deps: {}, users: {} };
    Array.prototype.forEach.call(table.querySelectorAll("tr.row"), function (tr) {
      var id = tr.getAttribute("data-id");
      g.rows.push(tr);
      g.byId[id] = tr;
      g.deps[id] = (tr.getAttribute("data-deps") || "").split(" ").filter(Boolean);
    });
    Object.keys(g.deps).forEach(function (id) {
      g.deps[id].forEach(function (d) { (g.users[d] = g.users[d] || []).push(id); });
    });
    table.dsGraph = g;
    return g;
  }
  function walk(id, edges, seen) {
    (edges[id] || []).forEach(function (n) {
      if (!seen[n]) { seen[n] = true; walk(n, edges, seen); }
    });
    return seen;
  }
  var current = null;
  function clear() {
    document.body.classList.remove("hovering");
    Array.prototype.forEach.call(document.querySelectorAll("tr.row.focus, tr.row.up, tr.row.down"), function (tr) {
      tr.classList.remove("focus", "up", "down");
    });
    current = null;
  }
  document.addEventListener("mouseover", function (e) {
    var tr = e.target.closest ? e.target.closest("tr.row") : null;
    var table = tr && tr.closest("table.sheet");
    if (!table || tr === current) return;
    clear();
    current = tr;
    var g = graph(table), id = tr.getAttribute("data-id");
    document.body.classList.add("hovering");
    tr.classList.add("focus");
    Object.keys(walk(id, g.deps, {})).forEach(function (u) { if (g.byId[u]) g.byId[u].classList.add("up"); });
    Object.keys(walk(id, g.users, {})).forEach(function (d) { if (g.byId[d]) g.byId[d].classList.add("down"); });
  });
  document.addEventListener("mouseout", function (e) {
    if (!current) return;
    var table = current.closest("table.sheet");
    if (!table || !table.contains(e.relatedTarget)) clear();
  });
})();
