import net from "node:net";

// pickFreePort asks the kernel for an unused TCP port on the loopback
// interface and immediately releases it before returning the chosen
// number. There is a small TOCTOU window between listen(0) returning
// and the caller binding it again, but for a single-user desktop
// subprocess spawning one extra service at a time the race is far less
// concerning than a hard-coded port conflict with server-manager
// (fixed 8090) or daemon-manager's hash-derived health port.
//
// The implementation opens a server, reads its bound port from
// address(), closes the server, and resolves with the port. If listen
// itself errors (e.g. permission denied) the returned promise rejects
// so the caller can fall back to a different strategy.
export function pickFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const addr = server.address();
      if (addr === null || typeof addr === "string") {
        server.close();
        reject(new Error("failed to read bound address from listening socket"));
        return;
      }
      const port = addr.port;
      server.close((err) => {
        if (err) reject(err);
        else resolve(port);
      });
    });
  });
}
