// Rotary knob (design/wing-polished.svg): a 270° track arc, the value arc
// in the knob's colour (bipolar knobs fill from 12 o'clock), a shaded
// body with a white pointer, the value below and a small label.
// Drag up/down (or the wheel) to turn; Shift, or a second finger, for
// fine steps; double-tap resets to the knob's default (when it has one).
// The value shown while turning is the knob's; after the console answers
// the caller sets the console's value.
(function () {
  "use strict";
  var W = window.Wing, NS = "http://www.w3.org/2000/svg", uid = 0;
  var SWEEP = 135; // degrees each side of 12 o'clock

  function svg(tag, attrs, parent) {
    var e = document.createElementNS(NS, tag);
    for (var k in attrs) e.setAttribute(k, attrs[k]);
    if (parent) parent.appendChild(e);
    return e;
  }
  function pt(cx, cy, r, deg) { var a = deg * Math.PI / 180; return [cx + r * Math.sin(a), cy - r * Math.cos(a)]; }
  function arc(cx, cy, r, a0, a1) {
    if (Math.abs(a1 - a0) < 0.01) return "";
    var s = pt(cx, cy, r, Math.min(a0, a1)), e = pt(cx, cy, r, Math.max(a0, a1));
    return "M" + s[0].toFixed(2) + " " + s[1].toFixed(2) + " A" + r + " " + r + " 0 " + (Math.abs(a1 - a0) > 180 ? 1 : 0) + " 1 " + e[0].toFixed(2) + " " + e[1].toFixed(2);
  }

  // Knob(parent, {size, color, bipolar, label, def}) → {el, set(pos, text), onTurn(pos, final), disabled}
  function Knob(parent, o) {
    var self = this, size = Math.max(40, o.size || 50), r = size / 2 - 3, cx = size / 2, cy = size / 2, br = r - 6;
    this.o = o;
    this.pos = 0;
    this.el = document.createElement("div");
    this.el.className = "knob";
    parent.appendChild(this.el);
    var s = svg("svg", { width: size, height: size, viewBox: "0 0 " + size + " " + size }, this.el);
    var id = "kg" + (++uid);
    var defs = svg("defs", {}, s);
    var rg = svg("radialGradient", { id: id, cx: "0.4", cy: "0.35", r: "0.75" }, defs);
    svg("stop", { offset: "0", "stop-color": "#3a414c" }, rg);
    svg("stop", { offset: "0.6", "stop-color": "#22272f" }, rg);
    svg("stop", { offset: "1", "stop-color": "#161a1f" }, rg);
    var f = svg("filter", { id: id + "s", x: "-30%", y: "-30%", width: "160%", height: "180%" }, defs);
    svg("feDropShadow", { dx: "0", dy: "3", stdDeviation: "3", "flood-color": "#000", "flood-opacity": "0.55" }, f);
    svg("path", { d: arc(cx, cy, r, -SWEEP, SWEEP), stroke: "#262c35", "stroke-width": 4, fill: "none", "stroke-linecap": "round" }, s);
    this.val = svg("path", { d: "", stroke: o.color || "#f5a524", "stroke-width": 4, fill: "none", "stroke-linecap": "round" }, s);
    svg("circle", { cx: cx, cy: cy, r: br, fill: "url(#" + id + ")", stroke: "#3a414c", "stroke-width": 1, filter: "url(#" + id + "s)" }, s);
    this.ptr = svg("line", { stroke: "#f4f6f9", "stroke-width": 2.5, "stroke-linecap": "round" }, s);
    this.geo = { cx: cx, cy: cy, r: r, br: br };
    this.text = document.createElement("div");
    this.text.className = "kv";
    this.el.appendChild(this.text);
    if (o.label) { var l = document.createElement("div"); l.className = "kl"; l.textContent = o.label; this.el.appendChild(l); }
    this.disabled = false;

    // turning
    var startY = 0, startPos = 0, active = 0, lastTap = 0, moved = false;
    this.el.addEventListener("pointerdown", function (ev) {
      if (self.disabled) return;
      active++;
      self.el.setPointerCapture(ev.pointerId);
      startY = ev.clientY; startPos = self.pos; moved = false;
      self.el.classList.add("drag");
      var now = Date.now();
      if (now - lastTap < 320 && o.def != null) { self.turn(o.def, true); lastTap = 0; return; }
      lastTap = now;
    });
    this.el.addEventListener("pointermove", function (ev) {
      if (!active || self.disabled) return;
      var fine = ev.shiftKey || active > 1 ? 0.2 : 1;
      var p = startPos + (startY - ev.clientY) / 200 * fine;
      if (Math.abs(startY - ev.clientY) > 2) moved = true;
      self.turn(Math.min(1, Math.max(0, p)), false);
    });
    function up() {
      if (!active) return;
      active = Math.max(0, active - 1);
      if (active === 0) {
        self.el.classList.remove("drag");
        if (moved) self.turn(self.pos, true);
      }
    }
    this.el.addEventListener("pointerup", up);
    this.el.addEventListener("pointercancel", up);
    var wheelT = 0;
    this.el.addEventListener("wheel", function (ev) {
      if (self.disabled) return;
      ev.preventDefault();
      self.turn(Math.min(1, Math.max(0, self.pos - ev.deltaY / (ev.shiftKey ? 4000 : 1000))), false);
      clearTimeout(wheelT);
      wheelT = setTimeout(function () { self.turn(self.pos, true); }, 250);
    }, { passive: false });
  }
  Knob.prototype.turn = function (pos, final) {
    this.draw(pos);
    if (this.onTurn) this.onTurn(pos, final);
  };
  Knob.prototype.draw = function (pos) {
    this.pos = pos;
    var g = this.geo, a = -SWEEP + 2 * SWEEP * pos;
    var from = this.o.bipolar ? 0 : -SWEEP;
    this.val.setAttribute("d", arc(g.cx, g.cy, g.r, from, a));
    var p0 = pt(g.cx, g.cy, g.br * 0.35, a), p1 = pt(g.cx, g.cy, g.br * 0.75, a);
    this.ptr.setAttribute("x1", p0[0].toFixed(2)); this.ptr.setAttribute("y1", p0[1].toFixed(2));
    this.ptr.setAttribute("x2", p1[0].toFixed(2)); this.ptr.setAttribute("y2", p1[1].toFixed(2));
  };
  Knob.prototype.set = function (pos, text) {
    if (this.el.classList.contains("drag")) return; // a finger is on it
    this.draw(pos);
    this.text.textContent = text;
  };
  Knob.prototype.show = function (text) { this.text.textContent = text; };
  Knob.prototype.setDisabled = function (d) { this.disabled = d; this.el.classList.toggle("off", d); };

  W.Knob = Knob;
})();
