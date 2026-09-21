/* xcut web UI — vanilla JS, no framework, no build step. */
"use strict";

const $ = (id) => document.getElementById(id);
let currentProject = null;
let pollTimer = null;

/* ---------- i18n ---------- */
/* Dictionary lives in i18n.js (window.XCUT_I18N, plain JSON so the Go test
 * gate can parse it). Keys are the English source strings; t() falls back
 * to the key itself, so English needs no entry. The choice persists in
 * localStorage and defaults to the browser language. */
const I18N = (typeof window !== "undefined" && window.XCUT_I18N) || {};
let lang = "en";
try {
  lang = localStorage.getItem("xcut_lang") ||
    ((navigator.language || "").toLowerCase().startsWith("zh") ? "zh" : "en");
} catch (_) { /* storage unavailable — stay with the default */ }
if (lang !== "zh") lang = "en";

const t = (s) => (I18N[lang] && I18N[lang][s]) || s;
const tf = (s, params) =>
  t(s).replace(/\{(\w+)\}/g, (m, k) => (params && k in params) ? String(params[k]) : m);

function applyI18n() {
  document.documentElement.lang = lang === "zh" ? "zh-CN" : "en";
  for (const el of document.querySelectorAll("[data-i18n]")) el.textContent = t(el.dataset.i18n);
  for (const el of document.querySelectorAll("[data-i18n-title]")) el.title = t(el.dataset.i18nTitle);
  for (const el of document.querySelectorAll("[data-i18n-placeholder]")) el.placeholder = t(el.dataset.i18nPlaceholder);
  // A placeholder is not an accessible name: the sign-in field for a remote
  // server is the one input a screen reader has to describe without context.
  for (const el of document.querySelectorAll("[data-i18n-aria]")) el.setAttribute("aria-label", t(el.dataset.i18nAria));
  $("lang").value = lang;
}

function setLang(l) {
  lang = l === "zh" ? "zh" : "en";
  try { localStorage.setItem("xcut_lang", lang); } catch (_) { /* private mode etc. */ }
  applyI18n();
  // Re-render every dynamic surface so in-flight text follows the switch;
  // each of these guards itself when no project is open.
  refreshHealth();
  refreshTimeline();
  refreshJobs();
  refreshSubtitlesStatus();
  refreshROIStatus();
}

/* ---------- remote sessions (D12 follow-up) ----------
 * A serve reachable from another machine answers 401 until it holds proof.
 * The token is typed once, exchanged for a session id, and then dropped: the
 * browser sends the id in a header for changes and the server's HttpOnly
 * cookie carries it for media URLs (<video>, thumbnails, downloads), which a
 * page cannot give a header. That is why the token never lands in
 * localStorage — only the session id does, and it cannot read or change
 * anything on its own over a GET. */
let sessionId = "";
try { sessionId = sessionStorage.getItem("xcut-session") || ""; } catch (_) { /* storage off */ }

function authed(opts) {
  opts = opts || {};
  if (sessionId) {
    opts.headers = Object.assign({}, opts.headers, { "X-Cut-Session": sessionId });
  }
  return opts;
}

function setSession(id) {
  sessionId = id || "";
  try {
    if (sessionId) sessionStorage.setItem("xcut-session", sessionId);
    else sessionStorage.removeItem("xcut-session");
  } catch (_) { /* storage off — the session lives for this page only */ }
  const btn = $("sign-out");
  if (btn) btn.hidden = !sessionId;
}

let signInOpen = null; // one prompt at a time: parallel 401s must not stack

// askSignIn shows the token panel and resolves true once a session exists,
// false if the caller should give up. Multiple concurrent 401s share one
// prompt — a modal per in-flight request would be unwinnable.
function askSignIn() {
  if (signInOpen) return signInOpen;
  const modal = $("login-modal");
  if (!modal) return Promise.resolve(false);
  const input = $("login-token"), err = $("login-error"), btn = $("login-submit");
  modal.hidden = false;
  err.hidden = true;
  input.value = "";
  setTimeout(() => input.focus(), 0);

  signInOpen = new Promise((resolve) => {
    const finish = (ok) => {
      modal.hidden = true;
      btn.removeEventListener("click", submit);
      input.removeEventListener("keydown", onKey);
      signInOpen = null;
      resolve(ok);
    };
    async function submit() {
      const token = input.value.trim();
      if (!token) return;
      btn.disabled = true;
      try {
        const resp = await fetch("/api/v1/session", {
          method: "POST",
          headers: { "Authorization": "Bearer " + token },
        });
        if (!resp.ok) {
          const body = await resp.json().catch(() => ({}));
          // A wrong token is a fact about the keystrokes, not a stack trace.
          err.textContent = body.error === "unauthorized"
            ? t("That token is not valid here.")
            : (body.message || resp.status + "");
          err.hidden = false;
          input.select();
        } else {
          const doc = await resp.json();
          setSession(doc.session || "");
          finish(true);
        }
      } catch (e) {
        err.textContent = t("Cannot reach this server.");
        err.hidden = false;
      }
      btn.disabled = false;
    }
    function onKey(ev) { if (ev.key === "Enter") submit(); }
    btn.addEventListener("click", submit);
    input.addEventListener("keydown", onKey);
  });
  return signInOpen;
}

async function signOut() {
  try {
    await fetch("/api/v1/session", { method: "DELETE", headers: sessionId ? { "X-Cut-Session": sessionId } : {} });
  } catch (_) { /* the local state is what the button promises */ }
  setSession("");
  refreshProjects();
}

async function api(path, opts) {
  let resp = await fetch(path, authed(opts));
  if (resp.status === 401) {
    if (!(await askSignIn())) {
      throw new Error("401: " + t("sign-in required"));
    }
    resp = await fetch(path, authed(opts)); // one retry, now with the session
  }
  let body = {};
  try { body = await resp.json(); } catch (_) { /* non-JSON */ }
  if (!resp.ok) {
    const msg = body.message || resp.statusText;
    throw new Error(`${resp.status}: ${msg}`);
  }
  return body;
}

async function post(path, body) {
  return api(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body || {}),
  });
}

function busy(on) {
  for (const id of ["btn-analyze", "btn-timeline", "btn-render", "btn-subtitles", "btn-subs-preview"]) {
    $(id).disabled = on;
  }
}

function banner(msg) {
  const el = $("banner");
  if (!msg) { el.hidden = true; return; }
  el.textContent = msg;
  el.hidden = false;
  clearTimeout(banner._t);
  banner._t = setTimeout(() => { el.hidden = true; }, 6000);
}

/* ---------- health ---------- */
async function refreshHealth() {
  const el = $("health");
  const warn = $("syswarn");
  try {
    const h = await api("/api/v1/health");
    el.textContent = `v${h.version} · ${t("online")}`;
    el.className = "health ok";
    // Setup guidance: a double-clicked exe usually means no PATH-managed
    // FFmpeg. Stay visible until the toolchain appears; the one-click
    // installer (pinned official source) sits in the same strip.
    warn.hidden = h.ffmpeg !== "missing";
    $("btn-setup-ffmpeg").hidden = h.ffmpeg === "ok" || installPoll !== null;
    if (warn.hidden) $("syswarn-text").textContent = "";
    else $("syswarn-text").textContent = t("FFmpeg not found — put ffmpeg.exe and ffprobe.exe next to xcut.exe (or in a bin folder beside it), or install them on PATH, then restart.");
  } catch (_) {
    el.textContent = t("offline");
    el.className = "health bad";
  }
}

