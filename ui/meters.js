// Meter bridge: LED columns per strip (L and R for stereo, one for mono), gate and compressor
// lights, the peak level, "CH 3" and the name, in the console's colour.
// Polls the add-on at ~15 Hz while visible; the add-on asks the console
// for meters only while someone polls.
(function () {
  "use strict";
  var W = window.Wing;
  if (!W) return;
  var stripsEl = document.getElementById("strips"), moreEl = document.getElementById("more"), noteEl = document.getElementById("note");
  var peaks = W.peaks();
  var ids = [], rows = {}, data = null, timer = 0;

  function el(tag, cls, parent) { var e = document.createElement(tag); if (cls) e.className = cls; if (parent) parent.appendChild(e); return e; }
  function tap() { return (W.ctx.settings || {}).tap === "pre" ? "pre" : "post"; }

  function build() {
    stripsEl.textContent = "";
    rows = {};
    var w = W.ctx.size.w || window.innerWidth, fit = Math.max(1, Math.floor((w - 16 + 6) / (34 + 6)));
    var shown = ids.slice(0, fit);
    stripsEl.style.gridTemplateColumns = "repeat(" + Math.max(1, shown.length) + ", minmax(0, 1fr))";
    stripsEl.style.gridTemplateRows = "minmax(0, 1fr)";
    moreEl.textContent = ids.length > shown.length ? "+" + (ids.length - shown.length) + " more: make the widget wider" : "";
    if (!ids.length) { el("div", "empty", stripsEl).textContent = "No strips picked (widget settings → Strips)."; return; }
    shown.forEach(function (id) {
      var r = { id: id };
      r.root = el("div", "mstrip", stripsEl);
      r.bar = el("div", "bar", r.root);
      r.cv = el("canvas", "", r.root);
      r.lights = el("div", "lights", r.root);
      r.g = el("i", "g", r.lights);
      r.d = el("i", "d", r.lights);
      r.g.title = "Gate";
      r.d.title = "Compressor";
      r.peak = el("div", "peak", r.root);
      r.tag = el("div", "tag", r.root);
      r.name = el("div", "name", r.root);
      rows[id] = r;
    });
  }

  function render() {
    var byId = {};
    ((data && data.strips) || []).forEach(function (s) { byId[s.id] = s; });
    Object.keys(rows).forEach(function (id) {
      var r = rows[id], s = byId[id] || { tag: id.replace(":", " ").toUpperCase(), color: "#707070" };
      r.bar.style.background = s.color || "#707070";
      r.tag.textContent = (s.tag || "") + (s.stereo ? " ST" : "");
      r.name.textContent = s.name || " ";
      r.root.title = (s.tag || "") + (s.name ? " · " + s.name : "");
      var l = s.l == null ? null : s.l, rr = s.r == null ? null : s.r;
      var pl = peaks(id + "L", l), pr = s.stereo ? peaks(id + "R", rr) : null;
      // stereo: L and R side by side; mono: one column
      W.led(r.cv, s.stereo ? [{ level: l, peak: pl }, { level: rr, peak: pr }] : [{ level: l, peak: pl }]);
      var pk = pl == null && pr == null ? null : Math.max(pl == null ? -200 : pl, pr == null ? -200 : pr);
      r.peak.textContent = pk == null ? "–" : pk <= -90 ? "-inf" : W.dbText(pk);
      r.g.classList.toggle("on", s.gate === true);
      r.d.classList.toggle("on", s.dyn === true);
      r.lights.style.visibility = s.kind === "dca" ? "hidden" : "";
    });
    if (!data) noteEl.textContent = "Connecting…";
    else if (data.error) noteEl.textContent = data.error;
    else if (data.meters && !data.meters.running) noteEl.textContent = data.meters.note || "Waiting for meters…";
    else noteEl.textContent = tap() === "pre" ? "pre fader" : "";
    noteEl.classList.toggle("bad", !!(data && (data.error || (data.meters && !data.meters.running && data.meters.note))));
  }

  function poll() {
    clearTimeout(timer);
    if (document.hidden || !W.ctx.ready) { timer = setTimeout(poll, 500); return; }
    var started = Date.now();
    W.live(ids, true, tap()).then(function (d) {
      data = d || W.ctx.state.previewLive || null;
      render();
      timer = setTimeout(poll, Math.max(0, 50 - (Date.now() - started)));
    });
  }

  W.on(function (kind) {
    if (kind === "init" || kind === "size" || kind === "state") {
      var next = W.stripIds("defaultMeters");
      if (kind !== "state" || next.join() !== ids.join()) {
        ids = next;
        build();
        render();
      }
      if (kind === "init") poll();
    }
  });
  document.addEventListener("visibilitychange", function () { if (!document.hidden) poll(); });
})();
