// Copies xterm.js out of node_modules into the app's resources, which is
// all the page can load: Neutralino serves resources/, nothing else.
import { cpSync, mkdirSync } from "node:fs";

mkdirSync("resources/vendor", { recursive: true });
cpSync("node_modules/@xterm/xterm/lib/xterm.js", "resources/vendor/xterm.js");
cpSync("node_modules/@xterm/xterm/lib/xterm.js.map", "resources/vendor/xterm.js.map");
cpSync("node_modules/@xterm/xterm/css/xterm.css", "resources/vendor/xterm.css");
cpSync("node_modules/@xterm/addon-webgl/lib/addon-webgl.js", "resources/vendor/addon-webgl.js");
console.log("xterm.js copied to resources/vendor");
