"use strict";
const el = id => document.getElementById(id);
const api = () => window.go.clientapp.App;
const show = (id, visible) => el(id).classList.toggle("hidden", !visible);
const steps = ["address", "network", "pair", "verify", "proxy"];
const titles = ["Computer address", "Network access", "Connect phone", "Start sharing", "Use in your apps"];
const invitationReadyMessage = "Your code is ready. Open Mobile Egress on your phone and tap Scan QR.";
const connectionUpdateMessage = "Open Mobile Egress on your phone and tap Scan QR to update this connection.";
const browserApprovalMessage = "Approve this computer in your browser, then come back here.";
const browserResumeMessage = "Finish approving this computer in your browser.";
const accountRecoveryMessage = "Inevitable couldn't approve this computer. Check your account access, then approve this computer again in your browser. Your saved phone is kept.";
const connectionFailureMessage = "Mobile Egress needs attention before it can connect. Open Connection details for the problem and next steps.";
const phoneStartMessage = "Open Mobile Egress on your phone. Tap Start cellular Agent on Android or Start sharing on iPhone. On iPhone, keep the app open and unlocked.";
const labels = {waiting:"Setup needed", migration_required:"Setup needed", listening:"Waiting for phone", awaiting_phone:"Waiting for phone", pairing:"Adding phone", acknowledging:"Adding phone", ready:"Waiting for phone", connected:"Waiting for phone", expired:"Code expired", revoked:"Phone removed", error:"Needs attention", unavailable:"App unavailable"};
let status = null, setup = {localAddresses:[], defaultBindAddress:":8443", defaultPublicPort:8443};
let step = "address", dashboard = false, initialized = false, metadataReady = false;
let managingPhone = false, reviewingSetup = false, noticeStep = "";
let renderedScreen = "";
let refreshing = null, busy = false, mutation = 0, editing = false, addressEdited = false;
let discoveryStarted = false, discovering = false, suggestedAddress = "";
let invitationConfiguration = "", invitationExpiry = "", firewall = null, firewallBusy = false;
let waitingSince = Date.now(), readinessExpired = false;
let selectedTransport = "";
let recoveryWasPending = false;
let diagnosticWasError = false;
const hostedSelected = () => (selectedTransport || status?.transport || (status?.endpoint ? "direct" : "hosted")) === "hosted";
const hostedAuthorized = (value = status) => value?.transport === "hosted" && !!value.endpoint && value.activationState === "authorized";
const available = () => status && status.phase !== "unavailable";
const phoneConnected = () => !!(available() && status.phase !== "error" && status.connected && !status.updatePending);
const pendingPhoneUpdate = () => !!(available() && status.paired && status.updatePending);
const configKey = value => [value.transport || (value.endpoint ? "direct" : "hosted"), value.endpoint, value.bindAddress, value.displayName, value.generation].join("|");
const activeInvitation = () => !!(status && status.invitationExpiresAt && new Date(status.invitationExpiresAt).getTime() > Date.now());

