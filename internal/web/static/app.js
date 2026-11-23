// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

/* ---------- helpers ---------- */
const $ = (s, el = document) => el.querySelector(s);
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const MINUS = "−";

async function api(method, path, body) {
  const opt = { method, headers: { "X-Aether": "1" } };
  if (body !== undefined) {
    opt.headers["Content-Type"] = "application/json";
    opt.body = JSON.stringify(body);
  }
  const r = await fetch(path, opt);
  const text = await r.text();
  let j = null;
  try { j = text ? JSON.parse(text) : null; } catch { /* not JSON */ }
  if (!r.ok) throw new Error(j && j.error ? j.error : `${r.status} ${r.statusText}`);
  return j;
}

// Milliseconds, with a sensible number of digits and a real minus sign.
function fmtMs(v, signed = true) {
  if (v == null || Number.isNaN(v)) return "—";
  const a = Math.abs(v);
  let s;
  if (a >= 1000) s = (a / 1000).toFixed(a >= 10000 ? 1 : 3) + " s";
  else if (a >= 100) s = a.toFixed(0) + " ms";
  else if (a >= 10) s = a.toFixed(1) + " ms";
  else s = a.toFixed(2) + " ms";
  if (!signed) return s;
  return (v > 0 ? "+" : v < 0 ? MINUS : "") + s;
}
const fmtSigned = (v, d = 2) => (v > 0 ? "+" : v < 0 ? MINUS : "") + Math.abs(v).toFixed(d);
const pad = (n, w = 2) => String(n).padStart(w, "0");

function ago(iso) {
  const t = typeof iso === "number" ? iso : Date.parse(iso);
  if (!t) return "";
  const s = Math.max(0, (nowMs() - t) / 1000);
  if (s < 5) return "just now";
  if (s < 90) return `${Math.round(s)} s ago`;
  if (s < 5400) return `${Math.round(s / 60)} min ago`;
  if (s < 172800) return `${Math.round(s / 3600)} h ago`;
  return new Date(t).toLocaleDateString();
}

const clockTime = (ms) => new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });

/* ---------- state ---------- */
let S = null;          // latest status from the server
let CFG = null;        // configuration (for thresholds and the settings form)
let skew = 0;          // server system clock minus browser clock (ms)
let skewInit = false;
let connected = false;
const openModules = new Set();

// Best estimate of true UTC: the server's system clock corrected by the measured offset.
function nowMs() {
  const off = S && S.engine && S.engine.offset_ms != null ? S.engine.offset_ms : 0;
  return Date.now() + skew + off;
}

/* ---------- live updates ---------- */
function connect() {
  const es = new EventSource("/api/events");
  es.onmessage = (ev) => {
    const st = JSON.parse(ev.data);
    const serverMs = Date.parse(st.engine.system_time.replace(/(\.\d{3})\d+/, "$1"));
    const sample = serverMs - Date.now();
    if (!skewInit || Math.abs(sample - skew) > 2000) { skew = sample; skewInit = true; }
    else skew += (sample - skew) * 0.2;
    S = st;
    if (!connected) { connected = true; }
    render();
  };
  es.onerror = () => {
    connected = false;
    renderBanners();
  };
}

/* ---------- the instrument ---------- */
// The meter is drawn at its real pixel width so the scale stays legible on phones.
const METER = { axisY: 66, decades: 4 };

function meterX(ms) {
  const f = Math.min(1, Math.log10(1 + Math.abs(ms)) / METER.decades);
  return METER.cx + Math.sign(ms) * f * METER.half;
}

function buildMeter() {
  const svg = $("#meter");
  const W = Math.max(260, Math.round(svg.parentElement.clientWidth || 640));
  svg.setAttribute("viewBox", `0 0 ${W} 104`);
  svg.innerHTML = `<title id="meter-title">Clock error on a logarithmic scale</title>`;
  METER.x0 = 26;
  METER.x1 = W - 26;
  METER.cx = W / 2;
  METER.half = (METER.x1 - METER.x0) / 2;
  const narrow = W < 520, tiny = W < 420;
  const y = METER.axisY;
  let h = "";
  const zone = (a, b, cls) => {
    for (const s of [-1, 1]) {
      const xa = meterX(s * a), xb = meterX(s * b);
      h += `<rect class="${cls}" x="${Math.min(xa, xb)}" y="${y - 40}" width="${Math.abs(xb - xa)}" height="40"/>`;
    }
  };
  zone(0, 100, "m-zone-good");
  zone(100, 1000, "m-zone-ok");
  zone(1000, 10000, "m-zone-bad");
  h += `<line class="m-axis" x1="${METER.x0}" x2="${METER.x1}" y1="${y}" y2="${y}"/>`;
  const labels = tiny ? { 100: "100 ms", 10000: "10 s" }
    : narrow ? { 100: "100 ms", 1000: "1 s", 10000: "10 s" }
    : { 10: "10 ms", 100: "100 ms", 1000: "1 s", 10000: "10 s" };
  for (let d = 0; d <= METER.decades; d++) {
    const base = Math.pow(10, d);
    for (let k = 1; k <= 9; k++) {
      const v = base * k;
      if (v > 10000) break;
      for (const s of [-1, 1]) {
        const x = meterX(s * v);
        const major = k === 1;
        h += `<line class="m-tick${major ? " major" : ""}" x1="${x}" x2="${x}" y1="${y}" y2="${y + (major ? 10 : 5)}"/>`;
        if (major && labels[v]) {
          h += `<text class="m-label" x="${x}" y="${y + 26}" text-anchor="middle">${labels[v]}</text>`;
        }
      }
    }
  }
  h += `<line class="m-tick major" x1="${METER.cx}" x2="${METER.cx}" y1="${y - 44}" y2="${y + 12}"/>`;
  h += `<text class="m-label zero" x="${METER.cx}" y="${y + 26}" text-anchor="middle">0</text>`;
  h += `<text class="m-end" x="${METER.x0}" y="12">Behind</text>`;
  h += `<text class="m-end" x="${METER.x1}" y="12" text-anchor="end">Ahead</text>`;
  h += `<rect id="m-band" class="m-band" x="${METER.cx}" y="${y - 34}" width="0" height="28" visibility="hidden"/>`;
  h += `<g id="m-needle" class="m-needle-g" transform="translate(${METER.cx} 0)" visibility="hidden">
          <line class="m-needle" x1="0" x2="0" y1="${y - 46}" y2="${y + 2}"/>
          <path class="m-needle-cap" d="M-6 ${y + 6} L0 ${y - 2} L6 ${y + 6} Z"/></g>`;
  svg.insertAdjacentHTML("beforeend", h);
  if (S) renderMeter(S.engine);
}

