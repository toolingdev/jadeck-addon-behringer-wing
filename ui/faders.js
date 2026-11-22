// Faders widget (and DCA faders: <body data-set="dca">), drawn after
// design/wing-polished.svg with the shared jadeck frame kit's fader strip
// (JK.Strip, /api/kit/v1). Any number of strips in the widget's order;
// wide boxes get one row, tall or narrow ones wrap into rows; strips are
// never narrower than 56 px nor wider than a console strip (160 px; one or
// two strips sit centred in a big box) and nothing scrolls.
// Each strip: the console's colour band, "CH 5" (+ ST chip when the
// console says stereo), name, preamp GAIN knob (or NO PREAMP), dB scale,
// fader, LED meter (L/R for stereo), level pill, MUTE (with what else
// mutes it) and S (solo). The kit does the drawing and the fader / knob
// handling; this file says what to send to the console.
(function () {
  "use strict";
  var W = window.Wing, JK = window.JK;
  if (!W) return;
  var isDca = document.body.dataset.set === "dca";
  var stripsEl = document.getElementById("strips"), moreEl = document.getElementById("more");
  var ids = [], rows = {}, data = null, timer = 0, busy = false, layoutKey = "", raf = 0;
  var actErr = null;
  var GAIN = { min: -3, max: 45.5 }; // the doc's widest preamp range (AES50); local is -2.5..45

  function opts() {
    var s = W.ctx.settings || {};
    return { tap: s.tap === "pre" ? "pre" : "post", meters: s.meters !== false, mute: s.mute !== false, readout: s.readout !== false,
      scale: s.scale !== "none", gain: s.gain !== false, gainEdit: s.gainEdit === true };
  }
  function pickIds() { return isDca ? W.stripIds("defaultDcas", "dca") : W.stripIds("defaultFaders"); }

  function layout(n) {
    var o = opts();
    return JK.stripLayout(stripsEl.clientWidth || W.ctx.size.w || window.innerWidth, stripsEl.clientHeight || 300, n,
      { readout: o.readout, mute: o.mute });
  }

  function canWrite() { return !W.ctx.editing && data && data.connected && !data.readOnly && !data.locked; }

  function build() {
    stripsEl.textContent = "";
    rows = {};
    if (!ids.length) {
      JK.el("div", "jk-empty", stripsEl, isDca ? "No DCAs picked (widget settings → DCAs)." : "No strips picked (widget settings → Strips).");
      moreEl.textContent = "";
      stripsEl.style.gridTemplateColumns = stripsEl.style.gridTemplateRows = "minmax(0, 1fr)";
      layoutKey = "";
      return;
    }
    var L = layout(ids.length), o = opts();
    layoutKey = L.rows + "x" + L.cols + ":" + L.shown;
    JK.stripGrid(stripsEl, L);
    moreEl.textContent = ids.length > L.shown ? "+" + (ids.length - L.shown) + " more: make the widget bigger" : "";
    ids.slice(0, L.shown).forEach(function (id) {
      var gain = o.gain && !isDca ? {
        min: GAIN.min, max: GAIN.max, step: 0.5, edit: o.gainEdit,
        title: function (edit) { return edit ? "Drag to change the preamp gain" : "Gain (turn on “Edit gain” in the widget settings to change it here)"; },
      } : null;
      var st = new JK.Strip(stripsEl, {
        width: L.colW, height: L.rowH, law: W.law, scale: o.scale, meter: o.meters, readout: o.readout, mute: o.mute, gain: gain,
        canWrite: canWrite,
        onLevel: function (db, final) {
          W.action("set_fader", { strip: id, db: db }).then(function (res) { if (!res.ok && final) note(res.error); });
        },
        onMute: function () { return toggle(id, "mute"); },
        onSolo: function () { return toggle(id, "solo"); },
        onGain: function (db, final) {
          W.action("set_param", { path: "/" + id.replace(":", "/") + "/in/set/$g", value: db }).then(function (res) {
            if (!res.ok && final) note(res.error);
          });
        },
      });
      rows[id] = { st: st, kind: id.split(":")[0] };
    });
  }

  function toggle(id, what) {
    return W.action(what, { strip: id, mode: "toggle" }).then(function (res) {
      if (!res.ok) note(res.error);
      poll(true);
    });
  }

  function note(text) { actErr = { text: text, until: Date.now() + 6000 }; W.note(text, true); }

  var byId = {};
  // paint updates what changes (levels, meters) without rebuilding.
  function paint() {
    var o = opts(), now = Date.now();
    Object.keys(rows).forEach(function (id) {
      var r = rows[id], s = byId[id] || { tag: id.replace(":", " ").toUpperCase(), color: "#707070" };
      r.st.set({
        tag: s.tag, name: s.name, color: s.color || "#707070", stereo: s.stereo, db: s.db, edb: s.edb,
        muted: s.muted, emuted: s.emuted, mutedBy: s.muteBy, solo: s.solo, soloable: r.kind !== "mgrp",
        gain: r.kind === "ch" || r.kind === "aux" ? s.gain : null, l: s.l, r: s.r,
      });
    });
    if (actErr && now < actErr.until) W.note(actErr.text, true);
    else if (!data) W.note("Connecting…");
    else if (data.error) W.note(data.error, true);
    else if (data.locked) W.note(data.locked, true);
    else if (data.readOnly) W.note("Read only");
    else if (o.meters && data.meters && !data.meters.running && data.meters.note) W.note(data.meters.note);
    else W.note(data.transport === "polling" ? "checking every 2 s" : "");
  }
  function render() {
    byId = {};
    ((data && data.strips) || []).forEach(function (s) { byId[s.id] = s; });
    cancelAnimationFrame(raf);
    raf = requestAnimationFrame(paint);
  }

  function poll(now) {
    clearTimeout(timer);
    if (busy && !now) return;
    if (document.hidden || !W.ctx.ready) { timer = setTimeout(poll, 500); return; }
    busy = true;
    var o = opts(), started = Date.now();
    W.live(ids, o.meters, o.tap).then(function (d) {
      busy = false;
      data = d || W.ctx.state.previewLive || null;
      render();
      timer = setTimeout(poll, Math.max(0, (o.meters ? 50 : 250) - (Date.now() - started))); // ~20 Hz with meters
    });
  }

  function refresh(force) {
    var next = pickIds(), L = next.length ? layout(next.length) : null;
    var key = L ? L.rows + "x" + L.cols + ":" + L.shown : "";
    if (force || next.join() !== ids.join() || key !== layoutKey) { ids = next; build(); render(); }
    else { Object.keys(rows).forEach(function (id) { rows[id].st.fit(); }); render(); }
  }
  W.on(function (kind) {
    if (kind === "init") { refresh(true); poll(true); }
    else if (kind === "size" || kind === "state") refresh(false);
  });
  window.addEventListener("resize", function () { refresh(false); });
  document.addEventListener("visibilitychange", function () { if (!document.hidden) poll(true); });
})();