/* ---------- FFmpeg one-click install (pinned official source) ---------- */
let installPoll = null;
$("btn-setup-ffmpeg").addEventListener("click", async () => {
  const btn = $("btn-setup-ffmpeg");
  const st = $("setup-status");
  btn.disabled = true;
  try {
    await post("/api/v1/setup/ffmpeg", {});
  } catch (e) {
    banner(tf("FFmpeg install failed: {msg}", { msg: e.message }));
    btn.disabled = false;
    return;
  }
  btn.hidden = true;
  installPoll = setInterval(async () => {
    let s;
    try { s = await api("/api/v1/setup/ffmpeg"); } catch (_) { return; }
    if (s.phase === "downloading") {
      st.textContent = tf("installing FFmpeg… {pct}%", { pct: Math.round(s.progress_pct) });
    } else if (s.phase === "extracting") {
      st.textContent = t("unpacking FFmpeg…");
    } else if (s.phase === "verifying") {
      st.textContent = t("verifying FFmpeg…");
    } else if (s.phase === "done") {
      clearInterval(installPoll);
      installPoll = null;
      st.textContent = "";
      btn.disabled = false;
      banner(t("FFmpeg installed — the pipeline is ready"));
      await refreshHealth();
    } else if (s.phase === "error" || s.phase === "unavailable") {
      clearInterval(installPoll);
      installPoll = null;
      st.textContent = "";
      btn.disabled = false;
      btn.hidden = false;
      banner(tf("FFmpeg install failed: {msg}", { msg: s.error || "installer unavailable" }));
    }
  }, 800);
});

/* ---------- projects ---------- */
async function refreshProjects() {
  const { projects } = await api("/api/v1/projects");
  const menu = $("project-list");
  menu.innerHTML = "";
  for (const p of projects) {
    const li = document.createElement("li");
    li.textContent = p.name;
    if (currentProject && p.id === currentProject.id) li.classList.add("active");
    const meta = document.createElement("span");
    meta.className = "meta";
    // created_at is a unix timestamp (seconds).
    const d = new Date(p.created_at * 1000);
    meta.textContent = isNaN(d) ? "" : d.toISOString().slice(0, 10);
    li.appendChild(meta);
    li.addEventListener("click", () => { closeProjMenu(); selectProject(p); });
    menu.appendChild(li);
  }
}

function closeProjMenu() { $("project-list").classList.remove("open"); }

$("proj-current").addEventListener("click", () => {
  $("project-list").classList.toggle("open");
});
document.addEventListener("click", (e) => {
  if (!e.target.closest(".proj-picker")) closeProjMenu();
});

$("new-project").addEventListener("submit", async (e) => {
  e.preventDefault();
  try {
    await post("/api/v1/projects", { name: $("np-name").value });
    $("np-name").value = "";
    await refreshProjects();
  } catch (err) { banner(tf("Create failed: {msg}", { msg: err.message })); }
});

function selectProject(p) {
  currentProject = p;
  $("project-view").hidden = false;
  $("detail").classList.remove("empty");
  const ph = document.querySelector("#detail .placeholder");
  if (ph) ph.style.display = "none";
  $("proj-current").innerHTML = "";
  $("proj-current").append(p.name, Object.assign(document.createElement("span"), { className: "caret", textContent: " ▾" }));
  $("proj-current").hidden = false;
  $("delete-project").hidden = false;
  // Reset the player: without this, switching from a project that has a
  // render shows the OLD project's video (and its download link) under the
  // new project until the new project renders something.
  $("player").removeAttribute("src");
  $("player").load();
  $("download").href = "#";
  $("btn-play").textContent = "▶";
  // Disarm the regeneration confirm across project switches.
  const tl = $("btn-timeline");
  tl.dataset.armed = "";
  tl.textContent = t("2 · Timeline");
  refreshAssets();
  refreshJobs();
  refreshTimeline();
  refreshSubtitlesStatus();
  refreshROIStatus();
}

async function deleteProject() {
  if (!currentProject) return;
  // Two-step confirm without confirm(): the button asks once more.
  const btn = $("delete-project");
  if (btn.dataset.armed !== "1") {
    btn.dataset.armed = "1";
    btn.title = tf('really delete "{name}"? click again', { name: currentProject.name });
    setTimeout(() => { btn.dataset.armed = ""; btn.title = t("delete this project"); }, 4000);
    return;
  }
  btn.dataset.armed = "";
  btn.title = t("delete this project");
  try {
    await api(`/api/v1/projects/${currentProject.id}`, { method: "DELETE" });
    currentProject = null;
    $("project-view").hidden = true;
    $("detail").classList.add("empty");
    const ph = document.querySelector("#detail .placeholder");
    if (ph) ph.style.display = "";
    btn.hidden = true;
    $("player").removeAttribute("src");
    $("proj-current").hidden = true;
    await refreshProjects();
  } catch (e) { banner(tf("Delete failed: {msg}", { msg: e.message })); }
}

/* ---------- media pool ---------- */
// Stale-response guard: every project-scoped refresh captures the project id
// when the request starts and discards the response if the user has since
// switched projects — otherwise a slow response from A lands after B was
// selected and renders A's rows (or worse, A's timeline document) under B.
function projectChangedSince(pid) {
  return !currentProject || currentProject.id !== pid;
}

/* ---------- file / drag-drop upload ---------- */
/* Browsers cannot reveal a local path, so picked or dropped files go to
 * the server as content: POST assets/upload lands a copy under the
 * workspace imports/ dir and runs the standard probe+import there. The
 * user's original file is never touched. */
function uploadFile(pid, file, onProgress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `/api/v1/projects/${pid}/assets/upload?filename=` +
      encodeURIComponent(file.name));
    // Uploads are mutations, so the session id has to ride in the header; a
    // cookie alone would be refused by design.
    if (sessionId) xhr.setRequestHeader("X-Cut-Session", sessionId);
    xhr.responseType = "json";
    xhr.upload.addEventListener("progress", (e) => {
      if (e.lengthComputable && onProgress) {
        onProgress(Math.round((e.loaded / e.total) * 100));
      }
    });
    xhr.onload = () => {
      if (xhr.status === 201) resolve(xhr.response.asset);
      else reject(new Error(`${xhr.status}: ${(xhr.response && xhr.response.message) || xhr.statusText}`));
    };
    xhr.onerror = () => reject(new Error("network error"));
    xhr.send(file);
  });
}

async function uploadFiles(files) {
  if (!currentProject || !files || !files.length) return;
  const pid = currentProject.id;
  const status = $("upload-status");
  let ok = 0;
  for (const f of files) {
    try {
      await uploadFile(pid, f, (pct) => {
        status.textContent = tf("uploading {name}… {pct}%", { name: f.name, pct });
      });
      ok++;
    } catch (e) {
      banner(tf("Upload failed: {msg}", { msg: e.message }));
    }
  }
  status.textContent = "";
  if (ok) {
    banner(tf("Imported {n} file(s) from upload", { n: ok }));
    await refreshAssets();
  }
}

$("btn-pick-files").addEventListener("click", () => $("import-file").click());
$("import-file").addEventListener("change", (e) => {
  const files = [...e.target.files];
  e.target.value = "";
  uploadFiles(files);
});
{
  const panel = $("panel-media");
  panel.addEventListener("dragover", (e) => { e.preventDefault(); });
  panel.addEventListener("drop", (e) => {
    e.preventDefault();
    uploadFiles([...e.dataTransfer.files]);
  });
}