function renderMeter(e) {
  const needle = $("#m-needle"), band = $("#m-band");
  if (e.offset_ms == null) {
    needle.setAttribute("visibility", "hidden");
    band.setAttribute("visibility", "hidden");
    $("#error-text").textContent = "No measurement yet";
    $("#uncert-text").textContent = "";
    return;
  }
  const err = -e.offset_ms; // positive: this computer's clock is ahead
  const unc = e.error_ms || 0;
  needle.setAttribute("visibility", "visible");
  needle.setAttribute("transform", `translate(${meterX(err).toFixed(1)} 0)`);
  const a = meterX(err - unc), b = meterX(err + unc);
  band.setAttribute("visibility", "visible");
  band.setAttribute("x", Math.min(a, b).toFixed(1));
  band.setAttribute("width", Math.max(1, Math.abs(b - a)).toFixed(1));
  let text;
  if (Math.abs(err) < 0.05) text = "System clock is exact";
  else text = `System clock is ${fmtMs(Math.abs(err), false)} ${err > 0 ? "ahead" : "behind"}`;
  if (e.holdover) text += " (estimated from drift)";
  $("#error-text").textContent = text;
  $("#uncert-text").textContent = `± ${fmtMs(unc, false)}`;
}

let cycleBuiltFor = 0;
function buildCycle(period) {
  const g = $("#cycle-ticks");
  const n = 15;
  let h = "";
  for (let i = 0; i < n; i++) {
    const a = (i / n) * 2 * Math.PI - Math.PI / 2;
    const major = i === 0;
    const r1 = major ? 44 : 48, r2 = 52;
    h += `<line class="cycle-tick${major ? " major" : ""}" x1="${(70 + r1 * Math.cos(a)).toFixed(2)}" y1="${(70 + r1 * Math.sin(a)).toFixed(2)}" x2="${(70 + r2 * Math.cos(a)).toFixed(2)}" y2="${(70 + r2 * Math.sin(a)).toFixed(2)}"/>`;
  }
  g.innerHTML = h;
  cycleBuiltFor = period;
}

function wsjtxSource() {
  return S && S.engine.sources ? S.engine.sources.find((s) => s.kind === "wsjtx") : null;
}

function currentMode() {
  const w = wsjtxSource();
  const inst = w && w.details && w.details.instances;
  if (inst && inst.length && inst[0].mode) return inst[0].mode.toUpperCase();
  return "FT8";
}

function tickClock() {
  const t = nowMs();
  const d = new Date(t);
  const sep = `<span class="sep">:</span>`;
  $("#utc").innerHTML = `${pad(d.getUTCHours())}${sep}${pad(d.getUTCMinutes())}${sep}${pad(d.getUTCSeconds())}<span class="tenths"><span class="sep">.</span>${Math.floor(d.getUTCMilliseconds() / 100)}</span>`;
  if (S) {
    $("#utc-date").textContent = d.toLocaleDateString([], { timeZone: "UTC", weekday: "long", day: "numeric", month: "long", year: "numeric" });
    $("#local").textContent = "Local time " + d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit", timeZoneName: "short" });
  }
  const mode = currentMode();
  const period = mode === "FT4" ? 7.5 : 15;
  if (cycleBuiltFor !== period) buildCycle(period);
  const secOfMin = (t / 1000) % 60;
  const into = secOfMin % period;
  const frac = into / period;
  const arc = $("#cycle-arc");
  arc.setAttribute("stroke-dasharray", `${(frac * 1000).toFixed(1)} 1000`);
  const tx = S && S.engine.transmitting;
  arc.classList.toggle("tx", !!tx);
  $("#cycle-sec").textContent = into.toFixed(1).padStart(4, "0");
  $("#cycle-mode").textContent = mode === "FT4" ? "FT4" : "FT8";
  const even = Math.floor(secOfMin / period) % 2 === 0;
  $("#cycle-caption").textContent = `${even ? "Even" : "Odd"} period${tx ? ", transmitting" : ""}`;
}

/* ---------- rendering ---------- */
const STATE = {
  synced: ["ok", "Synchronized"],
  tracking: ["warn", "Correcting"],
  holdover: ["hold", "Holdover: following the learned drift"],
  "no-reference": ["bad", "No time source available"],
  starting: ["", "Starting"],
};

function render() {
  const e = S.engine;
  renderMeter(e);
  const [cls, label] = STATE[e.state] || ["", e.state];
  $("#state-lamp").className = "lamp " + cls;
  let text = label;
  if (e.state === "synced" && e.used.length) text += ` with ${e.used.map(sourceName).join(" + ")}`;
  if (e.monitor) text += " (monitor only)";
  $("#state-text").textContent = text;
  const lc = e.last_correction;
  const note = e.note && !/^monitor only/.test(e.note) ? e.note : "";
  $("#last-corr").textContent = lc ? `Last correction ${fmtMs(lc.amount_ms)} ${ago(lc.at)}${lc.result !== "ok" ? " (failed)" : ""}` : note;
  $("#drift-line").textContent = e.drift_ppm != null ? `Clock drift ${fmtSigned(e.drift_ppm)} ppm (${Math.abs(e.drift_ppm * 86.4).toFixed(0)} ms per day)` : "";
  renderSources();
  renderFT8();
  renderMachine();
  renderBanners();
  renderFoot();
}

function sourceName(id) {
  return { ntp: "NTP", https: "web time", gps: "GPS", gpsd: "gpsd", wsjtx: "FT8 decodes" }[id] || id;
}

function lampFor(sv) {
  if (sv.state === "ok") return "ok";
  if (sv.state === "error") return "bad";
  if (sv.state === "waiting") return "warn";
  return "";
}

