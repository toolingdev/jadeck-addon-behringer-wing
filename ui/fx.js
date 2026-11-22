// FX widget: one tab per FX slot picked in the widget's settings. Each
// shows the model (read only: changing it would change the sound and the
// parameters at once) and the model's parameters as the console lists
// them, mix first, then decay / pre-delay / size / delay time / feedback.
// A reverb's return level is on its FX return channel (Faders widget).
(function () {
  "use strict";
  var W = window.Wing;
  if (!W) return;
  var panelEl = document.getElementById("panel"), noteEl = document.getElementById("note");
  var panel = new W.Panel(panelEl, function (p, v) { return W.action("set_param", { path: p.path, value: v }); });
  panel.onError = function (e) { note(e, true); };
  W.lockButton(document.getElementById("tools"), panel);
  var t = 0, first = true;

  function note(s, bad) { noteEl.textContent = s || ""; noteEl.classList.toggle("bad", !!bad); }
  function slots() {
    var s = (W.ctx.settings || {}).slots;
    var l = Array.isArray(s) ? s : typeof s === "string" ? s.split(",") : [];
    l = l.map(function (x) { return parseInt(x, 10); }).filter(function (n) { return n >= 1 && n <= 16; });
    return l.length ? l : [1, 2];
  }
  function sections(d) {
    return (d.slots || []).map(function (sl) {
      var params = [{ path: "/fx/" + sl.slot + "/mdl", name: "mdl", label: "Model", kind: "text", text: sl.model || "NONE", writable: false }].concat(sl.params || []);
      return { id: "fx" + sl.slot, title: "FX " + sl.slot + (sl.model && sl.model !== "NONE" ? " · " + sl.model : ""), params: params };
    });
  }
  function poll() {
    clearTimeout(t);
    if (document.hidden) { t = setTimeout(poll, 1000); return; }
    W.resource("res:fx?slots=" + slots().join(",") + "&t=" + Math.floor(Date.now() / 50)).then(function (d) {
      d = d || W.ctx.state.previewFx || null;
      var dis = W.ctx.editing || !d || !d.connected || d.readOnly || !!d.locked;
      if (!d) note("Connecting…");
      else if (d.error) note(d.error, true);
      else if (d.locked) note(d.locked, true);
      else if (d.readOnly) note("Read only");
      else if (!noteEl.classList.contains("bad")) note("");
      if (d && d.slots) { if (first) { panel.set(sections(d), dis); first = false; } else panel.update(sections(d), dis); }
      t = setTimeout(poll, 1000);
    });
  }
  W.on(function (kind) {
    if (kind === "init") { first = true; poll(); }
    if (kind === "size") panel.draw();
  });
  window.addEventListener("resize", function () { panel.draw(); });
})();
