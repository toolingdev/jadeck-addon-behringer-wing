// Channel strip widget: one strip's processing, picked in the widget's
// settings (or for now with the picker at the top left). Levels and the
// meter refresh ~15 times a second, the rest once a second.
(function () {
  "use strict";
  var W = window.Wing, el = W.el;
  var head = document.getElementById("head"), panelEl = document.getElementById("panel"), noteEl = document.getElementById("note");
  var picker = document.getElementById("pick");
  var strip = "", localPick = "", detail = null, live = null, peaks = W.peaks(), tDetail = 0, tLive = 0;
  var panel = new W.Panel(panelEl, function (p, v) { return W.action("set_param", { path: p.path, value: v }); });
  panel.onError = function (e) { note(e, true); };
  W.lockButton(document.getElementById("tools"), panel);

  var bar = el("div", "dot", head), info = el("div", "info", head), led = el("canvas", "", head);
  var tag = el("div", "tag", info), name = el("div", "name", info), lvl = el("div", "lvl", info);

  function note(t, bad) { noteEl.textContent = t || ""; noteEl.classList.toggle("bad", !!bad); }
  function current() { return localPick || (W.ctx.settings && W.ctx.settings.strip) || "ch:1"; }

  function fillPicker() {
    var opts = W.ctx.state.stripOptions || [];
    var cur = current();
    picker.textContent = "";
    if (!opts.some(function (o) { return o.value === cur; })) opts = [{ value: cur, label: cur.replace(":", " ").toUpperCase() }].concat(opts);
    opts.forEach(function (o) { if (o.value.indexOf("dca:") === 0) return; var e = el("option", "", picker, o.label); e.value = o.value; });
    picker.value = cur;
  }
  picker.onchange = function () { localPick = picker.value; strip = ""; detail = null; start(); };

  function sectionsOf(d) {
    var st = d.strip || {};
    return (d.sections || []).map(function (s) {
      return { id: s.id, title: s.title, params: s.params.map(function (p) {
        if (p.name === "pan" && st.stereo) p.label = "Balance (stereo)";
        return p;
      }) };
    });
  }

  function paintHead() {
    var s = (live && live.strips && live.strips[0]) || (detail && detail.strip) || {};
    bar.style.background = s.color || "#707070";
    tag.textContent = (s.tag || current().replace(":", " ").toUpperCase()) + (s.stereo ? " · ST" : "");
    name.textContent = s.name || "";
    lvl.textContent = (s.db != null ? (s.db <= -143.9 ? "−∞" : W.dbText(s.db) + " dB") : "") + (s.muted ? " · MUTED" : s.emuted ? " · MUTED BY " + (s.muteBy || "GROUP").toUpperCase() : "");
    lvl.classList.toggle("muted", !!s.emuted);
    led.classList.toggle("st", !!s.stereo);
    var cols = [{ level: s.l, peak: peaks("L", s.l) }];
    if (s.stereo) cols.push({ level: s.r, peak: peaks("R", s.r) });
    W.led(led, cols, 10);
  }

  function disabled() { return W.ctx.editing || !detail || !detail.connected || detail.readOnly || !!detail.locked; }

  function pollDetail() {
    clearTimeout(tDetail);
    if (document.hidden) { tDetail = setTimeout(pollDetail, 1000); return; }
    var s = current();
    W.resource("res:detail?strip=" + encodeURIComponent(s) + "&t=" + Math.floor(Date.now() / 50)).then(function (d) {
      if (s !== current()) return;
      detail = d || W.ctx.state.previewDetail || null;
      if (!detail) note("Connecting…");
      else if (detail.error) note(detail.error, true);
      else if (detail.locked) note(detail.locked, true);
      else if (detail.readOnly) note("Read only");
      else if (!noteEl.classList.contains("bad")) note("");
      if (detail && detail.sections) {
        if (strip !== s) { strip = s; panel.set(sectionsOf(detail), disabled()); }
        else panel.update(sectionsOf(detail), disabled());
      }
      paintHead();
      tDetail = setTimeout(pollDetail, 1000);
    });
  }
  function pollLive() {
    clearTimeout(tLive);
    if (document.hidden) { tLive = setTimeout(pollLive, 500); return; }
    var started = Date.now(), tap = (W.ctx.settings || {}).tap === "pre" ? "pre" : "post";
    W.live([current()], true, tap).then(function (d) {
      live = d || W.ctx.state.previewLive || null;
      paintHead();
      tLive = setTimeout(pollLive, Math.max(0, 50 - (Date.now() - started)));
    });
  }
  function start() { fillPicker(); pollDetail(); pollLive(); }

  W.on(function (kind) {
    if (kind === "init") { localPick = ""; strip = ""; start(); }
    if (kind === "state") fillPicker();
    if (kind === "size") panel.draw();
  });
  window.addEventListener("resize", function () { panel.draw(); });
})();