const lastModuleHTML = {};
function renderSources() {
  const box = $("#sources");
  const list = S.engine.sources || [];
  if (!list.length) {
    box.innerHTML = `<p class="empty">No time sources are turned on. Turn one on in Settings.</p>`;
    return;
  }
  // Keyed update keeps <details> open and avoids flicker.
  const ids = list.map((s) => s.id);
  for (const el of [...box.children]) if (!ids.includes(el.dataset.id)) el.remove();
  list.forEach((sv, i) => {
    let el = box.querySelector(`[data-id="${sv.id}"]`);
    if (!el) {
      el = document.createElement("details");
      el.className = "module";
      el.dataset.id = sv.id;
      el.addEventListener("toggle", () => (el.open ? openModules.add(sv.id) : openModules.delete(sv.id)));
      box.appendChild(el);
    }
    if (box.children[i] !== el) box.insertBefore(el, box.children[i]);
    const html = moduleHTML(sv);
    if (lastModuleHTML[sv.id] !== html) {
      const wasOpen = el.open;
      el.innerHTML = html;
      el.open = wasOpen || openModules.has(sv.id);
      lastModuleHTML[sv.id] = html;
    }
  });
}

function moduleHTML(sv) {
  let val = "—";
  if (sv.offset_ms != null && sv.fresh) {
    val = `${fmtMs(-sv.offset_ms)} <small>± ${fmtMs(sv.error_ms, false)}</small>`;
  }
  let role = "", roleCls = "role";
  if (sv.used) { role = "In use"; roleCls += " used"; }
  else if (sv.rejected) { role = "Disagrees, ignored"; roleCls += " reject"; }
  else if (sv.fresh) role = "Available";
  return `<summary>
      <span class="lamp ${lampFor(sv)}" aria-hidden="true"></span>
      <span class="name">${esc(sv.name)}</span>
      <span class="val">${val}</span>
      <span class="${roleCls}">${role}</span>
      <span class="msg">${esc(sv.message)}</span>
    </summary>
    <div class="module-body">${detailsHTML(sv)}</div>`;
}

function kv(pairs) {
  const rows = pairs.filter((p) => p[1] !== undefined && p[1] !== null && p[1] !== "").map(([k, v]) => `<dt>${esc(k)}</dt><dd>${v}</dd>`);
  return rows.length ? `<dl class="kv">${rows.join("")}</dl>` : "";
}

function detailsHTML(sv) {
  const d = sv.details || {};
  switch (sv.kind) {
    case "ntp": {
      const rows = (d.servers || []).map((s) => `<tr><td>${esc(s.name)}<br><span class="dim">${esc(s.address || "")}</span></td>
        <td class="num">${s.offset_ms != null ? fmtMs(-s.offset_ms) : "—"}</td>
        <td class="num">${s.delay_ms != null ? fmtMs(s.delay_ms, false) : ""}</td>
        <td class="num">${s.stratum || ""}</td>
        <td>${s.error ? esc(s.error) : s.used ? "In use" : s.offset_ms != null ? "Ignored" : ""}</td></tr>`).join("");
      return `<div class="scroll"><table><thead><tr><th>Server</th><th class="num">Clock error</th><th class="num">Round trip</th><th class="num">Stratum</th><th>Status</th></tr></thead><tbody>${rows}</tbody></table></div>` +
        kv([["Next poll", d.next_poll ? clockTime(Date.parse(d.next_poll)) : ""]]);
    }
    case "https": {
      const rows = (d.urls || []).map((u) => `<tr><td>${esc(u.url)}</td><td class="num">${u.offset_ms != null ? fmtMs(-u.offset_ms) : "—"}</td>
        <td class="num">${u.error_ms != null ? "± " + fmtMs(u.error_ms, false) : ""}</td><td>${u.error ? esc(u.error) : u.used ? "In use" : ""}</td></tr>`).join("");
      return (rows ? `<table><thead><tr><th>Server</th><th class="num">Clock error</th><th class="num">Precision</th><th>Status</th></tr></thead><tbody>${rows}</tbody></table>` : "") +
        `<p class="help">${d.fallback_only ? "Only used when NTP is unreachable." : "Polled alongside NTP."}</p>`;
    }
    case "gps":
    case "gpsd": {
      const lat = d.learned_latency_ms, conf = d.configured_latency_ms;
      let latency = "";
      if (lat != null) latency = `${lat.toFixed(1)} ms (learned from ${d.calibration_samples || "earlier"} comparisons)`;
      else if (d.auto_calibrate) latency = `learning (${d.calibration_samples || 0} of 20 comparisons; needs internet time)`;
      else if (conf) latency = `${conf} ms (configured)`;
      const pos = d.grid ? `${esc(d.grid)} <span class="dim">${d.lat.toFixed(4)}, ${d.lon.toFixed(4)}${d.alt ? `, ${Math.round(d.alt)} m` : ""}</span>` : "";
      const conn = sv.kind === "gps"
        ? (d.port ? `${esc(d.port)} at ${d.baud} baud` : esc(d.address || ""))
        : esc(d.address || "");
      const forget = lat != null ? `<button type="button" class="btn" data-forget="${sv.kind === "gps" ? "gps.latency_ms" : "gpsd.latency_ms"}">Relearn latency</button>` : "";
      return kv([
        ["Connection", `${conn}${d.connected ? "" : " (not connected)"}`],
        ["Fix", sv.kind === "gps" ? (d.fix ? "Yes" : "No") : (d.mode >= 2 ? `${d.mode}D` : "No")],
        ["Satellites", d.sats || ""],
        ["PPS", sv.kind === "gpsd" ? (d.pps ? "Locked" : "Not seen") : ""],
        ["Position", pos],
        ["Message delay", latency],
        ["Jitter", d.jitter_ms ? fmtMs(d.jitter_ms, false) : ""],
        ["Last sentence", d.last ? `<code>${esc(d.last)}</code>` : ""],
      ]) + forget;
    }
    case "wsjtx": {
      const inst = (d.instances || []).map((i) => `<tr><td>${esc(i.id)}${i.version ? ` <span class="dim">${esc(i.version)}</span>` : ""}</td>
        <td>${esc(i.mode || "")}</td><td class="num">${i.dial_hz ? (i.dial_hz / 1e6).toFixed(3) + " MHz" : ""}</td><td>${i.transmitting ? "Transmitting" : "Receiving"}</td></tr>`).join("");
      const bias = d.bias_ms != null ? `${fmtSigned(d.bias_ms / 1000)} s (${d.bias_source === "learned" ? "learned" : d.bias_source === "learning" ? "learning" : "configured"})` : "";
      const forget = d.bias_source === "learned" ? `<button type="button" class="btn" data-forget="wsjtx.dt_bias_ms">Relearn DT bias</button>` : "";
      return kv([
        ["Listening on", d.listen ? `UDP ${esc(d.listen)}` : "UDP off"],
        ["Relaying to", (d.forward || []).map(esc).join(", ")],
        ["ALL.TXT", d.all_txt ? esc(d.all_txt) : ""],
        ["DT when on time", bias],
      ]) + (inst ? `<table><thead><tr><th>Program</th><th>Mode</th><th class="num">Dial</th><th>State</th></tr></thead><tbody>${inst}</tbody></table>` :
        `<p class="help">No WSJT-X, JTDX or MSHV heard yet. In their settings, set the UDP server to ${esc(d.listen || "127.0.0.1:2237")}.</p>`) + forget;
    }
  }
  return "";
}

