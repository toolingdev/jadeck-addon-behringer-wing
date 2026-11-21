// Shared bridge code for the WING frame widgets (design/WIDGETS-V2.md):
// init (may arrive again), state, size, theme from the host; action and
// resource requests to it. Plain ES5-ish JS, no build step.
(function () {
  "use strict";
  var host = window.parent, seq = 0, pending = {};
  var ctx = { state: {}, settings: {}, editing: false, size: { w: 0, h: 0 }, ready: false };
  var listeners = [];

  function applyTheme(theme) {
    for (var k in theme || {}) if (k.indexOf("--") === 0) document.documentElement.style.setProperty(k, theme[k]);
  }
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
    // law: the drawn fader scale (design/wing-polished.svg): +10 at the
    // top, 0 dB at 78 %, -50 at 10 %, -inf at the bottom. Drawing only;
    // levels go to the console in dB.
    law: (function () {
      var P = [[0, -144], [0.0001, -90], [0.10, -50], [0.23, -30], [0.37, -20], [0.54, -10], [0.66, -5], [0.78, 0], [0.90, 5], [1, 10]];
      return {
        toPos: function (db) {
          if (db == null || db <= -144) return 0;
          for (var i = 1; i < P.length; i++) if (db <= P[i][1]) return P[i - 1][0] + (P[i][0] - P[i - 1][0]) * (db - P[i - 1][1]) / (P[i][1] - P[i - 1][1]);
          return 1;
        },
        toDb: function (pos) {
          if (pos <= 0) return -144;
          for (var i = 1; i < P.length; i++) if (pos <= P[i][0]) return Math.round((P[i - 1][1] + (P[i][1] - P[i - 1][1]) * (pos - P[i - 1][0]) / (P[i][0] - P[i - 1][0])) * 10) / 10;
          return 10;
        },
        ticks: [10, 5, 0, -5, -10, -20, -30, -50, -144],
      };
    })(),
    // led draws a segmented meter (little squares, green → amber → red,
    // a peak-hold square) along the fader scale; cols = [{level, peak}],
    // one column for mono, two (L, R) for stereo. segs = how many.
    led: function (cv, cols, segs) {
      var dpr = window.devicePixelRatio || 1, w = cv.clientWidth, h = cv.clientHeight;
      if (!w || !h) return;
      if (cv.width !== Math.round(w * dpr) || cv.height !== Math.round(h * dpr)) { cv.width = Math.round(w * dpr); cv.height = Math.round(h * dpr); }
      var g = cv.getContext("2d"), law = window.Wing.law;
      g.setTransform(dpr, 0, 0, dpr, 0, 0);
      g.clearRect(0, 0, w, h);
      var n = typeof segs === "number" ? segs : Math.max(8, Math.min(30, Math.floor(h / 10)));
      var gap = n * 3 > h ? 1 : 2, sh = Math.max(0.5, (h - gap * (n - 1)) / n), cgap = cols.length > 1 ? 4 : 0, cw = (w - cgap * (cols.length - 1)) / cols.length;
      for (var c = 0; c < cols.length; c++) {
        var lv = cols[c].level, pk = cols[c].peak, x = c * (cw + cgap);
        for (var i = 0; i < n; i++) {
          var db = law.toDb((i + 0.5) / n), y = h - (i + 1) * sh - i * gap;
          var top = law.toDb((i + 1) / n);
          var lit = lv != null && lv >= db, hold = !lit && pk != null && pk >= db && pk < top + 0.01;
          g.fillStyle = lit || hold ? (db >= 2 ? "#e5484d" : db >= -6 ? "#f5a524" : "#30a46c") : "#1c2128";
          g.globalAlpha = hold ? 0.85 : 1;
          var r = Math.max(0, Math.min(1.2, sh / 3));
          if (g.roundRect) { g.beginPath(); g.roundRect(x, y, cw, sh, r); g.fill(); } else g.fillRect(x, y, cw, sh);
        }
      }
      g.globalAlpha = 1;
    },
    // peaks keeps peak-hold values per key (1.5 s hold, then falls).
    peaks: function () {
      var m = {};
      return function (key, db) {
        var now = Date.now(), p = m[key];
        if (db == null) return p ? p.v : null;
        if (!p || db >= p.v || now - p.at > 1500) { m[key] = p = { v: db, at: now }; }
        return p.v;
      };
    },
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
