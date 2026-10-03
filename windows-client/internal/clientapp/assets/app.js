"use strict";
const el = id => document.getElementById(id);
const api = () => window.go.clientapp.App;
const labels = {waiting:"Ready to pair",pairing:"Pairing",connecting:"Connecting",acknowledging:"Confirming setup",ready:"Connected",expired:"Invitation expired",revoked:"Access revoked",error:"Needs attention",unavailable:"Service unavailable"};
let refreshing = false;
async function refresh() {
  if (refreshing) return;
  refreshing = true;
  try {
    const status = await api().Status();
    el("phase").textContent = labels[status.phase] || "Checking service";
    el("phase").className = "badge " + (["error","revoked","unavailable","expired"].includes(status.phase) ? "error" : status.phase === "ready" ? "ready" : "");
    el("message").textContent = status.message;
    el("httpAddress").textContent = status.httpAddress ? "HTTP / CONNECT  " + status.httpAddress : "";
    el("socksAddress").textContent = status.socksAddress ? "SOCKS5          " + status.socksAddress : "";
    const configured = status.generation > 0;
    el("copyHttp").disabled = el("copySocks").disabled = !configured || status.phase === "revoked";
    el("pairCard").classList.toggle("hidden", configured);
    el("version").textContent = status.version ? "Client " + status.version : "";
  } catch (_) { el("message").textContent = "Client app could not connect. Close and reopen it, or repair the Client installation."; }
  finally { refreshing = false; }
}
async function action(button, callback, success) {
  button.disabled = true;
  el("feedback").textContent = "";
  try { await callback(); el("feedback").textContent = success; }
  catch (error) { el("feedback").textContent = String(error); }
  finally { button.disabled = false; await refresh(); }
}
el("pair").onclick = () => action(el("pair"), async () => { await api().Pair(el("invitation").value.trim()); el("invitation").value = ""; }, "Pairing saved. Keep your controller open.");
el("import").onclick = () => action(el("import"), async () => { await api().Import(el("update").value.trim()); el("update").value = ""; }, "Connection update saved.");
el("copyHttp").onclick = () => action(el("copyHttp"), () => api().CopyProxy("http"), "Proxy line copied.");
el("copySocks").onclick = () => action(el("copySocks"), () => api().CopyProxy("socks"), "SOCKS URL copied.");
refresh(); setInterval(refresh, 2500);