// The path form sits beside pick/drop for files that already live on this
// machine (pick/drop cannot see a local path — the browser hides it). Without
// this handler the submit button performs a native form submission: the page
// reloads, the selected project is lost, and nothing is imported — a
// regression the workspace redesign shipped and no id-existence test can see.
$("import-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!currentProject) return;
  const path = $("import-path").value.trim();
  if (!path) return;
  try {
    await trigger("/assets", { path });
    $("import-path").value = "";
  } catch (err) { banner(tf("Import failed: {msg}", { msg: err.message })); busy(false); }
});

function fmtDur(s) {
  return `${Math.floor(s / 60)}:${String(Math.round(s % 60)).padStart(2, "0")}`;
}

// thumbCache: asset id → dataURL, captured once by the media pool's
// client-side frame grab and reused by the timeline strip blocks.
const thumbCache = new Map();

// Capturing a frame means pulling real media bytes through a hidden <video>
// and spinning up a decoder — per asset, per reload. Since serve can be
// reached from another machine (D14) those bytes cross a network, so the
// result is kept between loads, keyed by asset id and invalidated by the
// content fingerprint a re-imported file changes.
const THUMB_PREFIX = "xcut-thumb:";
const THUMB_MAX = 400; // ~4 KB each; the cap bounds localStorage growth

function cachedThumb(a) {
  try {
    const raw = localStorage.getItem(THUMB_PREFIX + a.id);
    if (!raw) return "";
    const t = JSON.parse(raw);
    return (t && t.fp === a.fingerprint) ? t.url : "";
  } catch (_) { return ""; } // storage off or a torn write: re-capture
}

function storeThumb(a, url) {
  try {
    localStorage.setItem(THUMB_PREFIX + a.id, JSON.stringify({ fp: a.fingerprint, url }));
    const keys = [];
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i);
      if (k && k.startsWith(THUMB_PREFIX)) keys.push(k);
    }
    for (const k of keys.slice(0, Math.max(0, keys.length - THUMB_MAX))) {
      localStorage.removeItem(k);
    }
  } catch (_) {
    // Out of quota: thumbnails are decoration, so drop the whole set rather
    // than let a full localStorage break the media panel.
    try {
      const keys = [];
      for (let i = 0; i < localStorage.length; i++) {
        const k = localStorage.key(i);
        if (k && k.startsWith(THUMB_PREFIX)) keys.push(k);
      }
      for (const k of keys) localStorage.removeItem(k);
    } catch (__) { /* nothing left to do */ }
  }
}

function paintThumb(el, url) {
  const img = document.createElement("img");
  img.src = url;
  img.alt = "";
  el.appendChild(img);
}

// One repaint per batch: with N assets each landing at its own time, an
// unguarded renderTimeline() meant up to N full timeline redraws per refresh.
let thumbRepaintTimer = null;
function scheduleThumbRepaint() {
  if (!timelineDoc) return;
  clearTimeout(thumbRepaintTimer);
  thumbRepaintTimer = setTimeout(() => { renderTimeline(); }, 120);
}

async function refreshAssets() {
  if (!currentProject) return;
  const pid = currentProject.id;
  const { assets } = await api(`/api/v1/projects/${pid}`);
  if (projectChangedSince(pid)) return;
  const grid = $("asset-grid");
  grid.innerHTML = "";
  for (const a of assets) {
    const mb = (a.size_bytes / 1048576).toFixed(1) + " MB";
    const card = document.createElement("div");
    card.className = "asset-card";
    const thumb = document.createElement("div");
    thumb.className = "asset-thumb";
    const meta = document.createElement("div");
    meta.className = "asset-meta";
    // Untrusted by policy: filenames are user input, textContent only.
    const name = document.createElement("div");
    name.className = "asset-name";
    name.textContent = a.filename;
    name.title = a.filename;
    const sub = document.createElement("div");
    sub.className = "asset-sub";
    sub.textContent = `${fmtDur(a.duration_s)} · ${mb} · ${a.video_codec}${a.has_audio ? " + " + a.audio_codec : ""}`;
    meta.append(name, sub);
    card.append(thumb, meta);
    grid.appendChild(card);
    const held = cachedThumb(a);
    if (held) {
      thumbCache.set(a.id, held);
      paintThumb(thumb, held);
      continue; // no media fetch, no decoder
    }
    // Real thumbnail: seek a client-side video to 1s and paint a frame.
    const v = document.createElement("video");
    v.muted = true;
    v.preload = "metadata";
    v.style.display = "none";
    v.src = `/api/v1/projects/${pid}/assets/${encodeURIComponent(a.id)}/file#t=1`;
    v.addEventListener("loadeddata", () => {
      try {
        const c = document.createElement("canvas");
        c.width = 184; c.height = 104;
        c.getContext("2d").drawImage(v, 0, 0, c.width, c.height);
        thumb.appendChild(c);
        const url = c.toDataURL("image/jpeg", 0.6);
        thumbCache.set(a.id, url);
        storeThumb(a, url);
        v.src = ""; // release the decoder
        // The timeline may have rendered before this capture landed (its
        // blocks read the same cache); repaint once so blocks are not dark.
        scheduleThumbRepaint();
      } catch (_) { /* frame capture is best-effort */ }
    }, { once: true });
    grid.appendChild(v);
  }
  // The transcribe/ROI picker mirrors the asset list (first = default).
  // Rebuilding it invalidates whatever the ROI status line was showing,
  // so refresh that too — after a project switch the status would
  // otherwise stay blank until the user touches the picker by hand.
  const sel = $("subs-asset");
  sel.innerHTML = "";
  for (const a of assets) {
    const o = document.createElement("option");
    o.value = a.id;
    o.textContent = a.filename;
    sel.appendChild(o);
  }
  refreshROIStatus();
}

/* ---------- jobs ---------- */
// Polling must survive transient failures (serve restart, laptop sleep):
// one unhandled rejection used to stop rescheduling forever, silently
// freezing the job panel and leaving busy() buttons stuck.
let pollFailures = 0;
async function refreshJobs() {
  if (!currentProject) {
    // Keep the loop alive: a deleted project must not kill polling
    // permanently (the next selection reuses this timer).
    schedulePoll(2500);
    return;
  }
  const pid = currentProject.id;
  try {
    const { jobs } = await api(`/api/v1/projects/${pid}/jobs`);
    pollFailures = 0;
    if (projectChangedSince(pid)) { schedulePoll(2500); return; }
    const ul = $("jobs");
    ul.innerHTML = "";
    let running = false;
    for (const j of jobs.slice(0, 12)) {
      const li = document.createElement("li");
      li.className = j.status;
      const pct = j.status === "running" ? ` ${Math.round((j.progress || 0) * 100)}%` : "";
      // error_message may echo hostile input (e.g. filenames): textContent only.
      const status = document.createElement("span");
      status.className = "status";
      status.textContent = `${j.status}${pct}`;
      const type = document.createElement("span");
      type.className = "type";
      type.textContent = j.type;
      const meta = document.createElement("span");
      meta.className = "meta";
      meta.textContent = j.error_message || j.id;
      li.append(status, type, meta);
      if (j.status === "running" || j.status === "queued") {
        running = true;
        const cancel = document.createElement("button");
        cancel.className = "cancel";
        cancel.textContent = t("Cancel");
        cancel.addEventListener("click", () => cancelJob(j.id));
        li.append(cancel);
      }
      ul.appendChild(li);
    }
    busy(running);
    // A subtitles job in the batch (started anywhere — CLI, another tab)
    // means the artifacts may have just changed: refresh the panel.
    if (jobs.some((j) => j.type === "subtitles")) refreshSubtitlesStatus();
    // Refresh assets once after a batch of work likely changed them.
    if (!running) refreshAssetsSoon();
    schedulePoll(running ? 800 : 2500);
  } catch (e) {
    pollFailures++;
    if (pollFailures === 5) banner(t("Lost contact with the server — retrying…"));
    schedulePoll(Math.min(2500 * pollFailures, 15000));
  }
}

