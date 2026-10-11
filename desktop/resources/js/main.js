// tend's desktop app, phase 0 of issue #40: a proof of concept and a place
// to measure. The spaces and tabs of a session down the left; the panes of
// the tab chosen, each a xterm.js at the pane's own size; the keyboard goes
// to the pane clicked last. Each pane's screen arrives whole from the server
// and is drawn over the last, so a slow window never shows a wrong one.

const bridge = new Bridge();
const terms = new Map(); // pane id → { term, el }
let snapshot = null;
let shownTab = 0;
let focused = 0;
let renderer = "webgl";

// What phase 0 is for: how much the webview draws, and how fast.
const stats = { screens: 0, bytes: 0, since: performance.now() };

function setStatus(text) {
  document.getElementById("status").textContent = text;
}

function tabsOf(ws) {
  return ws.tabs || [];
}

// renderSidebar lists the spaces with their tabs; a tab is what a click
// shows.
function renderSidebar() {
  const ul = document.getElementById("spaces");
  ul.innerHTML = "";
  for (const ws of snapshot.workspaces || []) {
    const li = document.createElement("li");
    li.textContent = ws.name || "space " + ws.id;
    ul.appendChild(li);
    for (const tab of tabsOf(ws)) {
      const t = document.createElement("li");
      t.className = "tab" + (tab.id === shownTab ? " active" : "");
      t.textContent = tab.name || "tab " + tab.id;
      t.onclick = () => showTab(tab.id);
      ul.appendChild(t);
    }
  }
}

function findTab(id) {
  for (const ws of snapshot.workspaces || []) {
    for (const tab of tabsOf(ws)) {
      if (tab.id === id) return tab;
    }
  }
  return null;
}

function paneInfo(id) {
  return (snapshot.panes || []).find((p) => p.id === id) || {};
}

// showTab puts a tab's panes up: a terminal each, at the pane's size, and
// asks the server for their screens from now on.
async function showTab(id) {
  const tab = findTab(id);
  if (!tab) return;
  shownTab = id;
  renderSidebar();
  const holder = document.getElementById("panes");
  for (const { term } of terms.values()) term.dispose();
  terms.clear();
  holder.innerHTML = "";

  for (const pane of tab.panes) {
    const screen = await bridge.call("pane.screen", { pane });
    const el = document.createElement("div");
    el.className = "pane";
    const title = document.createElement("div");
    title.className = "title";
    const info = paneInfo(pane);
    title.textContent = [pane, info.agent || info.title || "", info.state || ""].filter(Boolean).join(" · ");
    el.appendChild(title);
    const body = document.createElement("div");
    el.appendChild(body);
    holder.appendChild(el);

    const term = new Terminal({
      cols: screen.cols,
      rows: screen.rows,
      scrollback: 0,
      fontFamily: '"JetBrainsMono Nerd Font", "Symbols Nerd Font", monospace',
      fontSize: 13,
      cursorBlink: false,
    });
    term.open(body);
    // Drawn on the GPU when the webview can: the DOM renderer redraws the
    // whole screen on every update, and every update here is a whole
    // screen. TEND_RENDERER=dom keeps the DOM one, to compare.
    if (renderer !== "dom" && window.WebglAddon) {
      try {
        const gl = new WebglAddon.WebglAddon();
        gl.onContextLoss(() => gl.dispose());
        term.loadAddon(gl);
      } catch (e) {
        console.warn("no WebGL here; drawing with the DOM", e);
      }
    }
    term.write(screen.ansi);
    term.onData((data) => bridge.input(pane, data));
    el.addEventListener("mousedown", () => focusPane(pane));
    terms.set(pane, { term, el });
  }
  await bridge.call("pane.subscribe", { panes: tab.panes });
  focusPane(tab.active_pane || tab.panes[0]);
}

function focusPane(pane) {
  focused = pane;
  for (const [id, { term, el }] of terms) {
    el.classList.toggle("focused", id === pane);
    if (id === pane) term.focus();
  }
}

bridge.onOutput = (pane, data) => {
  const t = terms.get(pane);
  if (!t) return;
  stats.screens++;
  stats.bytes += data.length;
  t.term.write(data);
};

// Anything the session reports may have changed its shape; the list is
// read again, and the panes put up again only when the tab's are not the
// ones shown.
let reloading = null;
bridge.onEvent = () => {
  clearTimeout(reloading);
  reloading = setTimeout(reload, 150);
};

async function reload() {
  snapshot = await bridge.call("session.snapshot", null);
  renderSidebar();
  const tab = findTab(shownTab);
  const shown = [...terms.keys()].join(",");
  if (!tab || tab.panes.join(",") !== shown) {
    // The tab the session has in view, as the TUI opens on it.
    const ws = (snapshot.workspaces || []).find((w) => w.id === snapshot.active_workspace) || (snapshot.workspaces || [])[0] || {};
    const first = tab || tabsOf(ws).find((t) => t.id === ws.active_tab) || tabsOf(ws)[0];
    if (first) await showTab(first.id);
  }
}

bridge.onReady = async (build) => {
  setStatus("connected · tend " + build);
  await reload();
  setInterval(() => {
    const secs = (performance.now() - stats.since) / 1000;
    setStatus(
      `tend · ${terms.size} panes · ${(stats.screens / secs).toFixed(1)} screens/s · ` +
        `${(stats.bytes / secs / 1024).toFixed(0)} KB/s`
    );
    stats.screens = stats.bytes = 0;
    stats.since = performance.now();
  }, 2000);
};

bridge.onClosed = (why) => setStatus("disconnected: " + why);

Neutralino.init();
Neutralino.events.on("windowClose", () => Neutralino.app.exit());
Neutralino.events.on("ready", async () => {
  // TEND_BIN picks the tend to run (a build being tried), and TEND_SESSION
  // the session; otherwise tend on the PATH and its default session.
  const bin = (await Neutralino.os.getEnv("TEND_BIN")) || "tend";
  const session = (await Neutralino.os.getEnv("TEND_SESSION")) || "default";
  renderer = (await Neutralino.os.getEnv("TEND_RENDERER")) || "webgl";
  await bridge.start(`"${bin}" gui-bridge -s "${session}"`);
});