function renderFT8() {
  const box = $("#ft8");
  const w = wsjtxSource();
  if (!w) {
    box.innerHTML = `<p class="dim">FT8/FT4 timing is turned off. Turn it on in Settings to keep time from your decodes when you're offline.</p>`;
    return;
  }
  const d = w.details || {};
  const hist = d.histogram || [];
  const max = Math.max(1, ...hist);
  const bars = hist.map((n, i) => `<div class="${i === 10 ? "center" : ""}" style="height:${((n / max) * 100).toFixed(1)}%" title="DT ${fmtSigned((i - 10) / 10, 1)} s: ${n} decodes"></div>`).join("");
  const est = d.estimate;
  let summary = `<p class="dim">${esc(w.message)}</p>`;
  if (est) {
    summary = `<p>Median DT <strong>${fmtSigned(est.median_dt)} s</strong> from ${est.n} decodes, spread ${est.spread_dt.toFixed(2)} s.</p>`;
  }
  const recent = (d.recent || []).slice(0, 8).map((r) => `<tr><td class="num">${esc(r.at)}</td><td>${esc(r.mode)}</td><td class="num">${r.snr}</td><td class="num">${fmtSigned(r.dt, 1)}</td><td>${esc(r.message)}</td></tr>`).join("");
  box.innerHTML = `${summary}
    <div><div class="hist" role="img" aria-label="Distribution of DT values from ${MINUS}1 to +1 seconds">${bars}</div>
    <div class="hist-axis"><span>${MINUS}1.0 s</span><span>0</span><span>+1.0 s</span></div></div>
    ${recent ? `<div class="scroll"><table><thead><tr><th class="num">UTC</th><th>Mode</th><th class="num">SNR</th><th class="num">DT</th><th>Message</th></tr></thead><tbody>${recent}</tbody></table></div>` :
      `<p class="dim">Decodes appear here as WSJT-X, JTDX or MSHV report them.</p>`}`;
}

function renderMachine() {
  const e = S.engine, info = S.info;
  const learned = e.learned || {};
  const svcState = { running: "the service is running", stopped: "the service is installed but stopped", "not installed": "the service is not installed" }[info.service_status];
  let svcLine = info.service ? "Service" : `App${svcState ? `, ${svcState}` : ""}`;
  if (!info.service && info.service_status !== "running" && info.can_elevate) {
    svcLine += ` <button type="button" class="btn" id="install-svc">Install service</button>`;
  }
  const ns = e.ntp_server;
  const html = kv([
    ["Clock control", e.can_adjust ? "Allowed" : `Not allowed: ${esc(e.adjust_why)}`],
    ["Run mode", svcLine],
    ["Drift", e.drift_ppm != null ? `${fmtSigned(e.drift_ppm)} ppm (${e.drift_source})` : "Still measuring (needs 30 minutes of data)"],
    ["GPS message delay", learned["gps.latency_ms"] != null ? `${learned["gps.latency_ms"].toFixed(1)} ms` : ""],
    ["FT8 DT when on time", learned["wsjtx.dt_bias_ms"] != null ? `${fmtSigned(learned["wsjtx.dt_bias_ms"] / 1000)} s` : ""],
    ["Position", e.position ? `${esc(e.position.grid)} from ${esc(e.position.source)} <button type="button" class="btn" id="send-grid">Send grid to WSJT-X</button>` : ""],
    ["LAN time server", ns.enabled ? (ns.error ? `Error: ${esc(ns.error)}` : `Serving on ${esc(ns.listen)}, ${ns.served} requests answered`) : "Off"],
    ["Monitor only", `<label class="row"><input type="checkbox" id="monitor-toggle" ${e.monitor ? "checked" : ""} ${e.mode === "monitor" ? "disabled" : ""}> Measure without changing the clock</label>`],
  ]);
  const box = $("#machine");
  if (box.dataset.html !== html) {
    box.innerHTML = html;
    box.dataset.html = html;
  }
}

function renderBanners() {
  const out = [];
  if (!connected) {
    out.push(`<div class="banner bad"><p>Lost contact with Aether Time. Reconnecting…</p></div>`);
  }
  if (S) {
    const u = S.update, e = S.engine, info = S.info;
    if (u.state === "downloading" || u.state === "installing") {
      out.push(`<div class="banner info"><p>Installing Aether Time ${esc(u.latest.version)}: ${u.state === "downloading" ? Math.round(u.progress * 100) + "% downloaded" : "replacing the program"}.</p></div>`);
    } else if (u.state === "installed") {
      out.push(`<div class="banner good"><p>Update installed. Aether Time is restarting; this page reconnects by itself.</p></div>`);
    } else if (u.available && u.latest) {
      out.push(`<div class="banner info"><p>Aether Time ${esc(u.latest.version)} is available. You have ${esc(u.current)}.${u.signature_check ? "" : " This build can't verify release signatures; only checksums are checked."}</p>
        <a href="${esc(u.latest.url)}" target="_blank" rel="noopener">Release notes</a>
        <button type="button" class="btn btn-primary" id="install-update">Install and restart</button></div>`);
    } else if (u.state === "error" && u.last_error && /SIGNATURE|checksum/.test(u.last_error)) {
      out.push(`<div class="banner bad"><p>An update was rejected: ${esc(u.last_error)}</p></div>`);
    }
    if (!e.can_adjust && e.mode === "auto" && !e.monitor) {
      const fix = info.can_elevate && info.service_status !== "running"
        ? `<button type="button" class="btn btn-primary" id="install-svc-banner">Install service</button>` : "";
      out.push(`<div class="banner"><p>Measuring only: this copy may not change the clock. ${esc(e.adjust_why)}.</p>${fix}</div>`);
    }
    for (const c of S.conflicts || []) {
      out.push(`<div class="banner"><p><strong>${esc(c.name)}</strong> is running. ${esc(c.advice)}</p></div>`);
    }
    const recent = (e.alerts || []).filter((a) => a.level === "error" && Date.now() - Date.parse(a.at) < 5 * 60 * 1000).slice(-1);
    for (const a of recent) out.push(`<div class="banner bad"><p>${esc(a.text)}</p></div>`);
  }
  const html = out.join("");
  const box = $("#banners");
  if (box.dataset.html !== html) {
    box.innerHTML = html;
    box.dataset.html = html;
  }
}