async function cancelJob(jobID) {
  try {
    await post(`/api/v1/jobs/${jobID}/cancel`, {});
  } catch (e) {
    banner(tf("Cancel failed: {msg}", { msg: e.message }));
  }
  refreshJobs();
}

let assetsTimer = null;
function refreshAssetsSoon() {
  if (assetsTimer) return;
  assetsTimer = setTimeout(() => { assetsTimer = null; refreshAssets(); }, 400);
}

function schedulePoll(ms) {
  clearTimeout(pollTimer);
  pollTimer = setTimeout(refreshJobs, ms);
}

/* ---------- actions ---------- */
async function trigger(path, body) {
  busy(true);
  try {
    const { job_id } = await post(`/api/v1/projects/${currentProject.id}${path}`, body);
    await refreshJobs();
    watchUntilDone(jobIdOf(job_id));
  } catch (e) {
    busy(false);
    throw e; // caller renders the banner
  }
}

function jobIdOf(id) { return id; }

async function watchUntilDone(jobID) {
  // refreshJobs polls anyway; this re-enables once nothing is running.
  // A job row that is gone for good (pruned, or serve restarted mid-job)
  // must not keep this timer polling a 404 at 1Hz forever.
  const pid = currentProject && currentProject.id;
  let misses = 0;
  const timer = setInterval(async () => {
    let job;
    try {
      job = (await api(`/api/v1/jobs/${jobID}`)).job;
      misses = 0;
    } catch (_) {
      if (++misses < 5) return; // transient — the poll loop also retries
      clearInterval(timer);
      busy(false);
      refreshJobs();
      return;
    }
    if (["succeeded", "failed", "cancelled"].includes(job.status)) {
      clearInterval(timer);
      if (projectChangedSince(pid)) return; // A's job ending must not touch B's editor
      if (job.status === "succeeded" && job.type === "timeline") await refreshTimeline();
      if (job.status === "succeeded" && job.type === "render") showPlayer();
      if (job.type === "subtitles") await refreshSubtitlesStatus();
    }
  }, 1000);
}

function showPlayer() {
  if (!currentProject) return;
  $("player").src = `/api/v1/projects/${currentProject.id}/render?t=${Date.now()}`;
  $("download").href = `/api/v1/projects/${currentProject.id}/render`;
}

/* ---------- preview transport ---------- */
$("btn-play").addEventListener("click", togglePlay);
function togglePlay() {
  const p = $("player");
  if (!p.src) return;
  if (p.paused) p.play().catch(() => {});
  else p.pause();
}
function fmtClock(s) {
  if (!isFinite(s)) return "0:00";
  const m = Math.floor(s / 60);
  return `${m}:${String(Math.floor(s % 60)).padStart(2, "0")}`;
}
$("player").addEventListener("timeupdate", () => {
  const p = $("player");
  $("player-time").textContent = `${fmtClock(p.currentTime)} / ${fmtClock(p.duration)}`;
});
$("player").addEventListener("play", () => { $("btn-play").textContent = "⏸"; });
$("player").addEventListener("pause", () => { $("btn-play").textContent = "▶"; });

/* ---------- timeline editing ---------- */
let timelineDoc = null;   // last fetched timeline JSON
let clipEdits = null;     // working copy of timelineDoc.tracks[0].clips
let dragIndex = -1;       // clipEdits index being dragged (HTML5 DnD reorder)
let selectedIndex = -1;   // clip open in the inspector

async function refreshTimeline() {
  timelineDoc = null;
  clipEdits = null;
  dragIndex = -1;
  selectedIndex = -1;
  const prev = $("clip-preview");
  if (prev) { prev.hidden = true; prev.removeAttribute("src"); prev.load(); }
  renderInspector();
  renderTimeline();
  if (!currentProject) return;
  const pid = currentProject.id;
  try {
    const { timeline, has_backup } = await api(`/api/v1/projects/${pid}/timeline`);
    if (projectChangedSince(pid)) return; // stale: A's document must not become B's editing state
    timelineDoc = timeline;
    clipEdits = JSON.parse(JSON.stringify(timeline.tracks[0].clips));
    $("btn-tl-restore").hidden = !has_backup;
  } catch (_) { /* no timeline yet — expected before first generation */ }
  renderTimeline();
}

const EPS = 1e-6;
const playDur = (c) => (c.source_end - c.source_start) / (c.speed > 0 ? c.speed : 1);

function totalDuration() {
  if (!clipEdits || clipEdits.length === 0) return 0;
  const last = clipEdits[clipEdits.length - 1];
  return last.timeline_start + playDur(last);
}