function computerConnectionMessage() {
  if (!available()) return "Waiting for Mobile Egress to start.";
  if (status.phase === "error") return connectionFailureMessage;
  if (status.activationState === "access_rejected" || status.gatewayState === "authorization_rejected") return accountRecoveryMessage;
  if (!hostedAuthorized()) return "Approve this computer in your browser first, then come back here.";
  if (status.gatewayState === "connected") return phoneConnected() ? "Your computer and phone are connected." : "Your computer is connected to Inevitable. Next, connect your phone and start sharing.";
  if (["connecting", "disconnected"].includes(status.gatewayState)) return "Connecting to Inevitable. We’ll retry automatically. If this takes a while, check this computer’s internet connection.";
  return "Checking this computer’s connection to Inevitable.";
}
function connectionMessage() {
  if (!available()) return "Mobile Egress is not ready yet. Follow the steps above to get it running.";
  if (status.phase === "error") return connectionFailureMessage;
  if (status.transport === "hosted" && !hostedAuthorized()) return status.activationState === "pending" ? "Approve this computer in your browser to continue." : "Connect this computer to your Inevitable account to continue.";
  if (pendingPhoneUpdate()) return "Your phone needs updated connection details. Use Reconnect your phone above.";
  if (phoneConnected()) return "Your phone is connected and ready to share mobile data.";
  if (status.phase === "acknowledging") return "Finishing up on your phone. Keep Mobile Egress open; you don’t need to scan again.";
  if (status.phase === "migration_required") return "This installation needs a new setup. Review setup to connect your phone again.";
  if (status.phase === "expired") return "That code has expired. Show a new QR code to connect your phone.";
  if (status.transport === "hosted" && status.gatewayState !== "connected") return computerConnectionMessage();
  if (status.paired) return "Your phone is saved, but it isn’t sharing with this computer yet.";
  return "Finish setup to connect your phone.";
}

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
  if (status.transport === "hosted" && !hostedAuthorized()) return "address";
  if (status.phase === "acknowledging") return "verify";
  if (activeInvitation() || status.phase === "revoked") return "pair";
  return "network";
}
function returnToDashboard() {
  if (busy) return;
  dashboard = true; managingPhone = false; reviewingSetup = false; selectedTransport = "";
  show("revokeConfirmation", false); render();
}
function selectStep(next) {
  if (busy || !available()) return;
  if (next !== "address" && !status.endpoint) return;
  if (next === "proxy" && !phoneConnected()) return;
  reviewingSetup = reviewingSetup || dashboard;
  step = next; dashboard = false; managingPhone = false; show("revokeConfirmation", false); render();
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
function renderDashboardNotice() {
  let heading = "", message = "", action = "";
  noticeStep = "";
  if (dashboard && !managingPhone && available() && status.phase !== "error") {
    if (status.transport === "hosted" && !hostedAuthorized()) {
      heading = "Connect your Inevitable account";
      message = status.activationState === "pending" ? "Approve this computer in your browser, then come back here. Your progress is saved." : "Approve this computer through your account. If you removed it from Inevitable, you can add it again without removing your saved phone.";
      action = "Connect account"; noticeStep = "address";
    } else if (!status.paired) {
      heading = status.phase === "acknowledging" ? "Finishing up on your phone" : "Finish setting up this computer";
      message = status.phase === "acknowledging" ? "Keep Mobile Egress open on your phone. You don’t need to scan again." : "Continue where you left off to connect your phone.";
      action = "Continue setup"; noticeStep = resumeStep();
    } else if (!pendingPhoneUpdate() && !phoneConnected()) {
      if (!status.running) {
        heading = "Let’s get you connected"; message = "Mobile Egress hasn’t started the connection. Check Connection details, then review your saved settings.";
        action = "Review settings"; noticeStep = "address";
      } else if (status.transport === "hosted" && status.gatewayState !== "connected") {
        heading = "Connecting this computer"; message = computerConnectionMessage();
      } else {
        heading = "Start sharing on your phone";
        message = phoneStartMessage + " Already sharing? Check that this computer is enabled in the phone app and mobile data is on.";
      }
    }
  }
  show("dashboardNotice", !!heading); show("noticeAction", !!action);
  show("unfinishedSetup", !!heading && !status?.paired);
  el("noticeHeading").textContent = heading; el("noticeMessage").textContent = message;
  el("noticeAction").textContent = action; el("noticeAction").disabled = busy || !available();
}
function render() {
  const ready = available(), connected = phoneConnected(), pendingUpdate = pendingPhoneUpdate();
  const canUpdatePhone = pendingUpdate && (status.transport !== "hosted" || status.activationState === "authorized");
  show("servicePanel", !ready); show("serviceRetry", !ready && readinessExpired);
  el("serviceMessage").textContent = readinessExpired ? "Mobile Egress couldn’t start. Try again, or run the latest installer to repair it. Your saved phone and app settings will be kept." : "Getting Mobile Egress ready. This can take up to 30 seconds.";
  show("wizardHeader", initialized && !dashboard); show("dashboardHeader", initialized && dashboard && !managingPhone);
  show("clientNavigation", initialized && (dashboard || reviewingSetup));
  for (const [id, current] of [["backToDashboard", dashboard && !managingPhone], ["managePhone", managingPhone], ["reviewSetup", !dashboard]]) {
    el(id).setAttribute("aria-current", current ? "page" : "false");
  }
  el("finishLater").textContent = reviewingSetup ? "Back to dashboard" : "Finish later";
  show("phoneSettingsHeader", initialized && managingPhone);
  show("phoneSettingsPanel", initialized && managingPhone);
  renderDashboardNotice();
  el("stepTitle").textContent = hostedSelected() && step === "address" ? "Your account" : hostedSelected() && step === "network" ? "Connect this computer" : titles[steps.indexOf(step)];
  if (step === "verify" && pendingUpdate) el("stepTitle").textContent = "Reconnect your phone";
  el("verifyStepLabel").textContent = pendingUpdate ? "Reconnect phone" : "Start sharing";
  el("addressStepLabel").textContent = hostedSelected() ? "Your account" : "Computer address";
  el("networkStepLabel").textContent = hostedSelected() ? "Connect computer" : "Network access";
  el("stepCount").textContent = "Step " + (steps.indexOf(step) + 1) + " of 5";
  for (const [index, name] of steps.entries()) {
    const button = el("step" + name[0].toUpperCase() + name.slice(1));
    button.setAttribute("aria-current", !dashboard && step === name ? "step" : "false");
    button.disabled = busy || !ready || (index > 0 && !status.endpoint) || (name === "proxy" && !connected);
  }
  for (const name of ["address", "network", "pair", "verify", "proxy"]) {
    show(name + "Panel", initialized && (dashboard ? !managingPhone && name === "proxy" : step === name));
  }
  show("addressPanel", initialized && !dashboard && !hostedSelected() && step === "address");
  show("networkPanel", initialized && !dashboard && !hostedSelected() && step === "network");
  show("hostedPanel", initialized && !dashboard && hostedSelected() && step === "address");
  show("transportChoice", initialized && !dashboard && step === "address");
  show("gatewayPanel", initialized && hostedSelected() && !dashboard && step === "network");
  show("directTroubleshooting", !hostedSelected());show("hostedTroubleshooting", hostedSelected());
  el("hostedHeading").textContent = hostedAuthorized() ? "Your account is connected" : "Connect your account";
  el("hostedDescription").textContent = hostedAuthorized() ? "This computer is already approved. Continue to review its connection." : "Sign in to Inevitable and approve this computer in your browser. Your phone won’t need a login.";
  show("hostedNameField", !hostedAuthorized());
  show("hostedApprovalHelp", !hostedAuthorized());
  if (hostedAuthorized() && [browserApprovalMessage, browserResumeMessage].includes(el("feedback").textContent)) el("feedback").textContent = "";
  el("activationMessage").textContent = status?.activationState === "pending" ? "Waiting for you to approve this computer in your browser. You can reopen that page below." : hostedAuthorized() ? "You don’t need to sign in again." : status?.activationState === "authorized" ? "Approved. Saving your settings…" : status?.activationState === "denied" ? "This request wasn’t approved. Check your account access, then try again." : status?.activationState === "expired" ? "That request has expired. Open a new one below." : "Continue below to open your browser.";
  if (status?.activationState === "access_rejected") el("activationMessage").textContent = accountRecoveryMessage;
  const activationPending = status?.activationState === "pending";
  show("resumeActivation", activationPending);show("cancelActivation", activationPending);
  el("activateHosted").disabled = busy || !ready || activationPending;
  el("activateHosted").textContent = hostedAuthorized() ? "Continue" : status?.activationState === "access_rejected" ? "Reconnect in browser" : "Continue in browser";
  el("resumeActivation").disabled = el("cancelActivation").disabled = busy || !ready;
  el("advancedDirect").disabled = el("chooseHosted").disabled = busy || !ready;
  el("gatewayNext").disabled = busy || !ready || status?.transport !== "hosted" || !status?.endpoint;
  el("gatewayMessage").textContent = computerConnectionMessage();
  el("gatewayNext").textContent = status?.paired ? "Continue to your phone" : "Connect your phone";
  const recoveryVisible = initialized && (canUpdatePhone || (managingPhone && status?.paired));
  show("recoveryPanel", recoveryVisible);
  if ((dashboard || step !== "pair") && el("feedback").textContent === invitationReadyMessage) el("feedback").textContent = "";
  if (!recoveryVisible && el("feedback").textContent === connectionUpdateMessage) el("feedback").textContent = "";
  show("removePhone", initialized && managingPhone && status?.paired);
  if (canUpdatePhone && !recoveryWasPending) el("recoveryPanel").open = true;
  if (!canUpdatePhone && recoveryWasPending) el("recoveryPanel").open = false;
  recoveryWasPending = canUpdatePhone;
  el("recoveryHeading").textContent = canUpdatePhone ? "Reconnect your phone" : "Connection updates";
  el("recoveryMessage").textContent = canUpdatePhone ? "This computer’s connection details changed. Show the update QR below, then scan it on your saved phone to reconnect." : pendingUpdate ? "Approve this computer in your browser first. Then show the update QR and scan it on your phone." : "If this computer’s connection details change, scan an update QR on your saved phone to reconnect.";
  show("connectionSummary", initialized && !managingPhone && (dashboard || status?.phase === "error"));
  el("phase").textContent = connected ? "Connected" : labels[status?.phase] || "Checking connection";
  el("phase").className = "badge " + (!ready || status?.phase === "error" ? "error" : connected ? "ready" : "");
  el("message").textContent = connectionMessage();
  el("diagnosticMessage").textContent = status?.message || "No additional details are available yet.";
  if (!initialized) el("connectionDetails").open = false;
  if (status?.phase === "error" && !diagnosticWasError) el("connectionDetails").open = true;
  diagnosticWasError = status?.phase === "error";
  el("installedState").textContent = ready ? "Running" : "Checking";
  el("listeningState").textContent = ready && status.running && status.endpoint && !["waiting","migration_required","error"].includes(status.phase) ? "Listening" : "Not confirmed";
  el("connectionType").textContent = status?.transport === "hosted" ? "Inevitable connection" : "Direct connection";
  if (status?.transport === "hosted") el("listeningState").textContent = ready ? ({connected:"Computer connected", connecting:"Connecting", disconnected:"Reconnecting", authorization_rejected:"Account approval needed"}[status.gatewayState] || "Checking connection") : "Not confirmed";
  el("pairedState").textContent = status?.paired ? "Saved" : status?.phase === "acknowledging" ? "Adding phone" : "Not added";
  el("connectedState").textContent = connected ? "Connected" : "Waiting";
  el("httpAddress").textContent = status?.httpAddress ? "HTTP            " + status.httpAddress : "";
  el("socksAddress").textContent = status?.socksAddress ? "SOCKS5          " + status.socksAddress : "";
  el("copyHttp").disabled = busy || !ready || !status.httpAddress;
  el("copySocks").disabled = busy || !ready || !status.socksAddress;
  el("configure").disabled = busy || !ready;
  el("configure").textContent = "Save and continue";
  show("finishSetup", !dashboard); show("pairNext", !dashboard);
  el("finishLater").disabled = el("finishSetup").disabled = el("backToDashboard").disabled = busy;
  el("reviewSetup").disabled = busy || !ready;
  el("managePhone").disabled = busy || !ready || !status?.paired;
  el("confirmRevoke").disabled = busy || !ready || !status?.paired;
  el("cancelRevoke").disabled = busy;
  el("networkNext").disabled = busy || !ready || !status.endpoint;
  el("verifyNext").disabled = busy || !connected;
  show("verifyNext", connected);
  el("pairNext").disabled = busy || !ready;
  el("issue").disabled = busy || !ready || !status.running || !status.endpoint || status.paired || status.phase === "acknowledging" || !!el("invitation").value;
  el("issue").textContent = "Show QR code";
  el("revoke").disabled = busy || !ready || !status.paired;
  el("exportUpdate").disabled = el("copyUpdate").disabled = busy || !ready || !status.paired || (status.transport === "hosted" && !hostedAuthorized());
  el("copyInvitation").disabled = busy || !ready || !el("invitation").value;
  el("cancelInvitation").disabled = busy || !ready || (!activeInvitation() && status.phase !== "acknowledging");
  show("cancelPairing", status?.phase === "acknowledging");
  el("cancelPairing").disabled = busy || !ready;
  el("pending").textContent = pendingUpdate ? (canUpdatePhone ? "Reconnect your phone using the update QR above." : "Approve this computer in your browser, then update your phone’s connection.") : "";
  el("pairNote").textContent = status?.paired ? "Your phone is already saved. Continue to start sharing, or use Phone settings to replace it." : status?.phase === "acknowledging" ? "Finishing up on your phone. Keep Mobile Egress open with mobile data on. You don’t need a new code." : "Show the QR code below. On your phone, open Mobile Egress, tap Scan QR and scan this screen. Keep mobile data on.";
  const expires = activeInvitation() ? new Date(status.invitationExpiresAt) : null;
  el("expiry").textContent = expires ? "This code expires at " + expires.toLocaleTimeString() + "." : "";
  const confirmingPairing = status?.phase === "acknowledging";
  show("phoneStartInstructions", ready && status.paired && !confirmingPairing && !connected && !pendingUpdate);
  el("verifyHeading").textContent = pendingUpdate ? "Update the connection on your phone" : connected ? "Your phone is connected" : confirmingPairing ? "Finishing up on your phone" : "Start sharing on your phone";
  el("verificationMessage").textContent = !ready ? "Waiting for Mobile Egress to start." : pendingUpdate ? (canUpdatePhone ? "Scan the update QR above on your phone. We’ll continue automatically when the update is saved and your phone connects." : "Approve this computer in Inevitable first, then update the saved connection on your phone.") : connected ? "You’re ready to set up your apps." : confirmingPairing ? "Keep Mobile Egress open on your phone while it finishes. You don’t need to scan again." : status?.paired ? "One more step: start sharing in the phone app. We’ll continue automatically when it connects." : "Go to Connect phone and scan the QR code in the Mobile Egress phone app first.";
  if ((status?.paired || confirmingPairing) && el("feedback").textContent === invitationReadyMessage) el("feedback").textContent = "";
  el("proxyConnection").textContent = connected ? "Copy a format below and paste it into your app’s proxy settings." : "You can copy these settings into your app’s proxy settings now. They’ll work when your phone connects and starts sharing.";
  el("version").textContent = status?.version ? "Client " + status.version : "";
  renderNetwork();
  const screen = initialized ? (managingPhone ? "phoneSettings" : dashboard ? "dashboard" : step) : "";
  if (screen && screen !== renderedScreen) {
    renderedScreen = screen;
    el(managingPhone ? "phoneSettingsHeading" : dashboard ? "dashboardHeading" : "stepTitle").focus();
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
      if (managingPhone && !status.paired) managingPhone = false;
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
el("reviewSetup").onclick = () => { if (dashboard) selectStep("address"); };
el("noticeAction").onclick = () => { if (noticeStep) selectStep(noticeStep); };
el("managePhone").onclick = () => {
  if (!busy && available() && status.paired && !managingPhone) {
    dashboard = true; managingPhone = true; reviewingSetup = false; selectedTransport = "";
    show("revokeConfirmation", false); render();
  }
};
el("backToDashboard").onclick = returnToDashboard;
el("advancedDirect").onclick = () => { if (!busy && available()) { selectedTransport = "direct"; step = "address"; dashboard = false; render(); maybeDiscover(); } };
el("chooseHosted").onclick = () => { if (!busy && available()) { selectedTransport = "hosted"; step = "address"; dashboard = false; render(); } };
el("activateHosted").onclick = () => hostedAuthorized() ? selectStep("network") : action(el("activateHosted"), () => api().StartHostedActivation(el("hostedName").value.trim()), browserApprovalMessage);
el("resumeActivation").onclick = () => action(el("resumeActivation"), () => api().ResumeHostedActivation(), browserResumeMessage);
el("cancelActivation").onclick = () => action(el("cancelActivation"), () => api().CancelHostedActivation(), "Request canceled. You can start again when you’re ready.");
el("gatewayNext").onclick = () => selectStep(status?.paired ? "verify" : "pair");
el("finishLater").onclick = el("finishSetup").onclick = returnToDashboard;
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
}, "Address saved. Review network access, then connect your phone.");
el("issue").onclick = () => action(el("issue"), async () => {
  if (status.paired || status.phase === "acknowledging" || el("invitation").value) return;
  const key = configKey(status), view = await api().IssueInvitation();
  invitationConfiguration = key; invitationExpiry = "";
  el("invitation").value = view.bundle; el("invitationQR").src = view.qrDataUrl; show("invitationDetails", true);
}, invitationReadyMessage);
el("copyInvitation").onclick = () => action(el("copyInvitation"), () => api().CopyInvitation(), "Setup code copied. Keep it private.");
const cancelInvitation = button => action(button, async () => {
  await api().CancelInvitation(); clearInvitation(); if (!dashboard) step = "pair";
}, "Code canceled. Show a new QR code when you’re ready.");
el("cancelInvitation").onclick = () => cancelInvitation(el("cancelInvitation"));
el("cancelPairing").onclick = () => cancelInvitation(el("cancelPairing"));
el("exportUpdate").onclick = () => action(el("exportUpdate"), async () => {
  const view = await api().ExportEndpointUpdate(); el("update").value = view.bundle;
  el("updateQR").src = view.qrDataUrl; show("updateDetails", true);
}, connectionUpdateMessage);
el("copyUpdate").onclick = () => action(el("copyUpdate"), () => api().CopyEndpointUpdate(), "Update copied. Import it in Mobile Egress on your saved phone.");
el("copyHttp").onclick = () => action(el("copyHttp"), () => api().CopyProxy("http"), "Copied. Paste it into your app’s proxy settings.");
el("copySocks").onclick = () => action(el("copySocks"), () => api().CopyProxy("socks"), "Copied. Paste it into your app’s proxy settings.");
el("revoke").onclick = () => { show("revokeConfirmation", true); };
el("cancelRevoke").onclick = () => { show("revokeConfirmation", false); };
el("confirmRevoke").onclick = () => action(el("confirmRevoke"), async () => {
  await api().Revoke(); clearInvitation(); clearUpdate(); show("revokeConfirmation", false);
  managingPhone = false; dashboard = false; reviewingSetup = true; step = "pair"; selectedTransport = "";
}, "Phone removed from this computer. Show a new QR code to connect a phone again.");
startReadinessWait(); render();
void (async () => {
  try { setup = {...setup, ...await api().SetupInfo()}; } catch (_) { /* Manual configuration remains available. */ }
  metadataReady = true; fillInputs(); render(); maybeDiscover();
})();
void refresh();
setInterval(() => { if (available() || !readinessExpired) void refresh(); }, 2500);