function renderFoot() {
  const i = S.info;
  const html = `Aether Time ${esc(i.version)} for ${esc(i.os)}/${esc(i.arch)}. Free software under the
    <a href="https://www.gnu.org/licenses/gpl-3.0.html" target="_blank" rel="noopener">GNU GPL v3 or later</a>, with no warranty.
    Settings file: ${esc(i.config_path)}`;
  const f = $("#foot");
  if (f.dataset.html !== html) { f.innerHTML = html; f.dataset.html = html; }
}

/* ---------- history chart and corrections ---------- */
async function loadHistory() {
  if (!$("#view-dash").hidden) {
    const minutes = $("#hist-range").value;
    try {
      const h = await api("GET", `/api/history?minutes=${minutes}`);
      renderChart(h, Number(minutes));
      renderCorrections(h.corrections);
    } catch { /* shown by the connection banner */ }
  }
}

function niceRange(v) {
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 5, 10]) if (v <= m * p) return m * p;
  return 10 * p;
}

function renderChart(h, minutes) {
  const box = $("#chart");
  const W = Math.max(280, box.clientWidth), H = 220, L = 58, R = 10, T = 12, B = 26;
  const pts = h.points;
  if (pts.length < 2) {
    box.innerHTML = `<p class="dim">Collecting data. The chart fills in as measurements arrive.</p>`;
    return;
  }
  const tEnd = Date.now() + skew;
  const tStart = Math.max(tEnd - minutes * 60000, pts[0].t - 30000);
  let m = 0;
  for (const p of pts) m = Math.max(m, Math.abs(p.offset_ms) + p.error_ms);
  const thr = CFG ? CFG.adjust.threshold_ms : 20;
  const range = niceRange(Math.max(m, 2));
  const x = (t) => L + ((t - tStart) / (tEnd - tStart)) * (W - L - R);
  const y = (v) => T + (1 - (v + range) / (2 * range)) * (H - T - B);
  const clampV = (v) => Math.max(-range, Math.min(range, v));
  let g = "";
  for (const v of [range, range / 2, 0, -range / 2, -range]) {
    g += `<line class="${v === 0 ? "c-zero" : "c-grid"}" x1="${L}" x2="${W - R}" y1="${y(v)}" y2="${y(v)}"/>`;
    g += `<text class="c-label" x="${L - 6}" y="${y(v) + 4}" text-anchor="end">${v === 0 ? "0" : fmtMs(v)}</text>`;
  }
  if (thr < range) {
    for (const v of [thr, -thr]) g += `<line class="c-thr" x1="${L}" x2="${W - R}" y1="${y(v)}" y2="${y(v)}"/>`;
  }
  // Split into segments at gaps and holdover changes.
  const segs = [];
  let cur = null;
  for (const p of pts) {
    const hold = p.state === "holdover";
    if (!cur || p.t - cur.last > 60000 || cur.hold !== hold) {
      cur = { hold, pts: [], last: p.t };
      segs.push(cur);
    }
    cur.pts.push(p);
    cur.last = p.t;
  }
  for (const s of segs) {
    const up = s.pts.map((p) => `${x(p.t).toFixed(1)},${y(clampV(-p.offset_ms + p.error_ms)).toFixed(1)}`);
    const dn = s.pts.slice().reverse().map((p) => `${x(p.t).toFixed(1)},${y(clampV(-p.offset_ms - p.error_ms)).toFixed(1)}`);
    g += `<polygon class="c-band" points="${up.concat(dn).join(" ")}"/>`;
  }
  for (const s of segs) {
    const d = s.pts.map((p, i) => `${i ? "L" : "M"}${x(p.t).toFixed(1)} ${y(clampV(-p.offset_ms)).toFixed(1)}`).join("");
    g += `<path class="${s.hold ? "c-hold" : "c-line"}" d="${d}"/>`;
  }
  for (const c of h.corrections) {
    const t = Date.parse(c.at);
    if (t < tStart) continue;
    const cx = x(t).toFixed(1);
    g += `<line class="c-corr" x1="${cx}" x2="${cx}" y1="${T}" y2="${H - B}"><title>${esc(c.method)} ${fmtMs(c.amount_ms)} at ${clockTime(t)}</title></line>`;
  }
  for (const f of [0, 0.5, 1]) {
    const t = tStart + f * (tEnd - tStart);
    g += `<text class="c-label" x="${x(t)}" y="${H - 6}" text-anchor="${f === 0 ? "start" : f === 1 ? "end" : "middle"}">${new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</text>`;
  }
  box.innerHTML = `<svg viewBox="0 0 ${W} ${H}" role="img" aria-label="Clock error over the last ${minutes} minutes; positive means ahead">${g}</svg>`;
}

function renderCorrections(cs) {
  const box = $("#corrections");
  if (!cs.length) {
    box.innerHTML = `<p class="dim">No corrections in this period.</p>`;
    return;
  }
  const label = { step: "Step", smooth: "Gradual", manual: "Manual" };
  const rows = cs.slice().reverse().slice(0, 25).map((c) => `<tr>
      <td class="num">${clockTime(Date.parse(c.at))}</td>
      <td class="num">${fmtMs(c.amount_ms)}</td>
      <td>${esc(label[c.method] || c.method)}</td>
      <td>${esc((c.sources || []).map(sourceName).join(", "))}</td>
      <td>${c.result === "ok" ? "Done" : esc(c.result)}</td></tr>`).join("");
  box.innerHTML = `<div class="scroll"><table><thead><tr><th class="num">Time</th><th class="num">Moved by</th><th>How</th><th>From</th><th>Result</th></tr></thead><tbody>${rows}</tbody></table></div>`;
}

