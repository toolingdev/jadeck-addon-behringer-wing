// Faders widget (and DCA faders: <body data-set="dca">), drawn after
// design/wing-polished.svg. Any number of strips in the widget's order;
// wide boxes get one row, tall or narrow ones wrap into rows; strips are
// never narrower than 56 px and nothing scrolls.
// Each strip: the console's colour band, "CH 5" (+ ST chip when the
// console says stereo), name, preamp GAIN knob (or NO PREAMP), dB scale,
// fader, LED meter (L/R for stereo), level pill, MUTE (with what else
// mutes it) and S (solo).
// Fader: drag the cap or anywhere on the track (relative, no jump);
// Shift or a second finger = fine; double-tap = 0 dB. Sends at most ~7 a
// second (the bridge allows 10 actions/s) and once more on release.
(function () {
  "use strict";
  var W = window.Wing, law = W.law, body = document.body, isDca = body.dataset.set === "dca";
  var stripsEl = document.getElementById("strips"), moreEl = document.getElementById("more"), noteEl = document.getElementById("note");
  var peaks = W.peaks();
  var ids = [], rows = {}, data = null, timer = 0, busy = false, drag = {}, layoutKey = "", raf = 0;
  var actErr = null;
  var GAIN = { min: -3, max: 45.5 }; // the doc's widest preamp range (AES50); local is -2.5..45

  function opts() {
    var s = W.ctx.settings || {};
    return { tap: s.tap === "pre" ? "pre" : "post", meters: s.meters !== false, mute: s.mute !== false, readout: s.readout !== false,
      scale: s.scale !== "none", gain: s.gain !== false, gainEdit: s.gainEdit === true };
  }
  function pickIds() { return isDca ? W.stripIds("defaultDcas", "dca") : W.stripIds("defaultFaders"); }
  function el(tag, cls, parent, text) { var e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; if (parent) parent.appendChild(e); return e; }

  // minimum height of a strip for the parts it shows
  function minH(o, withGain) { return 64 + (withGain ? 96 : 0) + 140 + (o.readout ? 30 : 0) + (o.mute ? 68 : 0); }

  function layout(n) {
    var o = opts(), w = stripsEl.clientWidth || W.ctx.size.w || window.innerWidth, h = stripsEl.clientHeight || 300, gap = 10, minW = 56;
    var mh = minH(o, false), maxRows = Math.max(1, Math.floor((h + gap) / (mh + gap)));
    for (var r = 1; r <= maxRows; r++) {
      var cols = Math.ceil(n / r);
      if ((w - gap * (cols - 1)) / cols >= minW) return { rows: r, cols: cols, shown: n, rowH: (h - gap * (r - 1)) / r, colW: (w - gap * (cols - 1)) / cols };
    }
    var c = Math.max(1, Math.floor((w + gap) / (minW + gap)));
    return { rows: maxRows, cols: c, shown: Math.min(n, c * maxRows), rowH: (h - gap * (maxRows - 1)) / maxRows, colW: (w - gap * (c - 1)) / c };
  }

  function build() {
    stripsEl.textContent = "";
    rows = {};
    if (!ids.length) {
      el("div", "empty", stripsEl, isDca ? "No DCAs picked (widget settings → DCAs)." : "No strips picked (widget settings → Strips).");
      moreEl.textContent = "";
      layoutKey = "";
      return;
    }
    var L = layout(ids.length), o = opts();
    layoutKey = L.rows + "x" + L.cols + ":" + L.shown;
    stripsEl.style.gridTemplateColumns = "repeat(" + L.cols + ", minmax(0, 1fr))";
    stripsEl.style.gridTemplateRows = "repeat(" + L.rows + ", minmax(0, 1fr))";
    moreEl.textContent = ids.length > L.shown ? "+" + (ids.length - L.shown) + " more: make the widget bigger" : "";
    var showGain = o.gain && !isDca && L.rowH >= minH(o, true);
    ids.slice(0, L.shown).forEach(function (id) {
      var r = { id: id, kind: id.split(":")[0] };
      r.root = el("div", "card", stripsEl);
      r.root.classList.toggle("narrow", L.colW < 104);
      r.root.classList.toggle("short", !showGain);
      r.band = el("div", "band", r.root);
      var hdr = el("div", "hdr", r.root), nr = el("div", "numrow", hdr);
      r.num = el("div", "num", nr);
      r.st = el("span", "chip-st", nr, "ST");
      r.nm = el("div", "nm", hdr);
      r.gainbox = el("div", "gainbox", r.root);
      if (showGain && (r.kind === "ch" || r.kind === "aux")) {
        r.knob = new W.Knob(r.gainbox, { size: 50, color: "#f5a524", label: "Gain" });
        r.knob.onTurn = function (pos, final) { gainTurn(r, pos, final); };
        r.nopre = el("div", "nopre", r.gainbox, "NO PREAMP");
      } else if (showGain) {
        el("div", "nopre", r.gainbox, "NO PREAMP");
      }
      var fz = el("div", "fz", r.root);
      r.fz = fz;
      r.ticks = [];
      if (o.scale) {
        var sc = el("div", "scale", fz);
        law.ticks.forEach(function (db) {
          var t = el("div", "tick" + (db === 0 ? " z" : ""), sc);
          el("b", "", t, db <= -144 ? "−∞" : db > 0 ? "+" + db : db < 0 ? "−" + (-db) : "0");
          el("i", "", t);
          t.dataset.db = db;
          r.ticks.push(t);
        });
      }
      el("div", "rail", fz);
      r.hit = el("div", "hit", fz);
      r.cap = el("div", "cap", fz);
      r.meter = el("canvas", "meter", fz);
      r.lr = el("div", "lr", fz);
      el("span", "", r.lr, "L"); el("span", "", r.lr, "R");
      r.meter.style.display = r.lr.style.display = o.meters ? "" : "none";
      r.pill = el("div", "pill", r.root);
      r.pill.style.display = o.readout ? "" : "none";
      var btns = el("div", "btns", r.root);
      btns.style.display = o.mute ? "" : "none";
      r.mute = el("button", "wbtn mute", btns, "MUTE");
      r.solo = el("button", "wbtn solo", btns, "S");
      r.mute.type = r.solo.type = "button";
      r.solo.style.display = L.colW < 96 ? "none" : "";
      r.mute.addEventListener("click", function () { toggle(r, "mute", r.mute); });
      r.solo.addEventListener("click", function () { toggle(r, "solo", r.solo); });
      fader(r);
      rows[id] = r;
    });
    placeTicks();
  }

  function placeTicks() {
    Object.keys(rows).forEach(function (id) {
      var r = rows[id], h = r.fz.clientHeight;
      r.trackH = h;
      r.ticks.forEach(function (t) { t.style.top = ((1 - law.toPos(Number(t.dataset.db))) * h) + "px"; });
    });
  }

  function canWrite() { return !W.ctx.editing && data && data.connected && !data.readOnly && !data.locked; }

  function toggle(r, what, btn) {
    if (!canWrite()) return;
    btn.disabled = true;
    W.action(what, { strip: r.id, mode: "toggle" }).then(function (res) {
      btn.disabled = false;
      if (!res.ok) note(res.error);
      poll(true);
    });
  }

  var gainT = {};
  function gainTurn(r, pos, final) {
    var db = Math.round((GAIN.min + (GAIN.max - GAIN.min) * pos) * 2) / 2;
    r.knob.show((db > 0 ? "+" : "") + db.toFixed(1) + " dB");
    if (!canWrite() || !opts().gainEdit) return;
    var now = Date.now();
    if (!final && now - (gainT[r.id] || 0) < 250) return;
    gainT[r.id] = now;
    W.action("set_param", { path: "/" + r.id.replace(":", "/") + "/in/set/$g", value: db }).then(function (res) {
      if (!res.ok && final) note(res.error);
    });
  }

  function fader(r) {
    var lastSent = 0, pendingT = 0, startY = 0, startPos = 0, pointers = 0, lastTap = 0, moved = false;
    function send(final) {
      clearTimeout(pendingT);
      var d = drag[r.id];
      if (!d) return;
      var now = Date.now();
      if (!final && now - lastSent < 140) { pendingT = setTimeout(function () { send(false); }, 140 - (now - lastSent)); return; }
      lastSent = now;
      W.action("set_fader", { strip: r.id, db: law.toDb(d.pos) }).then(function (res) {
        if (!res.ok && final) note(res.error);
      });
    }
    r.hit.addEventListener("pointerdown", function (ev) {
      if (!canWrite()) return;
      pointers++;
      r.hit.setPointerCapture(ev.pointerId);
      var s = byId[r.id] || {};
      startY = ev.clientY;
      startPos = drag[r.id] ? drag[r.id].pos : law.toPos(s.db == null ? -144 : s.db);
      moved = false;
      var now = Date.now();
      if (now - lastTap < 320) { // double-tap: 0 dB
        lastTap = 0;
        drag[r.id] = { pos: law.toPos(0), active: false, until: now + 1500 };
        paint();
        send(true);
        return;
      }
      lastTap = now;
      drag[r.id] = { pos: startPos, active: true, until: 0 };
    });
    r.hit.addEventListener("pointermove", function (ev) {
      var d = drag[r.id];
      if (!d || !d.active) return;
      var fine = ev.shiftKey || pointers > 1 ? 0.2 : 1;
      var p = startPos + (startY - ev.clientY) / Math.max(40, r.trackH || 200) * fine;
      if (Math.abs(startY - ev.clientY) > 2) moved = true;
      d.pos = Math.min(1, Math.max(0, p));
      paint();
      send(false);
    });
    function up() {
      pointers = Math.max(0, pointers - 1);
      var d = drag[r.id];
      if (!d || !d.active || pointers > 0) return;
      d.active = false;
      d.until = Date.now() + 1500; // keep the finger's value until the console catches up
      if (moved) send(true);
    }
    r.hit.addEventListener("pointerup", up);
    r.hit.addEventListener("pointercancel", up);
  }

  function note(text) { actErr = { text: text, until: Date.now() + 6000 }; show(text, true); }
  function show(text, bad) { noteEl.textContent = text || ""; noteEl.classList.toggle("bad", !!bad); }

  var byId = {};
  // paint updates what changes (levels, meters) without rebuilding.
  function paint() {
    var o = opts(), now = Date.now(), off = !canWrite();
    Object.keys(rows).forEach(function (id) {
      var r = rows[id], s = byId[id] || { tag: id.replace(":", " ").toUpperCase(), color: "#707070" };
      r.band.style.background = s.color || "#707070";
      r.num.textContent = s.tag || "";
      r.st.style.display = s.stereo ? "" : "none";
      r.nm.textContent = s.name || "";
      r.root.title = (s.tag || "") + (s.stereo ? " (stereo)" : "") + (s.name ? " · " + s.name : "");
      r.root.classList.toggle("muted", !!s.emuted);
      r.root.classList.toggle("off", off);
      var d = drag[id];
      if (d && !d.active && now > d.until) { delete drag[id]; d = null; }
      var pos = d ? d.pos : law.toPos(s.db == null ? -144 : s.db);
      r.cap.style.transform = "translateY(" + ((1 - pos) * (r.trackH || 0)).toFixed(1) + "px)";
      var db = d ? law.toDb(pos) : s.db;
      r.pill.textContent = db == null ? "–" : (db <= -143.9 ? "−∞" : (db > 0.04 ? "+" : "") + db.toFixed(1) + " dB");
      r.pill.classList.toggle("live", !!d);
      r.pill.title = s.edb != null && s.db != null && Math.abs(s.edb - s.db) > 0.05 ? "After DCA: " + W.dbText(s.edb) + " dB" : "";
      var by = s.emuted && !s.muted;
      r.mute.classList.toggle("on", !!s.muted);
      r.mute.classList.toggle("by", !!by);
      r.mute.textContent = s.muted ? "MUTED" : by ? "MUTED BY " + (s.muteBy || "GROUP").toUpperCase() : "MUTE";
      r.mute.title = by ? "Muted by " + (s.muteBy || "a DCA or mute group") + ". Its own mute is off." : s.muted ? "Tap to unmute" : "Tap to mute";
      r.mute.disabled = off;
      r.solo.classList.toggle("on", !!s.solo);
      r.solo.disabled = off || r.kind === "mgrp";
      if (r.knob) {
        var has = s.gain != null;
        r.knob.el.style.display = has ? "" : "none";
        r.nopre.style.display = has ? "none" : "";
        if (has) r.knob.set((s.gain - GAIN.min) / (GAIN.max - GAIN.min), (s.gain > 0 ? "+" : "") + s.gain.toFixed(1) + " dB");
        r.knob.setDisabled(off || !o.gainEdit);
        r.knob.el.title = o.gainEdit ? "Drag to change the preamp gain" : "Gain (turn on “Edit gain” in the widget settings to change it here)";
      }
      if (o.meters) {
        r.meter.classList.toggle("st", !!s.stereo);
        r.lr.style.display = s.stereo ? "" : "none";
        var cols = [{ level: s.l, peak: peaks(id + "L", s.l) }];
        if (s.stereo) cols.push({ level: s.r, peak: peaks(id + "R", s.r) });
        W.led(r.meter, cols);
      }
    });
    if (actErr && now < actErr.until) show(actErr.text, true);
    else if (!data) show("Connecting…");
    else if (data.error) show(data.error, true);
    else if (data.locked) show(data.locked, true);
    else if (data.readOnly) show("Read only");
    else if (o.meters && data.meters && !data.meters.running && data.meters.note) show(data.meters.note);
    else show(data.transport === "polling" ? "checking every 2 s" : "");
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
    else { placeTicks(); render(); }
  }
  W.on(function (kind) {
    if (kind === "init") { refresh(true); poll(true); }
    else if (kind === "size" || kind === "state") refresh(false);
  });
  window.addEventListener("resize", function () { refresh(false); });
  document.addEventListener("visibilitychange", function () { if (!document.hidden) poll(true); });
})();