// renderTimeline draws the visual strip: a time ruler, one block per clip
// (width ∝ timeline duration), a transition badge on every join, and the
// playhead. Click selects (→ inspector), drag reorders, edge handles trim.
function renderTimeline() {
  const strip = $("tl-strip");
  strip.innerHTML = "";
  const has = Array.isArray(clipEdits) && clipEdits.length > 0;
  $("btn-tl-save").disabled = !clipEdits;
  $("btn-tl-reset").disabled = !clipEdits;
  if (!has) $("btn-tl-restore").hidden = true;
  if (!has) {
    const empty = document.createElement("div");
    empty.className = "tl-empty";
    empty.textContent = timelineDoc === null ? t("generate a timeline (step 2) to start editing") : t("every clip removed — save to empty it, or Reset");
    strip.appendChild(empty);
    renderInspector();
    return;
  }

  const total = Math.max(totalDuration(), 0.001);
  const w = strip.clientWidth || 800;
  const px = (t) => (t / total) * (w - 4) + 2;

  const ruler = document.createElement("div");
  ruler.className = "tl-ruler";
  const tickStep = niceTick(total);
  for (let t = 0; t <= total; t += tickStep) {
    const tick = document.createElement("div");
    tick.className = "tick";
    tick.style.left = px(t) + "px";
    tick.textContent = fmtClock(t);
    tick.addEventListener("click", (e) => { e.stopPropagation(); seekPreviewTo(t); });
    ruler.appendChild(tick);
  }
  strip.appendChild(ruler);

  const track = document.createElement("div");
  track.className = "tl-track";
  strip.appendChild(track);

  clipEdits.forEach((c, i) => {
    if (c._removed) return;
    const startX = px(c.timeline_start);
    const endX = px(c.timeline_start + playDur(c));
    const block = document.createElement("div");
    block.className = "tl-clip" + (i === selectedIndex ? " selected" : "");
    block.style.left = startX + "px";
    block.style.width = Math.max(endX - startX, 24) + "px";
    block.draggable = true;

    const thumb = document.createElement("div");
    thumb.className = "thumb";
    if (thumbCache.has(c.asset_id)) {
      thumb.style.backgroundImage = `url(${thumbCache.get(c.asset_id)})`;
    }
    const lbl = document.createElement("div");
    lbl.className = "lbl";
    const asset = (timelineDoc.tracks[0].clips.find((o) => o.asset_id === c.asset_id) || {});
    const nm = document.createElement("div");
    nm.className = "name";
    nm.textContent = asset.source || tf("clip {n}", { n: i + 1 });
    const sub = document.createElement("div");
    sub.className = "sub";
    sub.textContent = `${fmtClock(c.source_start)}–${fmtClock(c.source_end)} @${c.speed}x`;
    lbl.append(nm, sub);
    block.append(thumb, lbl);

    const hL = document.createElement("div");
    hL.className = "trimL";
    hL.title = t("drag to trim the head");
    const hR = document.createElement("div");
    hR.className = "trimR";
    hR.title = t("drag to trim the tail");
    block.append(hL, hR);

    block.addEventListener("click", (e) => {
      if (e.target.classList.contains("trimL") || e.target.classList.contains("trimR")) return;
      selectClip(i);
    });
    block.addEventListener("dragstart", (e) => {
      dragIndex = i;
      block.classList.add("dragging");
      e.dataTransfer.effectAllowed = "move";
      try { e.dataTransfer.setData("text/plain", String(i)); } catch (_) { /* IE-style targets */ }
    });
    block.addEventListener("dragend", () => block.classList.remove("dragging"));
    block.addEventListener("dragover", (e) => {
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
      block.classList.toggle("tl-drop-hint", dragIndex >= 0 && dragIndex !== i);
    });
    block.addEventListener("dragleave", () => block.classList.remove("tl-drop-hint"));
    block.addEventListener("drop", (e) => {
      e.preventDefault();
      block.classList.remove("tl-drop-hint");
      if (dragIndex < 0 || dragIndex === i) return;
      const [moved] = clipEdits.splice(dragIndex, 1);
      clipEdits.splice(i, 0, moved);
      if (selectedIndex === dragIndex) selectedIndex = i;
      else if (dragIndex < selectedIndex && selectedIndex <= i) selectedIndex--;
      else if (i <= selectedIndex && selectedIndex < dragIndex) selectedIndex++;
      dragIndex = -1;
      renderTimeline();
    });
    attachTrim(block, hL, hR, i, -1, px, total);
    attachTrim(block, hR, hL, i, +1, px, total);
    track.appendChild(block);
  });

  // Transition badges on every join (the join belongs to the LEFT clip).
  for (let i = 1; i < clipEdits.length; i++) {
    const prev = clipEdits[i - 1];
    if (prev._removed) continue;
    // `tr` not `t`: the global translator is t(); this loop's transition
    // must not shadow it (the old name collided when i18n landed).
    const tr = prev.transition;
    const kind = tr && tr.type !== "cut" ? tr.type : "cut";
    const badge = document.createElement("div");
    badge.className = "tl-join";
    badge.style.left = px(clipEdits[i].timeline_start) + "px";
    const k = document.createElement("span");
    k.className = "k";
    k.textContent = { xfade: "⤬", fade: "◐" }[kind] || "|";
    const lbl = document.createElement("span");
    lbl.textContent = kind === "xfade" ? `${(tr.duration || 0).toFixed(1)}s` : t(kind);
    badge.append(k, lbl);
    badge.title = tf("{kind} join — select the previous clip to edit it", { kind });
    badge.addEventListener("click", (e) => { e.stopPropagation(); selectClip(i - 1); });
    track.appendChild(badge);
  }

  const playhead = document.createElement("div");
  playhead.className = "tl-playhead";
  playhead.id = "tl-playhead";
  track.appendChild(playhead);
  renderInspector();
}

// niceTick picks a human ruler step so ~6-12 ticks fit any duration.
function niceTick(total) {
  const steps = [0.5, 1, 2, 5, 10, 15, 30, 60, 120, 300, 600];
  for (const s of steps) if (total / s <= 12) return s;
  return 900;
}

// attachTrim wires one edge handle: dragging adjusts the clip's source
// range (in/out). dir −1 = head (source_start), +1 = tail (source_end).
function attachTrim(block, handle, other, i, dir, px, total) {
  handle.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    e.stopPropagation();
    const strip = $("tl-strip");
    const startX = e.clientX;
    const clip = clipEdits[i];
    const orig = dir < 0 ? clip.source_start : clip.source_end;
    const secsPerPx = total / Math.max(strip.clientWidth, 1);
    const move = (ev) => {
      const dt = (ev.clientX - startX) * secsPerPx * (clip.speed > 0 ? clip.speed : 1);
      let v = orig + dir * dt;
      if (dir < 0) {
        v = Math.max(0, Math.min(v, clip.source_end - 0.1));
        clip.source_start = v;
      } else {
        v = Math.max(clip.source_start + 0.1, v);
        clip.source_end = v;
      }
      renderTimeline();
    };
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      renderTimeline();
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  });
}

function selectClip(i) {
  selectedIndex = i;
  renderTimeline();
}

function seekPreviewTo(t) {
  // Find the clip covering timeline position t and play its source there.
  for (let i = 0; i < (clipEdits || []).length; i++) {
    const c = clipEdits[i];
    if (c._removed) continue;
    const end = c.timeline_start + playDur(c);
    if (t >= c.timeline_start - EPS && t <= end + EPS) {
      selectClip(i);
      playClipAt(c, c.source_start + (t - c.timeline_start) * (c.speed > 0 ? c.speed : 1));
      return;
    }
  }
}

// previewClip plays the clip's source media from its start offset in the
// editor preview player. The media URL is the project-scoped asset endpoint
// (DB-registered path only — no client-supplied paths).
function previewClip(c) { playClipAt(c, c.source_start); }

function playClipAt(c, sourceAt) {
  const video = $("clip-preview");
  if (!video || !currentProject || !c.asset_id) return;
  video.hidden = false;
  if (!video.src.includes(`/assets/${encodeURIComponent(c.asset_id)}/`)) {
    video.src = `/api/v1/projects/${currentProject.id}/assets/${encodeURIComponent(c.asset_id)}/file`;
  }
  const seek = () => {
    if (isFinite(sourceAt) && sourceAt > 0) {
      try { video.currentTime = sourceAt; } catch (_) { /* not seekable yet */ }
    }
    video.play().catch(() => { /* autoplay policies — user can press play */ });
  };
  if (video.readyState >= 1) seek();
  else video.addEventListener("loadedmetadata", seek, { once: true });
}

// The playhead mirrors the per-clip preview's position over the strip.
$("clip-preview").addEventListener("timeupdate", () => {
  const ph = document.getElementById("tl-playhead");
  if (!ph || selectedIndex < 0 || !clipEdits) return;
  const c = clipEdits[selectedIndex];
  if (!c) return;
  const strip = document.getElementById("tl-strip");
  const total = Math.max(totalDuration(), 0.001);
  const p = c.timeline_start + $("clip-preview").currentTime * (c.speed > 0 ? c.speed : 1);
  ph.style.left = (p / total) * (strip.clientWidth - 4) + 2 + "px";
});