/* ---------- actions ---------- */
document.addEventListener("click", async (ev) => {
  const t = ev.target.closest("button");
  if (!t) return;
  try {
    if (t.id === "sync-now") {
      t.disabled = true;
      t.textContent = "Syncing…";
      await api("POST", "/api/sync");
      setTimeout(() => { t.disabled = false; t.textContent = "Sync now"; }, 4000);
    } else if (t.id === "install-update") {
      t.disabled = true;
      await api("POST", "/api/update/install");
    } else if (t.id === "install-svc" || t.id === "install-svc-banner") {
      await api("POST", "/api/service/install");
      t.textContent = "Approve the Windows prompt";
      t.disabled = true;
    } else if (t.id === "send-grid") {
      const r = await api("POST", "/api/grid/send");
      t.textContent = r.sent ? `Sent ${r.grid}` : "No WSJT-X heard";
    } else if (t.dataset.forget) {
      await api("POST", "/api/learned/forget", { key: t.dataset.forget });
      t.textContent = "Relearning";
      t.disabled = true;
    } else if (t.id === "log-refresh") {
      loadLog();
    }
  } catch (err) {
    alert(err.message);
    t.disabled = false;
  }
});

document.addEventListener("change", async (ev) => {
  if (ev.target.id === "monitor-toggle") {
    await api("POST", "/api/monitor", { on: ev.target.checked }).catch((e) => alert(e.message));
  }
});

$("#manual").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const v = Number($("#manual-ms").value);
  if (!v) return;
  if (!confirm(`Move the clock ${v > 0 ? "forward" : "back"} by ${fmtMs(Math.abs(v), false)}?`)) return;
  try {
    await api("POST", "/api/adjust", { ms: v });
    $("#manual-ms").value = "";
  } catch (err) {
    alert(err.message);
  }
});

$("#hist-range").addEventListener("change", loadHistory);

/* ---------- views ---------- */
function showView(name) {
  for (const v of ["dash", "settings", "log"]) $(`#view-${v}`).hidden = v !== name;
  for (const b of document.querySelectorAll(".tabs button")) {
    if (b.dataset.view === name) b.setAttribute("aria-current", "page");
    else b.removeAttribute("aria-current");
  }
  if (name === "settings") loadSettings();
  if (name === "log") loadLog();
  if (name === "dash") loadHistory();
}
document.querySelectorAll(".tabs button").forEach((b) => b.addEventListener("click", () => {
  history.replaceState(null, "", b.dataset.view === "dash" ? "#" : "#" + b.dataset.view);
  showView(b.dataset.view);
}));

/* ---------- log ---------- */
async function loadLog() {
  try {
    const entries = await api("GET", "/api/logs");
    const rows = entries.slice().reverse().map((e) => `<tr>
      <td class="num">${new Date(e.time).toLocaleString()}</td>
      <td class="lvl-${esc(e.level)}">${esc(e.level.toLowerCase())}</td>
      <td>${esc(e.msg)}${e.attrs ? `<div class="log-attrs">${esc(e.attrs)}</div>` : ""}</td></tr>`).join("");
    $("#log").innerHTML = rows ? `<table><thead><tr><th class="num">Time</th><th>Level</th><th>Message</th></tr></thead><tbody>${rows}</tbody></table>` : `<p class="dim">Nothing logged yet.</p>`;
  } catch (err) {
    $("#log").innerHTML = `<p class="dim">Couldn't load the log: ${esc(err.message)}</p>`;
  }
}

