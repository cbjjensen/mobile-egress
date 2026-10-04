"use strict";
const el = id => document.getElementById(id);
const api = () => window.go.clientapp.App;
const show = (id, visible) => el(id).classList.toggle("hidden", !visible);
const steps = ["address", "network", "pair", "verify", "proxy"];
const titles = ["Computer address", "Network access", "Pair phone", "Start on your phone", "Use your proxy"];
const invitationReadyMessage = "Invitation ready. Scan it with Mobile Egress on your phone.";
const connectionUpdateMessage = "Scan this connection update with Mobile Egress on your phone.";
const labels = {waiting:"Setup needed", migration_required:"Fresh pairing required", listening:"Listening", awaiting_phone:"Waiting for phone", pairing:"Pairing", acknowledging:"Confirming pairing", ready:"Waiting for phone", connected:"Waiting for phone", expired:"Invitation expired", revoked:"Phone removed", error:"Needs attention", unavailable:"Service unavailable"};
let status = null, setup = {localAddresses:[], defaultBindAddress:":8443", defaultPublicPort:8443};
let step = "address", dashboard = false, initialized = false, metadataReady = false;
let renderedScreen = "";
let refreshing = null, busy = false, mutation = 0, editing = false, addressEdited = false;
let discoveryStarted = false, discovering = false, suggestedAddress = "";
let invitationConfiguration = "", invitationExpiry = "", firewall = null, firewallBusy = false;
let waitingSince = Date.now(), readinessExpired = false;
let selectedTransport = "";
let recoveryWasPending = false;
const hostedSelected = () => (selectedTransport || status?.transport || (status?.endpoint ? "direct" : "hosted")) === "hosted";
const hostedAuthorized = (value = status) => value?.transport === "hosted" && !!value.endpoint && value.activationState === "authorized";
const available = () => status && status.phase !== "unavailable";
const phoneConnected = () => !!(available() && status.connected && !status.updatePending);
const pendingPhoneUpdate = () => !!(available() && status.paired && status.updatePending);
const configKey = value => [value.transport || (value.endpoint ? "direct" : "hosted"), value.endpoint, value.bindAddress, value.displayName, value.generation].join("|");
const activeInvitation = () => !!(status && status.invitationExpiresAt && new Date(status.invitationExpiresAt).getTime() > Date.now());