/* ---------- inspector ---------- */
function renderInspector() {
  const empty = $("insp-empty");
  const box = $("insp-clip");
  if (selectedIndex < 0 || !clipEdits || !clipEdits[selectedIndex]) {
    box.innerHTML = "";
    box.hidden = true;
    empty.hidden = false;
    return;
  }
  empty.hidden = true;
  box.hidden = false;
  const c = clipEdits[selectedIndex];
  box.innerHTML = "";

  const name = document.createElement("h3");
  name.textContent = tf("Clip {n}", { n: selectedIndex + 1 });
  box.appendChild(name);

  const stat = (k, v) => {
    const row = document.createElement("div");
    row.className = "insp-stat";
    const key = document.createElement("span");
    key.textContent = k;
    const val = document.createElement("b");
    val.textContent = v;
    row.append(key, val);
    return row;
  };
  box.appendChild(stat(t("timeline in"), fmtClock(c.timeline_start)));
  box.appendChild(stat(t("duration"), playDur(c).toFixed(1) + "s"));
  if (c.metadata && c.metadata.score !== undefined) {
    box.appendChild(stat(t("score"), String(c.metadata.score)));
    if (c.metadata.reason) box.appendChild(stat(t("why"), String(c.metadata.reason)));
  }

  const grid = document.createElement("div");
  grid.className = "insp-grid";
  const field = (label, type, value, attrs) => {
    const l = document.createElement("label");
    const s = document.createElement("span");
    s.textContent = label;
    const input = document.createElement("input");
    input.type = type;
    input.value = value;
    Object.assign(input, attrs || {});
    l.append(s, input);
    grid.appendChild(l);
    return input;
  };
  const inTrim = field(t("trim in (s)"), "number", c.source_start.toFixed(2), { step: "0.1", min: "0" });
  const outTrim = field(t("trim out (s)"), "number", c.source_end.toFixed(2), { step: "0.1" });
  const speed = field(t("speed (×)"), "number", String(c.speed), { step: "0.05", min: "0.1", max: "4" });
  const volume = field(t("volume (0–1)"), "number", String(c.volume), { step: "0.05", min: "0", max: "1" });
  box.appendChild(grid);

  // Transition editor: applies to the join AFTER this clip (server side the
  // transition rides the left clip). cut | fade | xfade + duration.
  // `tr` not `t` — the global translator owns the name t().
  const tr = c.transition || { type: "cut", duration: 0 };
  const trow = document.createElement("label");
  trow.className = "field";
  const tspan = document.createElement("span");
  tspan.textContent = t("transition to next clip");
  const tsel = document.createElement("select");
  for (const [v, label] of [["cut", t("cut (hard join)")], ["fade", t("fade (through black)")], ["xfade", t("crossfade (xfade)")]]) {
    const o = document.createElement("option");
    o.value = v; o.textContent = label;
    if (tr.type === v) o.selected = true;
    tsel.appendChild(o);
  }
  trow.append(tspan, tsel);
  const tdur = field(t("xfade duration (s)"), "number", (tr.duration || 0).toFixed(2), { step: "0.1", min: "0" });
  box.appendChild(trow);
  box.appendChild(tdur);

  const apply = document.createElement("div");
  apply.className = "insp-row";
  const btnApply = document.createElement("button");
  btnApply.textContent = t("Apply");
  btnApply.className = "primary";
  btnApply.addEventListener("click", () => {
    const ns = parseFloat(inTrim.value), ne = parseFloat(outTrim.value);
    if (!isFinite(ns) || !isFinite(ne) || ns < 0 || ne <= ns) {
      banner(t("Trim rejected: out must be after in"));
      return;
    }
    c.source_start = ns;
    c.source_end = ne;
    const sp = parseFloat(speed.value);
    if (isFinite(sp) && sp >= 0.1 && sp <= 4) c.speed = sp;
    const vol = parseFloat(volume.value);
    if (isFinite(vol) && vol >= 0 && vol <= 1) c.volume = vol;
    const tt = tsel.value;
    const td = parseFloat(tdur.value) || 0;
    if (tt === "cut") delete c.transition;
    else c.transition = { type: tt, duration: tt === "xfade" ? td : (td || 0) };
    renderTimeline();
  });
  const btnPreview = document.createElement("button");
  btnPreview.textContent = t("▶ preview");
  btnPreview.addEventListener("click", () => previewClip(c));
  const btnRemove = document.createElement("button");
  btnRemove.textContent = c._removed ? t("undo remove") : t("remove");
  btnRemove.className = "danger";
  btnRemove.addEventListener("click", () => {
    c._removed = !c._removed;
    selectedIndex = -1;
    renderTimeline();
  });
  apply.append(btnApply, btnPreview, btnRemove);
  box.appendChild(apply);

  const note = document.createElement("div");
  note.className = "insp-note";
  note.textContent = t("applies to the working copy — “Save changes” writes it to the server (revision-checked).");
  box.appendChild(note);
}

async function restoreBackup() {
  if (!currentProject) return;
  try {
    await api(`/api/v1/projects/${currentProject.id}/timeline/restore-backup`, { method: "POST" });
    banner(t("Timeline backup restored (the regenerated version is now the backup)"));
    await refreshTimeline();
  } catch (e) { banner(tf("Restore failed: {msg}", { msg: e.message })); }
}

async function saveTimeline() {
  if (!currentProject || !timelineDoc) return;
  const kept = clipEdits.filter(c => !c._removed);
  if (kept.length === 0) { banner(t("Cannot save: every clip is removed")); return; }
  const doc = JSON.parse(JSON.stringify(timelineDoc));
  // Optimistic concurrency: send the revision we read; a mismatch (another
  // tab saved, or the timeline was regenerated) is refused with 409.
  doc.revision = timelineDoc.revision || 0;
  let dropped = 0;
  let prevEnd = 0;
  const outs = [];
  kept.forEach((c, i) => {
    const copy = { ...c };
    delete copy._removed;
    copy.id = `clip_${i + 1}`;
    let start = prevEnd;
    if (i > 0) {
      // The join between kept[i-1] and kept[i] is described by the
      // PREVIOUS clip's transition. An xfade join shares a blend window
      // (the previous clip's tail overlaps this clip's head by the
      // transition duration); every other join is back-to-back (a fade
      // needs no overlap). Preserve the transition when its window still
      // fits both clips after the edit — reordering can change the
      // neighbors — and degrade loudly to a cut when an xfade does not,
      // instead of silently stripping every transition in the document.
      const prev = kept[i - 1];
      const t = prev.transition;
      if (t && t.type === "xfade") {
        const fits = t.duration > 0 &&
          t.duration <= playDur(prev) + 1e-9 && t.duration <= playDur(c) + 1e-9;
        if (fits) {
          start = prevEnd - t.duration;
        } else {
          dropped++;
          delete outs[i - 1].transition;
        }
      }
    }
    copy.timeline_start = start;
    // A trailing xfade has nothing to blend with (the renderer would
    // refuse the save) — degrade it too.
    if (i === kept.length - 1 && copy.transition && copy.transition.type === "xfade") {
      dropped++;
      delete copy.transition;
    }
    outs.push(copy);
    prevEnd = start + playDur(c);
  });
  doc.tracks[0].clips = outs;
  try {
    const resp = await fetch(`/api/v1/projects/${currentProject.id}/timeline`, authed({
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc),
    }));
    const body = await resp.json();
    if (!resp.ok) {
      banner(resp.status === 409
        ? t("Save rejected: the timeline changed elsewhere — press Reset to load the current version, then reapply your edits")
        : tf("Save rejected: {msg}", { msg: body.message || resp.statusText }));
      return;
    }
    timelineDoc.revision = body.revision;
    const droppedMsg = dropped
      ? tf(" — {n} transition(s) dropped (their joins no longer fit)", { n: dropped })
      : "";
    banner(tf("Timeline saved ({n} clips)", { n: body.clips }) + droppedMsg);
    await refreshTimeline();
  } catch (e) { banner(tf("Save failed: {msg}", { msg: e.message })); }
}

$("btn-tl-save").addEventListener("click", saveTimeline);
$("btn-tl-restore").addEventListener("click", restoreBackup);
$("btn-tl-reset").addEventListener("click", refreshTimeline);