/* ---------- settings ---------- */
const BAUDS = [4800, 9600, 19200, 38400, 57600, 115200].map((b) => [b, String(b)]);
const PRIO = { type: "int", help: "1 is the most trusted. Used when “Choose sources” is set to priority." };
const SCHEMA = [
  { id: "general", title: "Clock correction", intro: "How Aether Time decides when and how to change the clock.", fields: [
    ["mode", "Mode", { type: "select", options: [["auto", "Correct the clock"], ["monitor", "Measure only"]] }],
    ["selection", "Choose sources", { type: "select", options: [["auto", "Combine the sources that agree"], ["priority", "Use the most trusted source only"]] }],
    ["adjust.threshold_ms", "Ignore errors smaller than", { type: "num", unit: "ms", help: "FT8 tolerates about ±1 s; 20 ms keeps you well inside it without constant small corrections." }],
    ["adjust.smooth", "Correct small errors gradually", { type: "bool", help: "Moves the clock in small steps instead of one jump." }],
    ["adjust.smooth_max_step_ms", "Largest gradual step", { type: "num", unit: "ms per second" }],
    ["adjust.step_threshold_ms", "Jump at once when the error exceeds", { type: "num", unit: "ms" }],
    ["adjust.min_interval_s", "Wait between corrections at least", { type: "num", unit: "seconds" }],
    ["adjust.max_correction_s", "Refuse corrections larger than", { type: "num", unit: "seconds", help: "0 means no limit. Protects against a wrong source setting a wildly wrong time." }],
    ["adjust.allow_large_first_step", "Allow a large first correction", { type: "bool", help: "For computers whose clock battery is flat and start up with the wrong date." }],
    ["adjust.sync_on_start", "Correct as soon as the program starts", { type: "bool" }],
    ["adjust.defer_during_tx", "Wait until WSJT-X or JTDX stops transmitting", { type: "bool", help: "Avoids shifting the clock in the middle of your own transmission." }],
    ["adjust.holdover", "Keep correcting when every source is gone", { type: "bool", help: "Uses the drift learned while sources were available." }],
  ] },
  { id: "internet", title: "Internet time", intro: "Used whenever the computer is online. NTP is the most accurate; web time keeps working on networks that block NTP, such as hotels and some hotspots.", fields: [
    ["ntp.enabled", "Use NTP servers", { type: "bool" }],
    ["ntp.servers", "NTP servers", { type: "lines", help: "One per line. Several servers let Aether Time spot one that is wrong." }],
    ["ntp.poll_interval_s", "Check every", { type: "num", unit: "seconds" }],
    ["ntp.timeout_ms", "Give up on a server after", { type: "num", unit: "ms" }],
    ["ntp.samples", "Queries per server per check", { type: "int", help: "The fastest reply is kept." }],
    ["ntp.correction_ms", "NTP correction", { type: "num", unit: "ms", help: "Added to NTP time. Normally 0." }],
    ["ntp.priority", "NTP priority", PRIO],
    ["https.enabled", "Use web time as a fallback", { type: "bool" }],
    ["https.urls", "Web servers", { type: "lines", help: "HTTPS addresses whose Date header is read. One per line." }],
    ["https.fallback_only", "Only when NTP is unreachable", { type: "bool" }],
    ["https.poll_interval_s", "Check every", { type: "num", unit: "seconds" }],
    ["https.priority", "Web time priority", PRIO],
  ] },
  { id: "gps", title: "GPS receiver", intro: "A GPS receiver gives exact time anywhere, with no internet. USB and Bluetooth receivers appear as serial ports. Phone apps that share NMEA over Wi-Fi work with TCP.", fields: [
    ["gps.enabled", "Use a GPS receiver", { type: "bool" }],
    ["gps.transport", "Connection", { type: "select", options: [["serial", "Serial port (USB, Bluetooth, RS-232)"], ["tcp", "Network, TCP (phone app, ser2net)"], ["udp", "Network, UDP"]] }],
    ["gps.port", "Serial port", { type: "text", list: "serial-ports", show: (c) => c.gps.transport === "serial" }],
    ["gps.baud", "Speed", { type: "select", options: BAUDS, number: true, show: (c) => c.gps.transport === "serial" }],
    ["gps.data_bits", "Data bits", { type: "select", options: [[8, "8"], [7, "7"]], number: true, show: (c) => c.gps.transport === "serial" }],
    ["gps.parity", "Parity", { type: "select", options: [["none", "None"], ["odd", "Odd"], ["even", "Even"], ["mark", "Mark"], ["space", "Space"]], show: (c) => c.gps.transport === "serial" }],
    ["gps.stop_bits", "Stop bits", { type: "select", options: [["1", "1"], ["1.5", "1.5"], ["2", "2"]], show: (c) => c.gps.transport === "serial" }],
    ["gps.dtr", "Raise DTR", { type: "bool", show: (c) => c.gps.transport === "serial" }],
    ["gps.rts", "Raise RTS", { type: "bool", show: (c) => c.gps.transport === "serial" }],
    ["gps.address", "Address", { type: "text", help: "host:port, for example 192.168.1.40:11123", show: (c) => c.gps.transport !== "serial" }],
    ["gps.auto_calibrate", "Learn the message delay automatically", { type: "bool", help: "Compares the GPS with internet time while online, so it is exact when you go offline." }],
    ["gps.latency_ms", "Message delay", { type: "num", unit: "ms", help: "Used until the delay has been learned." }],
    ["gps.time_only", "Ignore the date from the receiver", { type: "bool", help: "For older receivers that report a wrong date (GPS week rollover)." }],
    ["gps.correction_ms", "GPS correction", { type: "num", unit: "ms" }],
    ["gps.priority", "GPS priority", PRIO],
  ] },
  { id: "gpsd", title: "gpsd", intro: "Use this if gpsd already manages your receiver, which is common on a Raspberry Pi. With a PPS signal the accuracy is better than a millisecond.", fields: [
    ["gpsd.enabled", "Use gpsd", { type: "bool" }],
    ["gpsd.address", "gpsd address", { type: "text" }],
    ["gpsd.correction_ms", "gpsd correction", { type: "num", unit: "ms" }],
    ["gpsd.priority", "gpsd priority", PRIO],
  ] },
  { id: "ft8", title: "FT8/FT4 decodes", intro: "With no internet and no GPS, the timing of the stations you decode keeps you in step with the band. In WSJT-X, JTDX or MSHV set the UDP server to the listen address below.", fields: [
    ["wsjtx.enabled", "Use FT8/FT4 decodes", { type: "bool" }],
    ["wsjtx.listen", "Listen for WSJT-X on", { type: "text", help: "UDP address:port. A multicast group such as 239.255.0.1:2237 lets several programs listen at once." }],
    ["wsjtx.forward", "Pass the traffic on to", { type: "lines", help: "One address:port per line, for JTAlert, GridTracker or a logger. Their replies are passed back to WSJT-X." }],
    ["wsjtx.all_txt_path", "Also read ALL.TXT from", { type: "text", list: "alltxt", help: "Optional. Use this when another program already owns the UDP port." }],
    ["wsjtx.modes", "Modes", { type: "csv", help: "Comma separated, for example FT8, FT4." }],
    ["wsjtx.min_snr", "Ignore decodes weaker than", { type: "int", unit: "dB" }],
    ["wsjtx.window_s", "Use decodes from the last", { type: "num", unit: "seconds" }],
    ["wsjtx.min_decodes", "Decodes needed before trusting the result", { type: "int" }],
    ["wsjtx.learn_bias", "Learn the normal DT automatically", { type: "bool", help: "While internet or GPS time is available, measures the DT you see when your clock is right (your audio delay and the band's habits)." }],
    ["wsjtx.dt_bias_ms", "Normal DT", { type: "num", unit: "ms", help: "Used until it has been learned." }],
    ["wsjtx.auto_grid", "Send my GPS grid to WSJT-X", { type: "bool", help: "Needs “Accept UDP requests” and AutoGrid turned on in WSJT-X." }],
    ["wsjtx.priority", "Decodes priority", PRIO],
  ] },
  { id: "server", title: "Time server for your network", intro: "Share this computer's time with the other computers on your network, for example one GPS laptop serving a whole field day site. Point them at this computer as their NTP server.", fields: [
    ["ntp_server.enabled", "Serve time to the network", { type: "bool" }],
    ["ntp_server.listen", "Listen on", { type: "text", help: "Port 123 needs administrator rights, which the service has." }],
  ] },
  { id: "web", title: "Dashboard", intro: "Where this page is served. To open it from a phone or another computer, listen on 0.0.0.0:8123 and set a password.", fields: [
    ["web.listen", "Address", { type: "text" }],
    ["web.password", "Password", { type: "password", help: "Required when the address isn't 127.0.0.1." }],
    ["web.open_browser", "Open the dashboard when the program starts", { type: "bool" }],
  ] },
  { id: "update", title: "Updates", intro: "New versions come from the project's GitHub releases. Each download is checked against its signature and checksum before it is installed.", fields: [
    ["update.enabled", "Check for updates", { type: "bool" }],
    ["update.auto_install", "Install updates automatically", { type: "bool" }],
    ["update.channel", "Channel", { type: "select", options: [["stable", "Stable releases"], ["beta", "Beta releases too"]] }],
    ["update.check_interval_h", "Check every", { type: "num", unit: "hours" }],
  ] },
  { id: "log", title: "Logging", intro: "", fields: [
    ["log.sync_log", "Record corrections in sync-log.csv", { type: "bool" }],
    ["log.debug", "Detailed log", { type: "bool", help: "Logs every measurement. Turn it on only while troubleshooting." }],
  ] },
];

