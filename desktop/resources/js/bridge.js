// The connection to a tend session: `tend gui-bridge`, started by the app
// and spoken to as JSON lines on its stdin and stdout (cmd/tend/guibridge.go).
// Neutralino hands a process's stdout to the page in chunks that need not
// end at a line, so lines are put back together here.

class Bridge {
  constructor() {
    this.proc = null;
    this.buffer = "";
    this.nextId = 1;
    this.pending = new Map();
    this.onOutput = () => {};
    this.onEvent = () => {};
    this.onReady = () => {};
    this.onClosed = () => {};
  }

  async start(command) {
    Neutralino.events.on("spawnedProcess", (e) => {
      if (!this.proc || e.detail.id !== this.proc.id) return;
      switch (e.detail.action) {
        case "stdOut":
          this.receive(e.detail.data);
          break;
        case "stdErr":
          console.warn("gui-bridge:", e.detail.data);
          break;
        case "exit":
          this.onClosed("the bridge exited (" + e.detail.data + ")");
          break;
      }
    });
    this.proc = await Neutralino.os.spawnProcess(command);
  }

  receive(chunk) {
    this.buffer += chunk;
    let at;
    while ((at = this.buffer.indexOf("\n")) >= 0) {
      const line = this.buffer.slice(0, at);
      this.buffer = this.buffer.slice(at + 1);
      if (line.trim() !== "") this.handle(JSON.parse(line));
    }
  }

  handle(m) {
    switch (m.t) {
      case "ready":
        this.onReady(m.build);
        break;
      case "out":
        this.onOutput(m.pane, m.data);
        break;
      case "event":
        this.onEvent(m.event);
        break;
      case "closed":
        this.onClosed("the session ended");
        break;
      case "res": {
        const p = this.pending.get(m.id);
        if (!p) {
          if (m.error) console.warn(m.error);
          return;
        }
        this.pending.delete(m.id);
        m.error ? p.reject(new Error(m.error)) : p.resolve(m.result);
        break;
      }
    }
  }

  send(message) {
    return Neutralino.os.updateSpawnedProcess(this.proc.id, "stdIn", JSON.stringify(message) + "\n");
  }

  call(method, params) {
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.send({ t: "call", id, method, params });
    });
  }

  input(pane, data) {
    return this.send({ t: "input", pane, data });
  }
}
