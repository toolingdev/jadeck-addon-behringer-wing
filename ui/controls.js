// Parameter panels for the Channel strip and FX widgets: tabs, a page of
// cells that fits the box (a pager instead of scrolling), a knob for every
// continuous value, ON/OFF for switches and a list for choices, and a
// lock that must be opened before anything changes (it closes again 30 s
// after the last touch). Values shown are always the console's: after a
// write the add-on reads the value back and the cell shows that.
(function () {
  "use strict";
  var W = window.Wing, law = W.law;
  var CELL_W = 94, CELL_H = 118; // grid cell incl. gap
  var RELOCK_MS = 30000;

  function el(tag, cls, parent, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    if (parent) parent.appendChild(e);
    return e;
  }

  // Friendly names for the parameters the doc lists (default models).
  var NAMES = {
    "in/set/$g": "Gain", "in/set/trim": "Trim", "in/set/bal": "Balance", "in/set/inv": "Polarity",
    "flt/lc": "Low cut", "flt/lcf": "Low cut", "flt/lcs": "LC slope", "flt/hc": "High cut", "flt/hcf": "High cut", "flt/hcs": "HC slope",
    "eq/on": "EQ", "eq/mdl": "EQ model", "eq/lg": "Low gain", "eq/lf": "Low freq", "eq/lq": "Low Q", "eq/leq": "Low type",
    "eq/hg": "High gain", "eq/hf": "High freq", "eq/hq": "High Q", "eq/heq": "High type", "eq/mix": "EQ mix",
    "gate/on": "Gate", "gate/mdl": "Gate model", "gate/thr": "Threshold", "gate/range": "Range", "gate/att": "Attack", "gate/hld": "Hold", "gate/rel": "Release",
    "dyn/on": "Comp", "dyn/mdl": "Comp model", "dyn/thr": "Threshold", "dyn/ratio": "Ratio", "dyn/att": "Attack", "dyn/rel": "Release", "dyn/hld": "Hold",
    "dyn/gain": "Makeup", "dyn/knee": "Knee", "dyn/mix": "Mix", "dyn/det": "Detect", "dyn/env": "Envelope", "dyn/auto": "Auto",
    "pan": "Pan", "wid": "Width", "preins/on": "Pre insert", "preins/ins": "Pre insert FX", "postins/on": "Post insert", "postins/ins": "Post insert FX",
    "fxmix": "Mix", "dcy": "Decay", "pdel": "Pre-delay", "size": "Size", "time": "Time", "feed": "Feedback", "damp": "Damping", "lc": "Low cut", "hc": "High cut",
  };
  function nameOf(p) {
    var rel = p.path.replace(/^\/(ch|aux|bus|main|mtx|fx)\/[0-9]+\//, "");
    var m = /^eq\/([1-8])([gfq])$/.exec(rel);
    if (m) return "Mid " + m[1] + " " + { g: "gain", f: "freq", q: "Q" }[m[2]];
    m = /^(send\/[^/]+|main\/[1-4])\/(on|lvl|mode|pre)$/.exec(rel);
    if (m) return { on: "On", lvl: "Level", mode: "Tap", pre: "Pre" }[m[2]];
    return NAMES[rel] || NAMES[p.name] || p.label || p.name;
  }
  // knob colour: amber for gains, blue for filters, grey for frequencies
  function colorOf(p) {
    var rel = p.path.replace(/^\/(ch|aux|bus|main|mtx|fx)\/[0-9]+\//, "");
    if (/^flt\//.test(rel) || /^(lc|hc)$/.test(p.name)) return "#3e8ede";
    if (/f$|freq/.test(p.name) || p.unit === "Hz") return "#8a93a0";
    if (/g$|gain|trim|lvl|\$g|fxmix/.test(p.name) || p.unit === "dB" || p.kind === "fader") return "#f5a524";
    return "#8a93a0";
  }
  function bipolar(p) { return p.kind !== "fader" && p.min < 0 && p.max > 0; }

  // ------------------------------------------------------------ value maps
  function toPos(p, v) {
    if (v == null || isNaN(v)) return 0;
    if (p.kind === "fader") return law.toPos(v);
    if (p.max <= p.min) return 0;
    if (p.log && p.min > 0) return Math.log(v / p.min) / Math.log(p.max / p.min);
    return (v - p.min) / (p.max - p.min);
  }
  function fromPos(p, x) {
    x = Math.min(1, Math.max(0, x));
    if (p.kind === "fader") return law.toDb(x);
    var v = p.log && p.min > 0 ? p.min * Math.pow(p.max / p.min, x) : p.min + (p.max - p.min) * x;
    v = Math.min(p.max, Math.max(p.min, v));
    if (p.kind === "int") return Math.round(v);
    if (!p.log && p.steps > 1) { var st = (p.max - p.min) / (p.steps - 1); v = p.min + Math.round((v - p.min) / st) * st; }
    return Math.round(v * 1000) / 1000;
  }
  function fmt(p, v) {
    if (v == null) return "–";
    if (p.kind === "fader") return v <= -143.9 ? "−∞" : (v > 0.04 ? "+" : "") + v.toFixed(1) + " dB";
    var a = Math.abs(v), n;
    if (p.kind === "int") n = String(Math.round(v));
    else if (p.unit === "Hz" && a >= 1000) n = (v / 1000).toFixed(a >= 10000 ? 1 : 2).replace(/\.?0+$/, "") + " k";
    else n = a >= 100 ? v.toFixed(0) : a >= 10 ? v.toFixed(1) : v.toFixed(2).replace(/0$/, "");
    if (p.unit === "dB" && v > 0) n = "+" + n;
    return n + (p.unit ? (p.unit === "Hz" && a >= 1000 ? "Hz" : " " + p.unit) : "");
  }

  // ---------------------------------------------------------------- panel
  // Panel(root, onSet): onSet(param, value) → Promise<{ok, error, result}>.
  function Panel(root, onSet) {
    var self = this;
    this.root = root;
    this.onSet = onSet;
    this.tab = null;
    this.page = 0;
    this.locked = true;
    this.lastTouch = 0;
    this.busy = {};
    this.tabsEl = el("div", "tabs", root);
    this.cellsEl = el("div", "cells", root);
    this.pagerEl = el("div", "pager", root);
    this.prev = el("button", "pg", this.pagerEl, "◀");
    this.pageText = el("span", "pgt", this.pagerEl);
    this.next = el("button", "pg", this.pagerEl, "▶");
    this.prev.type = this.next.type = "button";
    this.prev.onclick = function () { self.page--; self.draw(); };
    this.next.onclick = function () { self.page++; self.draw(); };
    this.hintEl = el("div", "hint", root, "Drag up/down to turn · Shift or two fingers: fine · double-tap resets · values read back from the console");
    setInterval(function () { if (!self.locked && Date.now() - self.lastTouch > RELOCK_MS) self.setLocked(true); }, 1000);
  }
  Panel.prototype.setLocked = function (on) {
    this.locked = on;
    this.lastTouch = Date.now();
    if (this.onLock) this.onLock(on);
    this.draw();
  };
  Panel.prototype.touch = function () { this.lastTouch = Date.now(); };
  Panel.prototype.set = function (sections, disabled) {
    this.sections = sections || [];
    this.disabled = disabled;
    var self = this;
    if (!this.tab || !this.sections.some(function (s) { return s.id === self.tab; })) this.tab = this.sections.length ? this.sections[0].id : null;
    this.draw();
  };
  Panel.prototype.draw = function () {
    var self = this;
    this.tabsEl.textContent = "";
    this.tabsEl.style.display = this.sections && this.sections.length > 1 ? "" : "none";
    (this.sections || []).forEach(function (s) {
      var b = el("button", "tab" + (s.id === self.tab ? " on" : ""), self.tabsEl, s.title);
      b.type = "button";
      b.onclick = function () { self.tab = s.id; self.page = 0; self.draw(); };
    });
    var sec = (this.sections || []).filter(function (s) { return s.id === self.tab; })[0];
    var params = sec ? sec.params : [];
    this.pagerEl.style.display = "none";
    this.cellsEl.textContent = "";
    var w = this.cellsEl.clientWidth, h = this.cellsEl.clientHeight;
    var per = Math.max(1, Math.floor((w + 6) / CELL_W) * Math.max(1, Math.floor((h + 6) / CELL_H)));
    var pages = Math.max(1, Math.ceil(params.length / per));
    if (pages > 1) { // the pager takes a row's room
      this.pagerEl.style.display = "";
      h = this.cellsEl.clientHeight;
      per = Math.max(1, Math.floor((w + 6) / CELL_W) * Math.max(1, Math.floor((h + 6) / CELL_H)));
      pages = Math.max(1, Math.ceil(params.length / per));
    }
    this.page = Math.min(Math.max(0, this.page), pages - 1);
    this.pageText.textContent = (this.page + 1) + " / " + pages;
    this.prev.disabled = this.page === 0;
    this.next.disabled = this.page >= pages - 1;
    this.cells = {};
    if (!params.length) { el("div", "empty", this.cellsEl, sec ? "Nothing to show here for this strip." : "Nothing to show."); return; }
    params.slice(this.page * per, this.page * per + per).forEach(function (p) { self.cell(p); });
  };
  Panel.prototype.cell = function (p) {
    var self = this, c = el("div", "cell", this.cellsEl);
    var canEdit = p.writable && !this.locked && !this.disabled;
    c.title = (p.group ? p.group + " · " : "") + nameOf(p) + " (" + p.path + ")";
    if (p.group) el("div", "grp", c, p.group);
    function send(v) {
      self.touch();
      self.busy[p.path] = true;
      return self.onSet(p, v).then(function (res) {
        delete self.busy[p.path];
        if (res.ok && res.result) { p.value = res.result.value; p.text = res.result.text; }
        else if (!res.ok) self.error(res.error);
        return res;
      });
    }
    if (p.kind === "bool") {
      var b = el("button", "pon" + (p.value >= 1 ? " on" : ""), c, p.value >= 1 ? "ON" : "OFF");
      b.type = "button";
      b.disabled = !canEdit;
      b.onclick = function () {
        b.disabled = true;
        send(p.value >= 1 ? 0 : 1).then(function () { b.disabled = !canEdit; b.classList.toggle("on", p.value >= 1); b.textContent = p.value >= 1 ? "ON" : "OFF"; });
      };
      el("div", "kl", c, nameOf(p));
      return;
    }
    if (p.kind === "enum") {
      var sel = el("select", "psel", c);
      (p.items || []).forEach(function (it) { var o = el("option", "", sel, it); o.value = it; });
      sel.value = p.value;
      sel.disabled = !canEdit || !p.writable;
      sel.onchange = function () { send(sel.value).then(function () { sel.value = p.value; }); };
      el("div", "kl", c, nameOf(p));
      return;
    }
    if (p.kind === "text") {
      el("div", "ptext", c, p.text || (p.value != null ? String(p.value) : "–"));
      el("div", "kl", c, nameOf(p));
      return;
    }
    var big = /\$g$|trim$/.test(p.path);
    var k = new W.Knob(c, { size: big ? 64 : 50, color: colorOf(p), bipolar: bipolar(p), label: nameOf(p),
      def: bipolar(p) ? toPos(p, 0) : p.kind === "fader" ? law.toPos(0) : null });
    k.set(toPos(p, p.value), fmt(p, p.value));
    k.setDisabled(!canEdit);
    var last = 0, t = 0;
    k.onTurn = function (pos, final) {
      var v = fromPos(p, pos);
      k.show(fmt(p, v));
      self.touch();
      clearTimeout(t);
      var now = Date.now();
      if (!final && now - last < 250) { t = setTimeout(function () { k.onTurn(pos, false); }, 250 - (now - last)); return; }
      last = now;
      send(v).then(function (res) { if (final && res.ok) k.set(toPos(p, p.value), fmt(p, p.value)); });
    };
    this.cells[p.path] = { p: p, knob: k };
  };
  Panel.prototype.error = function (e) { if (this.onError) this.onError(e); };
  // update takes polled values: knobs move in place; a changed shape redraws.
  Panel.prototype.update = function (sections, disabled) {
    if (Date.now() - this.lastTouch < 1500 || Object.keys(this.busy).length) return; // a finger is on it
    var shape = function (ss) { return JSON.stringify((ss || []).map(function (s) { return [s.id, s.params.map(function (p) { return p.path + p.kind + p.writable; })]; })); };
    if (shape(sections) !== shape(this.sections) || disabled !== this.disabled) { this.set(sections, disabled); return; }
    var self = this;
    sections.forEach(function (s) {
      s.params.forEach(function (p) {
        var c = self.cells && self.cells[p.path];
        if (c) { c.p.value = p.value; c.knob.set(toPos(p, p.value), fmt(p, p.value)); }
      });
    });
    var redraw = false;
    sections.forEach(function (s, i) {
      s.params.forEach(function (p, j) {
        var old = self.sections[i].params[j];
        if (p.kind !== "float" && p.kind !== "int" && p.kind !== "fader" && old.value !== p.value) redraw = true;
      });
    });
    this.sections = sections;
    if (redraw) this.draw();
  };

  // lockButton draws the lock chip into parent and wires it to panel.
  function lockButton(parent, panel) {
    var b = el("button", "lock", parent);
    b.type = "button";
    function paint(on) { b.textContent = on ? "LOCKED · TAP TO EDIT" : "EDITING · TAP TO LOCK"; b.classList.toggle("open", !on); b.title = on ? "Tap to change values (locks again after 30 s)" : "Tap to lock"; }
    panel.onLock = paint;
    paint(true);
    b.onclick = function () { panel.setLocked(!panel.locked); };
    return b;
  }

  W.Panel = Panel;
  W.lockButton = lockButton;
  W.el = el;
})();