const getPath = (o, p) => p.split(".").reduce((a, k) => (a == null ? a : a[k]), o);
function setPath(o, p, v) {
  const ks = p.split(".");
  let a = o;
  for (const k of ks.slice(0, -1)) a = a[k];
  a[ks[ks.length - 1]] = v;
}

let settingsDirty = false;
async function loadSettings(force) {
  if (settingsDirty && !force) return;
  try {
    CFG = await api("GET", "/api/config");
  } catch (err) {
    $("#save-msg").textContent = "Couldn't load settings: " + err.message;
    return;
  }
  const info = S ? S.info : { serial_ports: [], all_txt_candidates: [] };
  const form = $("#settings-form");
  let html = `<datalist id="serial-ports">${(info.serial_ports || []).map((p) => `<option value="${esc(p)}">`).join("")}</datalist>
    <datalist id="alltxt">${(info.all_txt_candidates || []).map((p) => `<option value="${esc(p)}">`).join("")}</datalist>`;
  for (const g of SCHEMA) {
    html += `<fieldset id="set-${g.id}"><legend>${esc(g.title)}</legend>${g.intro ? `<p class="fs-intro">${esc(g.intro)}</p>` : ""}`;
    for (const [path, label, f] of g.fields) {
      const id = "f-" + path.replace(/\./g, "-");
      const v = getPath(CFG, path);
      let ctl;
      switch (f.type) {
        case "bool":
          ctl = `<input type="checkbox" id="${id}" ${v ? "checked" : ""}>`;
          break;
        case "select":
          ctl = `<select id="${id}">${f.options.map(([ov, ol]) => `<option value="${esc(ov)}" ${String(ov) === String(v) ? "selected" : ""}>${esc(ol)}</option>`).join("")}</select>`;
          break;
        case "lines":
          ctl = `<textarea id="${id}" rows="4" spellcheck="false">${esc((v || []).join("\n"))}</textarea>`;
          break;
        case "csv":
          ctl = `<input type="text" id="${id}" value="${esc((v || []).join(", "))}">`;
          break;
        case "num":
        case "int":
          ctl = `<span class="row"><input type="number" id="${id}" step="${f.type === "int" ? 1 : "any"}" value="${esc(v)}">${f.unit ? `<span class="unit">${esc(f.unit)}</span>` : ""}</span>`;
          break;
        case "password":
          ctl = `<input type="password" id="${id}" value="${esc(v)}" autocomplete="new-password">`;
          break;
        default:
          ctl = `<input type="text" id="${id}" value="${esc(v)}" ${f.list ? `list="${f.list}"` : ""} spellcheck="false">`;
      }
      html += `<div class="field${f.type === "bool" ? " check" : ""}" data-path="${path}"><label for="${id}">${esc(label)}</label>
        <div class="control">${ctl}${f.help ? `<p class="help">${esc(f.help)}</p>` : ""}</div></div>`;
    }
    html += `</fieldset>`;
  }
  form.innerHTML = html;
  $("#settings-nav").innerHTML = SCHEMA.map((g) => `<a href="#set-${g.id}" data-jump="set-${g.id}">${esc(g.title)}</a>`).join("");
  settingsDirty = false;
  $("#save-msg").textContent = "";
  $("#save-msg").className = "";
  applyShowRules();
}

function readSettings() {
  const cfg = JSON.parse(JSON.stringify(CFG));
  for (const g of SCHEMA) {
    for (const [path, , f] of g.fields) {
      const el = document.getElementById("f-" + path.replace(/\./g, "-"));
      if (!el) continue;
      let v;
      switch (f.type) {
        case "bool": v = el.checked; break;
        case "num": v = el.value === "" ? 0 : Number(el.value); break;
        case "int": v = el.value === "" ? 0 : parseInt(el.value, 10); break;
        case "lines": v = el.value.split("\n").map((s) => s.trim()).filter(Boolean); break;
        case "csv": v = el.value.split(",").map((s) => s.trim()).filter(Boolean); break;
        case "select": v = f.number ? Number(el.value) : el.value; break;
        default: v = el.value.trim();
      }
      setPath(cfg, path, v);
    }
  }
  return cfg;
}

function applyShowRules() {
  const cfg = readSettings();
  for (const g of SCHEMA) {
    for (const [path, , f] of g.fields) {
      if (!f.show) continue;
      const row = document.querySelector(`.field[data-path="${path}"]`);
      if (row) row.hidden = !f.show(cfg);
    }
  }
}

$("#settings-form").addEventListener("input", () => {
  settingsDirty = true;
  $("#save-msg").className = "";
  $("#save-msg").textContent = "Unsaved changes";
  applyShowRules();
});
$("#settings-nav").addEventListener("click", (ev) => {
  const a = ev.target.closest("a[data-jump]");
  if (!a) return;
  ev.preventDefault();
  document.getElementById(a.dataset.jump).scrollIntoView({ behavior: "smooth", block: "start" });
});
$("#settings-reset").addEventListener("click", () => loadSettings(true));
$("#settings-save").addEventListener("click", async () => {
  const btn = $("#settings-save"), msg = $("#save-msg");
  btn.disabled = true;
  try {
    const r = await api("PUT", "/api/config", readSettings());
    settingsDirty = false;
    msg.className = "good";
    msg.textContent = r.restart_needed ? "Settings saved. Restart Aether Time for the dashboard and update changes to take effect." : "Settings saved.";
    CFG = await api("GET", "/api/config");
  } catch (err) {
    msg.className = "bad";
    msg.textContent = err.message;
  } finally {
    btn.disabled = false;
  }
});
window.addEventListener("beforeunload", (ev) => { if (settingsDirty) ev.preventDefault(); });

/* ---------- start ---------- */
buildMeter();
buildCycle(15);
connect();
api("GET", "/api/config").then((c) => { CFG = c; }).catch(() => {});
setInterval(tickClock, 100);
setInterval(loadHistory, 10000);
setInterval(() => { if (!$("#view-log").hidden) loadLog(); }, 5000);
window.addEventListener("resize", () => {
  clearTimeout(window.__rz);
  window.__rz = setTimeout(() => { buildMeter(); loadHistory(); }, 200);
});
showView(location.hash === "#settings" ? "settings" : location.hash === "#log" ? "log" : "dash");
