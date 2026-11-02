// Hovering a row tints what it depends on (transitively) and what depends on
// it. The sheet reads fully without this script.
(function () {
  var rows = Array.prototype.slice.call(document.querySelectorAll("tr.row"));
  var byId = {}, deps = {}, users = {};
  rows.forEach(function (tr) {
    var id = tr.getAttribute("data-id");
    byId[id] = tr;
    deps[id] = (tr.getAttribute("data-deps") || "").split(" ").filter(Boolean);
  });
  Object.keys(deps).forEach(function (id) {
    deps[id].forEach(function (d) { (users[d] = users[d] || []).push(id); });
  });
  function walk(id, edges, seen) {
    (edges[id] || []).forEach(function (n) {
      if (!seen[n]) { seen[n] = true; walk(n, edges, seen); }
    });
    return seen;
  }
  function clear() {
    document.body.classList.remove("hovering");
    rows.forEach(function (tr) { tr.classList.remove("focus", "up", "down"); });
  }
  rows.forEach(function (tr) {
    tr.addEventListener("mouseenter", function () {
      clear();
      var id = tr.getAttribute("data-id");
      document.body.classList.add("hovering");
      tr.classList.add("focus");
      Object.keys(walk(id, deps, {})).forEach(function (u) { if (byId[u]) byId[u].classList.add("up"); });
      Object.keys(walk(id, users, {})).forEach(function (d) { if (byId[d]) byId[d].classList.add("down"); });
    });
  });
  var table = document.querySelector("table.sheet");
  if (table) table.addEventListener("mouseleave", clear);
})();