/* ---------- keyboard ---------- */
document.addEventListener("keydown", (e) => {
  const typing = /^(input|select|textarea)$/i.test(e.target.tagName);
  if (e.key === "Escape") { closeProjMenu(); return; }
  if (typing) return;
  if (e.key === " ") { e.preventDefault(); togglePlay(); return; }
  if ((e.key === "Delete" || e.key === "Backspace") && selectedIndex >= 0 && clipEdits) {
    e.preventDefault();
    const c = clipEdits[selectedIndex];
    if (c) c._removed = !c._removed;
    selectedIndex = -1;
    renderTimeline();
    return;
  }
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
    e.preventDefault();
    if (!$("btn-tl-save").disabled) saveTimeline();
  }
});

/* ---------- wiring ---------- */
$("delete-project").addEventListener("click", deleteProject);

/* ---------- subtitles (AI sidecar) ---------- */
async function refreshSubtitlesStatus() {
  const status = $("subs-status");
  const links = $("subs-links");
  const previewBtn = $("btn-subs-preview");
  const transcript = $("subs-transcript");
  const hideTranscript = () => { transcript.hidden = true; transcript.textContent = ""; };
  if (!currentProject) {
    status.textContent = ""; links.innerHTML = "";
    previewBtn.hidden = true; hideTranscript();
    return;
  }
  const pid = currentProject.id;
  try {
    const st = await api(`/api/v1/projects/${pid}/subtitles`);
    if (projectChangedSince(pid)) return; // stale: panel belongs to the new project now
    if (!st.srt && !st.ass) {
      status.textContent = t("none yet — transcribe to create");
      links.innerHTML = "";
      previewBtn.hidden = true; hideTranscript();
      checkAISidecar(status);
      return;
    }
    status.textContent = st.ass ? t("srt + karaoke ass ready") : t("srt ready");
    const base = `/api/v1/projects/${currentProject.id}/subtitles/file?format=`;
    links.innerHTML = "";
    for (const [fmt, ok] of [["srt", st.srt], ["ass", st.ass]]) {
      if (!ok) continue;
      const a = document.createElement("a");
      a.href = base + fmt;
      a.download = "";
      a.textContent = tf("download .{fmt}", { fmt });
      links.appendChild(a);
    }
    previewBtn.hidden = !st.srt;
    if (!st.srt) hideTranscript();
  } catch (_) { status.textContent = t("status unavailable"); }
}

// checkAISidecar appends the configured-sidecar state to a status line:
// health reports only whether a sidecar binary resolves (LookPath, no
// process spawn) — the user-facing hint for "you need to configure AI".
async function checkAISidecar(statusEl) {
  try {
    const h = await api("/api/v1/health");
    if (h.ai_sidecar === "missing" && statusEl) {
      statusEl.textContent += " · " + t("no AI sidecar — set workers.ai_bin or install xcut-ai on PATH");
    }
  } catch (_) { /* health already handled elsewhere */ }
}

$("btn-subs-preview").addEventListener("click", async () => {
  const transcript = $("subs-transcript");
  if (!transcript.hidden) { transcript.hidden = true; return; }
  if (!currentProject) return;
  const pid = currentProject.id;
  try {
    const resp = await fetch(`/api/v1/projects/${pid}/subtitles/file?format=srt`);
    if (projectChangedSince(pid)) return; // stale: do not render A's transcript under B
    if (!resp.ok) throw new Error(`${resp.status}: ${resp.statusText}`);
    transcript.textContent = srtToText(await resp.text()); // untrusted: textContent only
    transcript.hidden = false;
  } catch (e) { banner(tf("Preview failed: {msg}", { msg: e.message })); }
});

function srtToText(srt) {
  return srt.replace(/\r/g, "")
    .split("\n\n")
    .map((b) => b.split("\n")
      .filter((l) => l.trim() && !/^\d+$/.test(l.trim()) && !l.includes("-->"))
      .join(" "))
    .filter(Boolean)
    .join("\n");
}

$("btn-subtitles").addEventListener("click", async () => {
  try {
    await trigger("/subtitles", { asset: $("subs-asset").value || undefined });
    banner(t("Transcription queued — status updates when the job finishes"));
  } catch (e) { banner(tf("Transcribe failed: {msg}", { msg: e.message })); busy(false); }
});

/* ---------- region editor (court ROI / scoreboard) ---------- */
/* Two normalized rects (0..1) stored per SOURCE:
 *   assets.motion_roi  — the court area, which part of the frame the motion
 *                        signal should look at (overrides the style's rect);
 *   assets.score_crop  — the score overlay, whose changes mark finished points.
 * The court rect is read by the timeline directly; the scoreboard rect is only
 * an intent — measuring it runs the AI sidecar, which belongs to the analyze
 * job, so saving here reports "region stored" and the boundaries show up after
 * the next analyze run. */
let roiRect = null;      // {x,y,w,h} drawn but not yet saved
let roiAssetId = null;   // asset whose frame is on display

function roiStyleName() { return $("style").value; }
function roiAsset() { return $("subs-asset").selectedOptions[0] || null; }
// "roi" (court) or "score" (scoreboard): the same picker, the same drag code,
// two endpoints that take the same rectangle shape.
function roiTarget() { return $("roi-target").value === "score" ? "score" : "roi"; }
function roiIsScore() { return roiTarget() === "score"; }
function roiUrl(assetValue) {
  return `/api/v1/projects/${currentProject.id}/assets/${assetValue}/${roiTarget()}`;
}

function fmtROI(label, roi) {
  return tf(label, { x: roi.x.toFixed(2), y: roi.y.toFixed(2), w: roi.w.toFixed(2), h: roi.h.toFixed(2) });
}

function fmtCrop(crop) {
  return crop.map((v) => v.toFixed(2)).join(", ");
}

async function refreshROIStatus() {
  const status = $("roi-status");
  const clearBtn = $("btn-roi-clear");
  const asset = roiAsset();
  const target = roiTarget();
  // The two branches are separate requests, so whichever resolves last wins
  // the label — which is wrong when the user switched target in between.
  const stale = () => target !== roiTarget();
  $("roi-hint-court").hidden = roiIsScore();
  $("roi-hint-score").hidden = !roiIsScore();
  $("roi-tag-motion").hidden = roiIsScore();
  $("roi-tag-score").hidden = !roiIsScore();
  $("btn-roi-label-court").hidden = roiIsScore();
  $("btn-roi-label-score").hidden = !roiIsScore();
  if (!currentProject || !asset) { status.textContent = ""; clearBtn.hidden = true; return; }
  const pid = currentProject.id;
  try {
    if (target === "score") {
      const own = await api(`/api/v1/projects/${pid}/assets/${asset.value}/score`);
      if (projectChangedSince(pid) || stale()) return;
      clearBtn.hidden = !own.crop;
      if (own.marks > 0 && own.stale) {
        status.textContent = tf("{n} boundaries from an older region ({crop}) — analyze again to re-measure",
          { n: own.marks, crop: fmtCrop(own.crop) });
      } else if (own.marks > 0) {
        status.textContent = tf("{n} point boundaries measured at {crop}",
          { n: own.marks, crop: fmtCrop(own.crop) });
      } else if (own.crop) {
        status.textContent = tf("region {crop} stored — run analyze to measure it", { crop: fmtCrop(own.crop) });
      } else {
        status.textContent = t("no scoreboard region");
      }
      return;
    }
    const [own, preset] = await Promise.all([
      api(`/api/v1/projects/${pid}/assets/${asset.value}/roi`),
      roiStyleName()
        ? api(`/api/v1/styles/${encodeURIComponent(roiStyleName())}/roi`)
        : Promise.resolve({ roi: null }),
    ]);
    if (projectChangedSince(pid) || stale()) return;
    if (own.roi) {
      status.textContent = fmtROI("per-source ROI x={x} y={y} w={w} h={h} (overrides the style)", own.roi);
      clearBtn.hidden = false;
    } else if (preset.roi) {
      status.textContent = fmtROI("style ROI x={x} y={y} w={w} h={h} (fallback for this asset)", preset.roi);
      clearBtn.hidden = true;
    } else {
      status.textContent = t("full frame (no ROI)");
      clearBtn.hidden = true;
    }
  } catch (_) { status.textContent = t("roi status unavailable"); }
}

