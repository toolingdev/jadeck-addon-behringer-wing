// Shared bridge code for the WING frame widgets (design/WIDGETS-V2.md):
// init (may arrive again), state, size, theme from the host; action and
// resource requests to it. Plain ES5-ish JS, no build step. The controls
// (header, buttons, fader strip, knob, LED meter, segmented, lock) are the
// shared jadeck frame kit's (window.JK, from /api/kit/v1/kit.js, loaded
// before this; design/WIDGETS-V2.md "Shared frame kit").
(function () {
  "use strict";
  var JK = window.JK;
  if (!JK) { // a jadeck from before the shared frame kit
    document.body.textContent = "This widget needs a newer jadeck (0.6 or later).";
    document.body.style.cssText = "color:#8d96a5;font:12px system-ui,sans-serif;display:flex;align-items:center;justify-content:center;text-align:center;height:100%;margin:0;padding:8px";
    return;
  }
  // header: title · note … tools (the note and tools keep their old ids)
  var hdr = JK.header({ title: document.body.getAttribute("data-title") || "" });
  hdr.el.querySelector(".jk-note").id = "note";
  hdr.right.id = "tools";
  var host = window.parent, seq = 0, pending = {};
  var ctx = { state: {}, settings: {}, editing: false, size: { w: 0, h: 0 }, ready: false };
  var listeners = [];

  function applyTheme(theme) { JK.theme(theme); }
  function emit(kind) { for (var i = 0; i < listeners.length; i++) listeners[i](kind, ctx); }

  window.addEventListener("message", function (e) {
    if (e.source !== host) return;
    var m = e.data || {};
    switch (m.t) {
      case "init":
        ctx.state = m.state || {};
        ctx.settings = m.settings || {};
        ctx.editing = !!m.editing;
        ctx.touch = !!m.touch;
        if (m.size) ctx.size = m.size;
        applyTheme(m.theme);
        ctx.ready = true;
        document.body.classList.toggle("editing", ctx.editing);
        JK.setEditing(ctx.editing);
        emit("init");
        break;
      case "state": ctx.state = m.state || {}; emit("state"); break;
      case "size": if (m.size) ctx.size = m.size; emit("size"); break;
      case "theme": applyTheme(m.theme); emit("theme"); break;
      case "result":
        if (pending[m.id]) { pending[m.id](m); delete pending[m.id]; }
        break;
    }
  });

  function call(msg, timeoutMs) {
    msg.id = "w" + (++seq);
    return new Promise(function (resolve) {
      var t = setTimeout(function () { delete pending[msg.id]; resolve({ ok: false, error: "timeout" }); }, timeoutMs || 4000);
      pending[msg.id] = function (r) { clearTimeout(t); resolve(r); };
      host.postMessage(msg, "*");
    });
  }

  // cssVar reads a theme colour (with a fallback).
  function cssVar(name, d) {
    var v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
    return v || d;
  }

  window.Wing = {
    ctx: ctx,
    hdr: hdr,
    note: function (text, bad) { hdr.note(text, bad); },
    // on registers a listener; one added after init gets it right away.
    on: function (fn) { listeners.push(fn); if (ctx.ready) setTimeout(function () { fn("init", ctx); }, 0); },
    action: function (name, params) { return call({ t: "action", name: name, params: params || {} }); },
    // live fetches the add-on's live data; null when it can't (preview).
    live: function (ids, meters, tap) {
      // t: a 50 ms time bucket, so an older host's 2 s cache can't serve stale levels
      var path = "res:live?ids=" + encodeURIComponent(ids.join(",")) + "&meters=" + (meters ? 1 : 0) +
        "&tap=" + encodeURIComponent(tap || "post") + "&t=" + Math.floor(Date.now() / 50);
      return call({ t: "resource", path: path }, 3000).then(function (r) {
        if (!r.ok || !r.blob) return null;
        return r.blob.text().then(function (s) { try { return JSON.parse(s); } catch (e) { return null; } });
      });
    },
    // resource fetches a JSON resource; null when it can't (preview).
    resource: function (path) {
      return call({ t: "resource", path: path }, 4000).then(function (r) {
        if (!r.ok || !r.blob) return null;
        return r.blob.text().then(function (s) { try { return JSON.parse(s); } catch (e) { return null; } });
      });
    },
    log: function (text) { host.postMessage({ t: "log", text: String(text) }, "*"); },
    cssVar: cssVar,
    // stripIds: the widget's own pick, else the connection's default list.
    stripIds: function (fallbackKey, onlyKind) {
      var s = ctx.settings && ctx.settings.strips, ids = [];
      if (Array.isArray(s)) ids = s.filter(function (x) { return typeof x === "string" && x; });
      else if (typeof s === "string" && s.trim()) ids = s.split(",").map(function (x) { return x.trim(); }).filter(Boolean);
      if (!ids.length && Array.isArray(ctx.state[fallbackKey])) ids = ctx.state[fallbackKey].slice();
      if (onlyKind) ids = ids.filter(function (x) { return x.indexOf(onlyKind + ":") === 0; });
      return ids.slice(0, 64);
    },
    // The fader scale (+10 at the top, 0 dB at 78 %, -50 at 10 %, -inf at
    // the bottom; design/wing-polished.svg), the LED meters and peak hold
    // are the shared frame kit's. Drawing only; levels go to the console
    // in dB.
    law: JK.laws.console,
    led: function (cv, cols, segs) { JK.led(cv, cols, { segs: segs, law: JK.laws.console }); },
    peaks: JK.peaks,
    Knob: JK.Knob,
    dbText: function (db) {
      if (db == null) return "";
      if (db <= -143.9) return "-inf";
      return (db > 0.04 ? "+" : "") + db.toFixed(1);
    },
  };

  // ready once every page script has run, so the first init has listeners
  function ready() { host.postMessage({ t: "ready" }, "*"); }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", ready);
  else ready();
})();