function clearInvitation() {
  if (el("feedback").textContent === invitationReadyMessage) el("feedback").textContent = "";
  el("invitation").value = ""; el("invitationQR").removeAttribute("src");
  invitationConfiguration = ""; invitationExpiry = "";
  show("invitationDetails", false);
}
function clearUpdate() {
  if (el("feedback").textContent === connectionUpdateMessage) el("feedback").textContent = "";
  el("update").value = ""; el("updateQR").removeAttribute("src"); show("updateDetails", false);
}
function endpointParts(endpoint) {
  try { const url = new URL(endpoint); return {host:url.hostname.replace(/^\[|\]$/g, ""), port:url.port || "443"}; }
  catch (_) { return {host:"", port:String(setup.defaultPublicPort)}; }
}
function enteredEndpoint() {
  const input = el("endpoint").value.trim(), port = el("publicPort").value.trim();
  if (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535) throw new Error("Enter a public port from 1 to 65535.");
  if (!input || /[\s/?#@]/.test(input)) throw new Error("Enter a hostname or IP address, without https:// or a path.");
  const host = input.replace(/^\[|\]$/g, "");
  const authority = host.includes(":") ? "[" + host + "]" : host;
  try { new URL("https://" + authority + ":" + port); } catch (_) { throw new Error("Enter a valid hostname or IP address."); }
  return "https://" + authority + ":" + Number(port);
}
function fillInputs() {
  if (editing || !status) return;
  if (status.endpoint) {
    const parts = endpointParts(status.endpoint);
    el("endpoint").value = parts.host; el("publicPort").value = parts.port;
  } else {
    el("endpoint").value = suggestedAddress;
    el("publicPort").value = String(setup.defaultPublicPort);
  }
  el("bindAddress").value = status.bindAddress || setup.defaultBindAddress;
  el("displayName").value = status.displayName || setup.suggestedName || "";
  if (!el("hostedName").value) el("hostedName").value = status.displayName || setup.suggestedName || "";
}
function resumeStep() {
  if (!status.endpoint) return "address";
  if (status.phase === "acknowledging") return "verify";
  if (activeInvitation()) return "pair";
  return "network";
}
function selectStep(next) {
  if (busy || !available()) return;
  if (next !== "address" && !status.endpoint) return;
  if (next === "proxy" && !phoneConnected()) return;
  step = next; dashboard = false; render();
  if (next === "network" && !firewall && !hostedSelected()) void checkFirewall(false);
}
function renderNetwork() {
  const parts = endpointParts(status?.endpoint || "");
  const localPort = (status?.bindAddress || setup.defaultBindAddress).split(":").pop();
  const publicPort = parts.port, addresses = (setup.localAddresses || []).join(", ") || "your computer's LAN address";
  const location = el("networkLocation").value || "home";
  let text;
  if (location === "aws") {
    text = "In the EC2 security group, allow inbound Custom TCP on the Client listener port " + localPort + ". Allow it in the host firewall too. The instance needs a public address and a subnet route through an internet gateway. The rule's source must include your phone's cellular address: AWS My IP may select this computer instead. Cellular addresses can change; a broader source range may be needed for this authenticated Client listener only. ";
    if (publicPort !== localPort) text += "Your advertised public port is " + publicPort + "; make sure your network explicitly forwards it to listener port " + localPort + ". ";
  } else if (location === "hosted") {
    text = "In your hosting provider's firewall, allow inbound TCP on listener port " + localPort + ". Allow the same port in the host firewall and confirm this server has a publicly reachable address and an internet route. Your phone connects to public port " + publicPort + "; if it differs, configure forwarding to listener port " + localPort + ". ";
  } else {
    text = "In your router, forward public TCP port " + publicPort + " to this computer's listener port " + localPort + ". LAN addresses on this computer: " + addresses + ". Select the address on the router's network and keep it stable. For a directly reachable IPv6 address, allow inbound TCP in the router firewall. If your ISP uses CGNAT, forwarding on your router alone will not make this computer reachable; ask the ISP for inbound access or use a reachable hosted Client. ";
  }
  el("networkInstructions").textContent = text + "Mobile Egress does not change routers or cloud firewalls. Never expose proxy ports 1080 or 1081.";
  el("networkEndpoint").textContent = status?.endpoint || "Save your computer address first.";
  const firewallLabels = {allowed:"Local access allowed", disabled:"Local firewall disabled", blocked:"Local access blocked", unavailable:"Firewall check unavailable", unknown:"Local access not confirmed"};
  el("firewallState").textContent = firewallBusy ? "Checking local firewall…" : firewallLabels[firewall?.state] || "Check local access";
  el("firewallMessage").textContent = firewall ? (firewall.message || "") + (firewall.scope === "application" ? " This check applies to the Client service application." : " This check applies to local TCP port " + firewall.port + ".") + " Local firewall results do not prove cellular reachability." : "Check this computer's firewall, then review the network instructions below.";
  el("checkFirewall").disabled = el("retryFirewall").disabled = busy || firewallBusy || !available() || !status.endpoint;
}
function render() {
  const ready = available(), connected = phoneConnected(), pendingUpdate = pendingPhoneUpdate();
  const canUpdatePhone = pendingUpdate && (status.transport !== "hosted" || status.activationState === "authorized");
  show("servicePanel", !ready); show("serviceRetry", !ready && readinessExpired);
  el("serviceMessage").textContent = readinessExpired ? "The Client service has not become available. Retry, or run the latest signed installer to repair the installation. Your saved pairing and proxy credentials are preserved." : "Waiting for the installed Client service. Setup will wait up to 30 seconds.";
  show("wizardHeader", initialized && !dashboard); show("dashboardHeader", initialized && dashboard);
  show("unfinishedSetup", initialized && dashboard && !status?.paired);
  el("stepTitle").textContent = hostedSelected() && step === "address" ? "Activate Inevitable" : hostedSelected() && step === "network" ? "Gateway connection" : titles[steps.indexOf(step)];
  if (step === "verify" && pendingUpdate) el("stepTitle").textContent = "Reconnect your phone";
  el("verifyStepLabel").textContent = pendingUpdate ? "Reconnect your phone" : "Start on your phone";
  el("addressStepLabel").textContent = hostedSelected() ? "Activate Inevitable" : "Computer address";
  el("networkStepLabel").textContent = hostedSelected() ? "Gateway connection" : "Network access";
  el("stepCount").textContent = "Step " + (steps.indexOf(step) + 1) + " of 5";
  for (const [index, name] of steps.entries()) {
    const button = el("step" + name[0].toUpperCase() + name.slice(1));
    button.setAttribute("aria-current", !dashboard && step === name ? "step" : "false");
    button.disabled = busy || !ready || (index > 0 && !status.endpoint) || (name === "proxy" && !connected);
  }
  for (const name of ["address", "network", "pair", "verify", "proxy"]) {
    show(name + "Panel", initialized && (dashboard ? ["address","pair","proxy"].includes(name) : step === name));
  }
  show("addressPanel", initialized && !hostedSelected() && (dashboard || step === "address"));
  show("networkPanel", initialized && !dashboard && !hostedSelected() && step === "network");
  show("hostedPanel", initialized && hostedSelected() && (dashboard || step === "address"));
  show("transportChoice", initialized && (dashboard || step === "address"));
  show("gatewayPanel", initialized && hostedSelected() && !dashboard && step === "network");
  show("directTroubleshooting", !hostedSelected());show("hostedTroubleshooting", hostedSelected());
  el("activationMessage").textContent = status?.activationState === "pending" ? "Waiting for browser approval. Your activation request is saved." : status?.activationState === "authorized" ? "Inevitable activation approved." : status?.activationState === "denied" ? "Activation denied. You can try again." : status?.activationState === "expired" ? "Activation expired. Start a new request." : "Activate this Client to use hosted connectivity.";
  if (status?.activationState === "access_rejected") el("activationMessage").textContent = "Gateway access was rejected. Check your Mobile Egress account access or reactivate this Client. Your local phone pairing is preserved.";
  const activationPending = status?.activationState === "pending";
  show("resumeActivation", activationPending);show("cancelActivation", activationPending);
  el("activateHosted").disabled = busy || !ready || activationPending;
  el("activateHosted").textContent = hostedAuthorized() ? "Continue to gateway connection" : status?.activationState === "access_rejected" ? "Reactivate in browser" : "Activate in browser";
  el("resumeActivation").disabled = el("cancelActivation").disabled = busy || !ready;
  el("advancedDirect").disabled = el("chooseHosted").disabled = busy || !ready;
  el("gatewayNext").disabled = busy || !ready || status?.transport !== "hosted" || !status?.endpoint;
  el("gatewayMessage").textContent = "Gateway " + (status?.gatewayState || "disconnected") + ". " + (status?.gatewayState === "connected" ? "Waiting for the authenticated phone session." : "The Client reconnects to its saved gateway. Check this computer's Internet connection.");
  if (status?.gatewayState === "authorization_rejected") el("gatewayMessage").textContent = "Gateway access was rejected. Check your Mobile Egress account access or reactivate this Client. Your local phone pairing is preserved.";
  show("recoveryPanel", initialized && (dashboard || canUpdatePhone)); show("removePhone", initialized && dashboard);
  if (canUpdatePhone && !recoveryWasPending) el("recoveryPanel").open = true;
  if (!canUpdatePhone && recoveryWasPending) el("recoveryPanel").open = false;
  recoveryWasPending = canUpdatePhone;
  el("recoveryHeading").textContent = canUpdatePhone ? "Reconnect your phone" : "Connection updates";
  el("recoveryMessage").textContent = canUpdatePhone ? "This computer's connection changed. Show the connection update below, then scan it in Mobile Egress on your phone. Your existing pairing is kept." : pendingUpdate ? "Complete Inevitable activation in your browser first. Once approved, show the connection update and scan it on your phone." : "If your phone cannot reach this computer after a connection change, show an update and scan it in Mobile Egress on the paired phone.";
  show("connectionSummary", initialized);
  el("phase").textContent = connected ? "Connected" : labels[status?.phase] || "Checking service";
  el("phase").className = "badge " + (!ready || status?.phase === "error" ? "error" : connected ? "ready" : "");
  el("message").textContent = status?.message || "Configure a reachable address to pair your phone.";
  el("installedState").textContent = ready ? "Installed" : "Checking";
  el("listeningState").textContent = ready && status.running && status.endpoint && !["waiting","migration_required","error"].includes(status.phase) ? "Listening" : "Not confirmed";
  if (status?.transport === "hosted") el("listeningState").textContent = "Gateway " + (status.gatewayState || "disconnected");
  el("pairedState").textContent = status?.paired ? "Paired" : status?.phase === "acknowledging" ? "Confirming" : "Not paired";
  el("connectedState").textContent = connected ? "Connected" : "Waiting";
  el("httpAddress").textContent = status?.httpAddress ? "HTTP / CONNECT  " + status.httpAddress : "";
  el("socksAddress").textContent = status?.socksAddress ? "SOCKS5          " + status.socksAddress : "";
  el("copyHttp").disabled = busy || !ready || !status.httpAddress;
  el("copySocks").disabled = busy || !ready || !status.socksAddress;
  el("configure").disabled = busy || !ready;
  el("configure").textContent = dashboard ? "Save endpoint" : "Save and continue";
  show("finishSetup", !dashboard); show("pairNext", !dashboard);
  el("finishLater").disabled = el("finishSetup").disabled = el("reviewSetup").disabled = busy;
  el("networkNext").disabled = busy || !ready || !status.endpoint;
  el("verifyNext").disabled = busy || !connected;
  show("verifyNext", connected);
  el("pairNext").disabled = busy || !ready;
  el("issue").disabled = busy || !ready || !status.running || !status.endpoint || status.paired || status.phase === "acknowledging" || !!el("invitation").value;
  el("issue").textContent = activeInvitation() ? "Show current invitation" : "Create invitation";
  el("revoke").disabled = busy || !ready || !status.paired;
  el("exportUpdate").disabled = el("copyUpdate").disabled = busy || !ready || !status.paired || (status.transport === "hosted" && !hostedAuthorized());
  el("copyInvitation").disabled = busy || !ready || !el("invitation").value;
  el("cancelInvitation").disabled = busy || !ready || (!activeInvitation() && status.phase !== "acknowledging");
  show("cancelPairing", status?.phase === "acknowledging");
  el("cancelPairing").disabled = busy || !ready;
  el("pending").textContent = pendingUpdate ? (canUpdatePhone ? "Phone connection update needed. Use Reconnect your phone." : "Complete Inevitable activation in your browser, then update the phone's connection.") : "";
  el("pairNote").textContent = status?.paired ? "A phone is already paired. Continue to verify its live connection. Use the dashboard to remove it before pairing a replacement." : status?.phase === "acknowledging" ? "The phone is confirming pairing. Leave it open on cellular and let it retry; a new invitation is not needed." : "Create or show this computer's invitation, then scan it in the Mobile Egress phone app using cellular data.";
  const expires = activeInvitation() ? new Date(status.invitationExpiresAt) : null;
  el("expiry").textContent = expires ? "Invitation expires " + expires.toLocaleTimeString() + "." : "";
  const confirmingPairing = status?.phase === "acknowledging";
  show("phoneStartInstructions", ready && status.paired && !confirmingPairing && !connected && !pendingUpdate);
  el("verifyHeading").textContent = pendingUpdate ? "Waiting for your phone's connection update" : connected ? "Your phone is connected" : confirmingPairing ? "Finishing pairing on your phone" : "Start sharing on your phone";
  el("verificationMessage").textContent = !ready ? "Waiting for the Client service." : pendingUpdate ? (canUpdatePhone ? "Scan the connection update above on your phone. This screen will continue automatically after your phone confirms the update and connects." : "Reactivate this computer in Inevitable first, then update the saved connection on your phone.") : connected ? "Your phone is connected. You can now use your proxy." : confirmingPairing ? "Keep Mobile Egress open on your phone while it confirms pairing. You do not need to scan again." : status?.paired ? "Your phone is paired. Follow these steps on your phone; this screen will continue automatically when it connects." : "First, return to Pair phone and scan the QR code with Mobile Egress on your phone.";
  if ((status?.paired || confirmingPairing) && el("feedback").textContent === invitationReadyMessage) el("feedback").textContent = "";
  el("proxyConnection").textContent = connected ? "Your phone is connected. Copy a proxy into an application on this computer." : "The phone is not connected. Saved proxy details are available, but traffic needs a live phone connection.";
  el("version").textContent = status?.version ? "Client " + status.version : "";
  renderNetwork();
  const screen = initialized ? (dashboard ? "dashboard" : step) : "";
  if (screen && screen !== renderedScreen) {
    renderedScreen = screen;
    el(dashboard ? "dashboardHeading" : "stepTitle").focus();
  }
}
function startReadinessWait() {
  waitingSince = Date.now(); readinessExpired = false;
  setTimeout(() => { if (!available() && Date.now() - waitingSince >= 30000) { readinessExpired = true; render(); } }, 30000);
}
function refresh() {
  if (refreshing) return refreshing;
  const revision = mutation;
  refreshing = (async () => {
    let next;
    try { next = await api().Status(); }
    catch (_) { next = {phase:"unavailable", message:"The app could not connect to the Client service.", connected:false}; }
    // A response can resolve between the action callback and its finally block.
    // Commit only after the action has finished and a fresh snapshot is fetched.
    if (busy || revision !== mutation) return;
    const wasAvailable = available(), previous = status;
    if (next.phase === "unavailable") {
      if (wasAvailable) startReadinessWait();
      status = {...status, ...next, connected:false, running:false};
      if (Date.now() - waitingSince >= 30000) readinessExpired = true;
      clearInvitation(); clearUpdate();
    } else {
      status = next; readinessExpired = false;
      if (previous?.endpoint && configKey(previous) !== configKey(next)) { firewall = null; clearUpdate(); if (previous.transport !== next.transport) selectedTransport = next.transport; }
      if (!status.paired || (previous?.updatePending && !status.updatePending) || (status.transport === "hosted" && !hostedAuthorized())) clearUpdate();
      if (el("invitation").value && (status.paired || !activeInvitation() || invitationConfiguration !== configKey(status) || (invitationExpiry && invitationExpiry !== status.invitationExpiresAt))) clearInvitation();
      if (el("invitation").value) invitationExpiry = status.invitationExpiresAt;
      if (!initialized) { initialized = true; dashboard = !!status.paired; step = resumeStep(); }
      else if (!dashboard) {
        if (!status.endpoint) step = "address";
        // Browser approval can finish before the UI observes a pending snapshot.
        // Advance on usable approval, while leaving an explicit setup review put.
        else if (step === "address" && hostedSelected() && hostedAuthorized() && !hostedAuthorized(previous)) step = "network";
        else if (["pair","verify"].includes(step) && phoneConnected()) step = "proxy";
        else if (step === "proxy" && pendingPhoneUpdate()) step = "verify";
        else if (step === "pair" && (status.paired || status.phase === "acknowledging")) step = "verify";
      }
      fillInputs();
    }
    render();
    maybeDiscover();
  })().finally(() => { refreshing = null; });
  return refreshing;
}
function maybeDiscover() {
  if (metadataReady && initialized && !dashboard && !hostedSelected() && step === "address" && !status.endpoint && !discoveryStarted) void discover();
}
async function discover() {
  if (hostedSelected()) return;
  if (discovering) return;
  discovering = true; discoveryStarted = true; el("retryDiscovery").disabled = true;
  el("discoveryMessage").textContent = "Looking up a suggested public address…";
  try {
    const result = await api().DiscoverPublicAddress();
    const currentAddress = el("endpoint").value;
    if (!addressEdited && !status?.endpoint && (!currentAddress || currentAddress === suggestedAddress)) {
      suggestedAddress = result.address;
      el("endpoint").value = result.address;
    }
    el("discoveryMessage").textContent = "Suggested address—not yet verified: " + result.address + ". Check that it accepts incoming cellular connections.";
  } catch (error) { el("discoveryMessage").textContent = String(error) + " Enter your hostname or public IP manually, or retry the lookup."; }
  finally { discovering = false; el("retryDiscovery").disabled = false; }
}
async function checkFirewall(retry) {
  if (hostedSelected()) return;
  if (firewallBusy || !available() || !status.endpoint) return;
  firewallBusy = true; renderNetwork();
  const key = configKey(status);
  try { const result = await api()[retry ? "RetryFirewall" : "CheckFirewall"](); if (configKey(status) === key) firewall = result; }
  catch (error) { if (configKey(status) === key) firewall = {state:"unavailable", scope:setup.platform === "darwin" ? "application" : "port", port:(status.bindAddress || "").split(":").pop(), message:String(error)}; }
  finally { firewallBusy = false; renderNetwork(); }
}
async function action(button, callback, success) {
  if (busy) return;
  busy = true; mutation++; button.disabled = true; el("feedback").textContent = ""; render();
  try { await callback(); el("feedback").textContent = success; }
  catch (error) { el("feedback").textContent = String(error); }
  finally {
    // Polls started during the action may still describe its previous state.
    mutation++;
    busy = false;
    if (refreshing) await refreshing;
    await refresh();
  }
}
for (const id of ["bindAddress","endpoint","displayName","publicPort"]) el(id).oninput = () => { editing = true; if (id === "endpoint") addressEdited = true; };
for (const name of steps) el("step" + name[0].toUpperCase() + name.slice(1)).onclick = () => selectStep(name);
el("reviewSetup").onclick = () => selectStep("address");
el("advancedDirect").onclick = () => { if (!busy && available()) { selectedTransport = "direct"; step = "address"; dashboard = false; render(); maybeDiscover(); } };
el("chooseHosted").onclick = () => { if (!busy && available()) { selectedTransport = "hosted"; step = "address"; dashboard = false; render(); } };
el("activateHosted").onclick = () => hostedAuthorized() ? selectStep("network") : action(el("activateHosted"), () => api().StartHostedActivation(el("hostedName").value.trim()), "Approve this Client in your browser. Your phone pairs locally afterward.");
el("resumeActivation").onclick = () => action(el("resumeActivation"), () => api().ResumeHostedActivation(), "Continue activation in your browser.");
el("cancelActivation").onclick = () => action(el("cancelActivation"), () => api().CancelHostedActivation(), "Activation canceled.");
el("gatewayNext").onclick = () => selectStep(status?.paired ? "verify" : "pair");
el("finishLater").onclick = el("finishSetup").onclick = () => { if (!busy) { dashboard = true; render(); } };
el("networkLocation").onchange = renderNetwork;
el("retryDiscovery").onclick = discover;
el("checkFirewall").onclick = () => checkFirewall(false);
el("retryFirewall").onclick = () => checkFirewall(true);
el("networkNext").onclick = () => selectStep(status?.paired || status?.phase === "acknowledging" ? "verify" : "pair");
el("pairNext").onclick = () => selectStep(phoneConnected() ? "proxy" : "verify");
el("verifyNext").onclick = () => selectStep("proxy");
el("verifyBack").onclick = () => selectStep("network");
el("serviceRetry").onclick = async () => { startReadinessWait(); render(); await refresh(); };
el("configure").onclick = () => action(el("configure"), async () => {
  const endpoint = enteredEndpoint(), bind = el("bindAddress").value.trim(), name = el("displayName").value.trim();
  // Merely reviewing setup must not replace invitations or update generations.
  const sameEndpoint = status.endpoint && new URL(endpoint).origin === new URL(status.endpoint).origin;
  if (!sameEndpoint || bind !== status.bindAddress || name !== status.displayName) {
    clearInvitation(); clearUpdate(); firewall = null;
    await api().Configure(bind, endpoint, name);
  }
  editing = false;
  if (!dashboard) step = "network";
}, "Address saved. Review network access, then connect your phone to verify reachability.");
el("issue").onclick = () => action(el("issue"), async () => {
  if (status.paired || status.phase === "acknowledging" || el("invitation").value) return;
  const key = configKey(status), view = await api().IssueInvitation();
  invitationConfiguration = key; invitationExpiry = "";
  el("invitation").value = view.bundle; el("invitationQR").src = view.qrDataUrl; show("invitationDetails", true);
}, invitationReadyMessage);
el("copyInvitation").onclick = () => action(el("copyInvitation"), () => api().CopyInvitation(), "Invitation copied. Keep it private.");
const cancelInvitation = button => action(button, async () => {
  await api().CancelInvitation(); clearInvitation(); if (!dashboard) step = "pair";
}, "Invitation canceled. You can create a new invitation when ready.");
el("cancelInvitation").onclick = () => cancelInvitation(el("cancelInvitation"));
el("cancelPairing").onclick = () => cancelInvitation(el("cancelPairing"));
el("exportUpdate").onclick = () => action(el("exportUpdate"), async () => {
  const view = await api().ExportEndpointUpdate(); el("update").value = view.bundle;
  el("updateQR").src = view.qrDataUrl; show("updateDetails", true);
}, connectionUpdateMessage);
el("copyUpdate").onclick = () => action(el("copyUpdate"), () => api().CopyEndpointUpdate(), "Connection update copied.");
el("copyHttp").onclick = () => action(el("copyHttp"), () => api().CopyProxy("http"), "Proxy line copied.");
el("copySocks").onclick = () => action(el("copySocks"), () => api().CopyProxy("socks"), "SOCKS URL copied.");
el("revoke").onclick = () => { show("revokeConfirmation", true); };
el("cancelRevoke").onclick = () => { show("revokeConfirmation", false); };
el("confirmRevoke").onclick = () => action(el("confirmRevoke"), async () => {
  await api().Revoke(); clearInvitation(); clearUpdate(); show("revokeConfirmation", false);
}, "Phone removed. Existing connections are closed. You can pair another phone.");
startReadinessWait(); render();
void (async () => {
  try { setup = {...setup, ...await api().SetupInfo()}; } catch (_) { /* Manual configuration remains available. */ }
  metadataReady = true; fillInputs(); render(); maybeDiscover();
})();
void refresh();
setInterval(() => { if (available() || !readinessExpired) void refresh(); }, 2500);
