# tend desktop (issue #40, phase 0)

tend as a desktop app: a [Neutralino](https://neutralino.js.org/) window on a
tend session. A proof of concept and a place to measure, not a product yet.

The window starts `tend gui-bridge`, which speaks the session's own protocol
as JSON lines on its stdin and stdout (`cmd/tend/guibridge.go`); nothing new
on the server, nothing listening on a network. Each pane is an
[xterm.js](https://xtermjs.org/) at the pane's own size, drawn on the GPU
(WebGL) where the webview can.

## Run it

```bash
cd desktop
npm install
npm run setup     # Neutralino's binaries and client, and xterm.js into resources/
npm start         # a window on your default session
```

- `TEND_SESSION=name npm start` — another session.
- `TEND_BIN=/path/to/tend npm start` — a tend other than the one on the PATH
  (the build being tried: it needs `gui-bridge`).
- `TEND_RENDERER=dom npm start` — xterm.js's DOM renderer, to compare.

The status line says how many screens a second arrive and how many bytes.

## What it does now

Spaces and tabs down the left; a tab's panes on the right, the one clicked
has the keyboard. Not yet: the panes' arrangement (they are laid out in a
row, at their own sizes), resizing, the panels (agents, MCP, issues…), the
menus.

## Measured (Linux, WebKitGTK, 2026-10-02)

Two panes updating — a clock five times a second and `top` — 6 screens a
second, 29 KB/s from the bridge:

| renderer | webview CPU |
|---|---|
| DOM (xterm.js default) | 25% |
| WebGL (`@xterm/addon-webgl`) | 5% |

Still to measure: more panes (8+), WebView2 on Windows (through
`wsl tend gui-bridge`), WKWebView on macOS, and which shortcuts the webview
takes for itself.

## Licences

Neutralino (MIT) and xterm.js (MIT) are fetched by `npm run setup` and are
not kept in this repository. `resources/icons` holds Neutralino's sample
icons until tend has its own (#23).