function drawROIOverlay() {
  const canvas = $("roi-canvas");
  const video = $("roi-video");
  canvas.width = video.clientWidth || 320;
  canvas.height = video.clientHeight || 240;
  const ctx = canvas.getContext("2d");
  ctx.clearRect(0, 0, canvas.width, canvas.height);
  if (!roiRect) return;
  ctx.strokeStyle = "#3dd68c";
  ctx.lineWidth = 2;
  ctx.setLineDash([6, 4]);
  ctx.strokeRect(roiRect.x * canvas.width, roiRect.y * canvas.height,
    roiRect.w * canvas.width, roiRect.h * canvas.height);
}

async function openROIEditor() {
  if (!currentProject) return;
  const opt = roiAsset();
  if (!opt) { banner(t("Import an asset first — the ROI editor draws over its first frame")); return; }
  roiAssetId = opt.value;
  roiRect = null;
  $("btn-roi-save").disabled = true;
  $("roi-editor").hidden = false;
  const video = $("roi-video");
  video.src = `/api/v1/projects/${currentProject.id}/assets/${roiAssetId}/file#t=1`;
  video.currentTime = 1;
  video.pause();
  drawROIOverlay();
}

function closeROIEditor() {
  $("roi-editor").hidden = true;
  const video = $("roi-video");
  video.removeAttribute("src");
  video.load();
}

$("btn-roi").addEventListener("click", openROIEditor);
$("btn-roi-cancel").addEventListener("click", closeROIEditor);
$("subs-asset").addEventListener("change", refreshROIStatus);
$("roi-target").addEventListener("change", () => { closeROIEditor(); refreshROIStatus(); });
$("btn-roi-clear").addEventListener("click", async () => {
  const asset = roiAsset();
  if (!currentProject || !asset) return;
  try {
    await api(roiUrl(asset.value), { method: "DELETE" });
    banner(roiIsScore() ? t("Scoreboard region cleared") : t("Per-source ROI cleared — this asset falls back to the style's region"));
    refreshROIStatus();
  } catch (e) { banner(tf("Clear failed: {msg}", { msg: e.message })); }
});
$("btn-roi-save").addEventListener("click", async () => {
  if (!roiRect || !currentProject) return;
  const asset = roiAsset();
  if (!asset) return;
  try {
    await api(roiUrl(asset.value), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(roiRect),
    });
    banner(roiIsScore()
      ? t("Scoreboard region saved — the point boundaries are measured on the next analyze run")
      : t("Court ROI saved for this asset — it overrides the style's region"));
    closeROIEditor();
    refreshROIStatus();
  } catch (e) { banner(tf("Save failed: {msg}", { msg: e.message })); }
});
$("style").addEventListener("change", refreshROIStatus);

(() => {
  const video = $("roi-video");
  const canvas = $("roi-canvas");
  let dragStart = null;
  const norm = (e) => {
    const r = canvas.getBoundingClientRect();
    return {
      x: Math.min(Math.max((e.clientX - r.left) / r.width, 0), 1),
      y: Math.min(Math.max((e.clientY - r.top) / r.height, 0), 1),
    };
  };
  canvas.addEventListener("pointerdown", (e) => {
    dragStart = norm(e);
    canvas.setPointerCapture(e.pointerId);
  });
  canvas.addEventListener("pointermove", (e) => {
    if (!dragStart) return;
    const p = norm(e);
    roiRect = {
      x: Math.min(dragStart.x, p.x),
      y: Math.min(dragStart.y, p.y),
      w: Math.abs(p.x - dragStart.x),
      h: Math.abs(p.y - dragStart.y),
    };
    drawROIOverlay();
    $("btn-roi-save").disabled = roiRect.w < 0.02 || roiRect.h < 0.02;
  });
  canvas.addEventListener("pointerup", () => { dragStart = null; });
  video.addEventListener("loadedmetadata", drawROIOverlay);
  window.addEventListener("resize", () => { if (!$("roi-editor").hidden) drawROIOverlay(); });
})();

/* ---------- pipeline steps ---------- */
$("btn-analyze").addEventListener("click", async () => {
  try { await trigger("/analyze", {}); }
  catch (e) { banner(tf("Analyze failed: {msg}", { msg: e.message })); }
});

$("btn-timeline").addEventListener("click", async (e) => {
  const btn = e.currentTarget;
  // Two-step confirm: regenerating overwrites manual edits (the backup
  // keeps one level of undo). The confirm disarms itself after 4s.
  if (timelineDoc && btn.dataset.armed !== "1") {
    btn.dataset.armed = "1";
    btn.textContent = t("overwrite edits?");
    setTimeout(() => { btn.dataset.armed = ""; btn.textContent = t("2 · Timeline"); }, 4000);
    return;
  }
  btn.dataset.armed = "";
  btn.textContent = t("2 · Timeline");
  try { await trigger("/timeline", timelineRequest()); }
  catch (err) { banner(tf("Timeline failed: {msg}", { msg: err.message })); }
});

// timelineRequest carries the style plus an optional reel length. An empty or
// out-of-range field means "ask the style", which is what the placeholder says.
// The input's min/max mirror the server's accepted range, and the server still
// owns the verdict: values the browser's validity UI would flag are dropped
// here rather than round-tripped as a 400.
function timelineRequest() {
  const req = { style: $("style").value };
  const raw = $("tl-duration").value.trim();
  const secs = Number(raw);
  if (raw !== "" && Number.isFinite(secs) && secs >= 1) req.duration = secs;
  return req;
}

$("btn-render").addEventListener("click", async () => {
  try { await trigger("/render", { subs: $("burn-subs").checked }); showPlayerSoon(); }
  catch (e) { banner(tf("Render failed: {msg}", { msg: e.message })); }
});

let playerTimer = null;
function showPlayerSoon() {
  clearTimeout(playerTimer);
  playerTimer = setTimeout(showPlayer, 1500);
}

async function loadStyles() {
  try {
    const { styles } = await api("/api/v1/styles");
    const sel = $("style");
    sel.innerHTML = "";
    for (const s of styles) {
      const o = document.createElement("option");
      o.value = s; o.textContent = s;
      sel.appendChild(o);
    }
    refreshROIStatus();
  } catch (_) { /* non-fatal */ }
}

refreshHealth();
setInterval(refreshHealth, 15000);
$("lang").addEventListener("change", (e) => setLang(e.target.value));
$("sign-out").addEventListener("click", signOut);
// A session restored from sessionStorage means the sign-out affordance has to
// appear on this page load, not only after the next successful sign-in.
setSession(sessionId);
applyI18n();
refreshProjects();
loadStyles();
refreshJobs();
