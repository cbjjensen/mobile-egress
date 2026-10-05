import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const flush = async () => { for (let i=0;i<30;i++) await Promise.resolve(); };
const deferred = () => { let resolve; const promise=new Promise(r=>{resolve=r;}); return {promise,resolve}; };

// Native bridge and DOM doubles let Node exercise the real frontend handlers.
async function harness(options={}) {
  let now=Date.parse('2026-10-03T12:00:00Z');
  const elements=new Map(), timers=[], calls=[];
  const tags=[...fs.readFileSync(new URL('./index.html',import.meta.url),'utf8').matchAll(/<[^>]+\bid="([^"]+)"[^>]*>/g)];
  const markup=new Map(tags.map(tag=>[tag[1],tag[0]]));
  assert.equal(markup.size,tags.length,'HTML ids must be unique');
  const get=id=>{
    assert.ok(markup.has(id),'Missing HTML element: '+id);
    if (!elements.has(id)) {
      const classes=new Set((markup.get(id).match(/class="([^"]*)"/)?.[1] || '').split(/\s+/).filter(Boolean));
      elements.set(id,{value:'',textContent:'',src:'',disabled:false,className:'',hidden:false,
        classList:{toggle(name,on){if(on===undefined)on=!classes.has(name);if(on)classes.add(name);else classes.delete(name);},add(name){classes.add(name);},remove(name){classes.delete(name);},contains(name){return classes.has(name);}},
        setAttribute(name,value){this[name]=value;},removeAttribute(name){this[name]='';},focus(){context.document.activeElement=this;}});
    }
    return elements.get(id);
  };
  const status={transport:'direct',phase:'listening',message:'Listening',running:true,connected:false,generation:1,endpoint:'https://client.example:8443',bindAddress:':8443',displayName:'Workload',paired:false,updatePending:false,httpAddress:'127.0.0.1:1081',socksAddress:'127.0.0.1:1080',version:'2.0.0',...options.status};
  const api={Status:async()=>({...status}),SetupInfo:async()=>({platform:'windows',suggestedName:'My computer',localAddresses:['192.168.1.20'],defaultBindAddress:':8443',defaultPublicPort:8443}),DiscoverPublicAddress:async()=>{calls.push(['discover']);return {address:'203.0.113.30',endpoint:'https://203.0.113.30:8443',provider:'ipify'};},
    Configure:async(bind,endpoint,name)=>{calls.push(['configure',bind,endpoint,name]);Object.assign(status,{bindAddress:bind,endpoint,displayName:name,generation:status.generation+1,phase:'listening',invitationExpiresAt:undefined});},
    IssueInvitation:async()=>{calls.push(['issue']);status.invitationExpiresAt=new Date(now+600000).toISOString();status.phase='awaiting_phone';return {bundle:'invitation',qrDataUrl:'data:image/png;base64,aA=='};},
    CancelInvitation:async()=>{calls.push(['cancel']);delete status.invitationExpiresAt;status.phase='listening';},Revoke:async()=>{calls.push(['revoke']);status.paired=false;status.connected=false;},CopyProxy:async kind=>calls.push(['proxy',kind]),ExportEndpointUpdate:async()=>({bundle:'signed-update',qrDataUrl:'data:image/png;base64,aA=='}),CopyEndpointUpdate:async()=>calls.push(['copy-update']),CopyInvitation:async()=>calls.push(['copy-invitation']),
    CheckFirewall:async()=>{calls.push(['check-firewall']);return {state:'allowed',scope:'port',port:8443,message:'Rule allows this listener.'};},RetryFirewall:async()=>{calls.push(['retry-firewall']);return {state:'allowed',scope:'port',port:8443,message:'Rule allows this listener.'};},...options.api};
  class Clock extends Date { constructor(...args){super(...(args.length?args:[now]));}static now(){return now;} }
  const context=vm.createContext({document:{getElementById:get,activeElement:null},go:{clientapp:{App:api}},setInterval(fn,delay){timers.push({fn,delay,at:now+delay,interval:true});},setTimeout(fn,delay){timers.push({fn,delay,at:now+delay});},Date:Clock,URL,console});
  context.window=context;
  vm.runInContext(fs.readFileSync(new URL('./app.js',import.meta.url),'utf8'),context);
  await flush();
  return {get,calls,status,context,api,refresh:async()=>{await vm.runInContext('refresh()',context);await flush();},advance:async(ms)=>{now+=ms;for(const timer of [...timers])if(timer.at<=now){timer.at=timer.interval?now+timer.delay:Infinity;await timer.fn();}await flush();}};
}
const visible=(h,id)=>!h.get(id).classList.contains('hidden');
const edit=(h,id,value)=>{h.get(id).value=value;h.get(id).oninput();};
const operations=h=>h.calls.filter(c=>!['check-firewall','discover'].includes(c[0]));

test('fresh setup starts at address, suggests name and public address once',async()=>{
 const h=await harness({status:{endpoint:'',bindAddress:'',displayName:'',phase:'waiting',generation:0}});
 assert.equal(h.get('stepTitle').textContent,'Computer address');
 assert.equal(h.get('displayName').value,'My computer');assert.equal(h.get('endpoint').value,'203.0.113.30');
 assert.equal(h.get('publicPort').value,'8443');await h.refresh();await h.refresh();
 assert.equal(h.calls.filter(c=>c[0]==='discover').length,1);assert.deepEqual(operations(h),[]);
});
test('saved configuration resumes network access without lookup or invitation creation',async()=>{
 const h=await harness();assert.equal(h.get('stepTitle').textContent,'Network access');
 assert.equal(h.get('endpoint').value,'client.example');assert.equal(h.calls.some(c=>c[0]==='discover'||c[0]==='issue'),false);
});
test('existing paired installation opens dashboard even while offline',async()=>{
 const h=await harness({status:{paired:true,phase:'awaiting_phone'}});
 assert.equal(visible(h,'wizardHeader'),false);assert.equal(visible(h,'dashboardHeader'),true);
 assert.equal(h.get('connectedState').textContent,'Waiting');assert.equal(h.calls.some(c=>c[0]==='discover'),false);
 await h.get('reviewSetup').onclick();assert.equal(h.get('stepTitle').textContent,'Computer address');
 await h.get('configure').onclick();assert.equal(h.calls.some(c=>c[0]==='configure'),false);assert.equal(h.status.paired,true);
});

test('review setup uses stable active page navigation and a dashboard return label',async()=>{
 const h=await harness({status:{paired:true,connected:false}});
 const active=id=>assert.equal(h.get(id)['aria-current'],'page');
 assert.equal(visible(h,'clientNavigation'),true);active('backToDashboard');
 await h.get('reviewSetup').onclick();active('reviewSetup');
 assert.equal(h.get('finishLater').textContent,'Back to dashboard');
 assert.equal(h.get('backToDashboard')['aria-current'],'false');
 for(const id of ['stepNetwork','stepPair','stepVerify']){
  await h.get(id).onclick();assert.equal(visible(h,'clientNavigation'),true);active('reviewSetup');
  assert.equal(h.get('finishLater').textContent,'Back to dashboard');
 }
 await h.get('managePhone').onclick();active('managePhone');
 assert.equal(visible(h,'wizardHeader'),false);assert.equal(visible(h,'phoneSettingsPanel'),true);
 await h.get('backToDashboard').onclick();active('backToDashboard');
 assert.equal(visible(h,'phoneSettingsPanel'),false);assert.equal(visible(h,'proxyPanel'),true);
 assert.deepEqual(operations(h),[]);
});

test('fresh setup keeps Finish later while revisiting it is an explicit review',async()=>{
 const h=await harness({status:{endpoint:'',phase:'waiting'}});
 assert.equal(visible(h,'clientNavigation'),false);assert.equal(h.get('finishLater').textContent,'Finish later');
 await h.get('finishLater').onclick();assert.equal(visible(h,'clientNavigation'),true);
 await h.get('reviewSetup').onclick();assert.equal(h.get('finishLater').textContent,'Back to dashboard');
 assert.equal(visible(h,'clientNavigation'),true);assert.equal(h.get('managePhone').disabled,true);
});

test('review tab preserves the current step and unsaved input across page navigation',async()=>{
 const h=await harness({status:{paired:true}});
 await h.get('reviewSetup').onclick();edit(h,'endpoint','edited.example');
 await h.get('stepNetwork').onclick();await h.get('reviewSetup').onclick();
 assert.equal(h.get('stepTitle').textContent,'Network access');
 await h.get('managePhone').onclick();await h.get('reviewSetup').onclick();
 assert.equal(h.get('endpoint').value,'edited.example');
 assert.equal(visible(h,'phoneSettingsPanel'),false);assert.equal(visible(h,'addressPanel'),true);
 assert.deepEqual(operations(h),[]);
});

test('review retains its dashboard exit during service loss and blocks navigation during a save',async()=>{
 const h=await harness({status:{paired:true}});
 await h.get('reviewSetup').onclick();edit(h,'endpoint','edited.example');
 const saving=deferred();h.api.Configure=()=>saving.promise;const action=h.get('configure').onclick();
 for(const id of ['backToDashboard','managePhone','reviewSetup'])assert.equal(h.get(id).disabled,true);
 await h.get('backToDashboard').onclick();await h.get('managePhone').onclick();
 assert.equal(visible(h,'wizardHeader'),true);
 saving.resolve();await action;
 h.api.Status=async()=>{throw new Error('offline');};await h.refresh();
 assert.equal(h.get('finishLater').textContent,'Back to dashboard');assert.equal(h.get('backToDashboard').disabled,false);
 await h.get('backToDashboard').onclick();assert.equal(visible(h,'dashboardHeader'),true);
});
test('reviewing a saved default HTTPS port never rewrites configuration',async()=>{
 const h=await harness({status:{endpoint:'https://client.example',paired:true}});
 await h.get('reviewSetup').onclick();await h.get('configure').onclick();
 assert.equal(h.calls.some(c=>c[0]==='configure'),false);
});
test('endpoint save sends separate local and public ports and brackets IPv6',async()=>{
 const h=await harness();await h.get('stepAddress').onclick();
 edit(h,'bindAddress',':9443');edit(h,'endpoint','2001:db8::15');edit(h,'publicPort','443');edit(h,'displayName','Mac Client');
 await h.get('configure').onclick();
 assert.deepEqual(operations(h),[['configure',':9443','https://[2001:db8::15]:443','Mac Client']]);
 assert.equal(h.get('stepTitle').textContent,'Network access');
 assert.equal(h.context.document.activeElement,h.get('stepTitle'));
});
test('invalid public port stays on address without saving',async()=>{
 const h=await harness();await h.get('stepAddress').onclick();edit(h,'publicPort','0');await h.get('configure').onclick();
 assert.equal(h.calls.some(c=>c[0]==='configure'),false);assert.match(h.get('feedback').textContent,/port/i);assert.equal(h.get('stepTitle').textContent,'Computer address');
});
test('late public lookup never replaces an address the owner entered',async()=>{
 const lookup=deferred();const h=await harness({status:{endpoint:'',displayName:'',phase:'waiting'},api:{DiscoverPublicAddress:()=>lookup.promise}});
 edit(h,'endpoint','my.example');lookup.resolve({address:'203.0.113.30',provider:'ipify'});await flush();await h.refresh();
 assert.equal(h.get('endpoint').value,'my.example');
});
test('late lookup cannot replace a saved or externally configured endpoint',async()=>{
 const lookup=deferred();const h=await harness({status:{endpoint:'',phase:'waiting'},api:{DiscoverPublicAddress:()=>lookup.promise}});
 Object.assign(h.status,{endpoint:'https://saved.example:9443',bindAddress:':9443',generation:2});await h.refresh();
 lookup.resolve({address:'203.0.113.31'});await flush();
 assert.equal(h.get('endpoint').value,'saved.example');assert.equal(h.get('publicPort').value,'9443');
});
test('explicit lookup on saved settings shows a suggestion without applying it',async()=>{
 const h=await harness();await h.get('retryDiscovery').onclick();
 assert.equal(h.get('endpoint').value,'client.example');assert.match(h.get('discoveryMessage').textContent,/203\.0\.113\.30/);
});
test('retry replaces an untouched automatic suggestion and Save uses the new address',async()=>{
 const h=await harness({status:{endpoint:'',bindAddress:'',displayName:'',phase:'waiting',generation:0}});
 assert.equal(h.get('endpoint').value,'203.0.113.30');
 h.api.DiscoverPublicAddress=async()=>({address:'203.0.113.31',provider:'ipify'});
 await h.get('retryDiscovery').onclick();
 await h.get('configure').onclick();
 assert.deepEqual(operations(h),[['configure',':8443','https://203.0.113.31:8443','My computer']]);
 assert.equal(h.get('endpoint').value,'203.0.113.31');
});
test('typing during an address retry preserves the manual address through Save',async()=>{
 const h=await harness({status:{endpoint:'',phase:'waiting'}}), retry=deferred();
 assert.equal(h.get('endpoint').value,'203.0.113.30');
 h.api.DiscoverPublicAddress=()=>retry.promise;
 const lookup=h.get('retryDiscovery').onclick();
 edit(h,'endpoint','manual.example');
 retry.resolve({address:'203.0.113.31',provider:'ipify'});await lookup;
 await h.get('configure').onclick();
 assert.equal(h.get('endpoint').value,'manual.example');
 assert.equal(operations(h).find(call=>call[0]==='configure')[2],'https://manual.example:8443');
});
test('a retry completed after Save preserves the saved automatic address',async()=>{
 const h=await harness({status:{endpoint:'',phase:'waiting'}}), retry=deferred();
 assert.equal(h.get('endpoint').value,'203.0.113.30');
 h.api.DiscoverPublicAddress=()=>retry.promise;
 const lookup=h.get('retryDiscovery').onclick();
 await h.get('configure').onclick();
 retry.resolve({address:'203.0.113.31',provider:'ipify'});await lookup;await h.refresh();
 assert.equal(h.get('endpoint').value,'203.0.113.30');
 assert.equal(h.status.endpoint,'https://203.0.113.30:8443');
});
test('failed discovery leaves manual setup available with explicit retry',async()=>{
 let attempts=0;const h=await harness({status:{endpoint:'',phase:'waiting'},api:{DiscoverPublicAddress:async()=>{attempts++;throw new Error('Address lookup unavailable.');}}});
 assert.match(h.get('discoveryMessage').textContent,/unavailable/i);assert.equal(h.get('configure').disabled,false);assert.equal(attempts,1);
 await h.refresh();assert.equal(attempts,1);await h.get('retryDiscovery').onclick();assert.equal(attempts,2);
});
test('status polling preserves edited endpoint inputs',async()=>{
 const h=await harness();edit(h,'endpoint','new.example');await h.refresh();assert.equal(h.get('endpoint').value,'new.example');
});
test('resuming an active invitation offers its existing QR without auto-issuing',async()=>{
 const h=await harness({status:{phase:'awaiting_phone',invitationExpiresAt:'2026-10-03T12:10:00Z'}});
 assert.equal(h.get('stepTitle').textContent,'Pair phone');assert.equal(h.calls.some(c=>c[0]==='issue'),false);
 await h.get('issue').onclick();await h.refresh();await h.refresh();assert.equal(h.get('invitation').value,'invitation');
 assert.equal(h.calls.filter(c=>c[0]==='issue').length,1);assert.equal(h.get('issue').disabled,true);
});
test('lost pairing acknowledgement resumes verification without replacing invitation',async()=>{
 const h=await harness({status:{phase:'acknowledging',invitationExpiresAt:'2026-10-03T12:10:00Z'}});
 assert.equal(h.get('stepTitle').textContent,'Start on your phone');assert.equal(h.calls.some(c=>c[0]==='issue'),false);
 assert.match(h.get('verificationMessage').textContent,/confirm|pairing/i);assert.equal(h.get('verifyNext').disabled,true);
 assert.equal(visible(h,'phoneStartInstructions'),false);assert.equal(visible(h,'cancelPairing'),true);
});
test('verification requires connected status even if paired or phase says ready',async()=>{
 const h=await harness();await h.get('networkNext').onclick();await h.get('issue').onclick();
 Object.assign(h.status,{paired:true,phase:'ready',connected:false});await h.refresh();
 assert.equal(h.get('stepTitle').textContent,'Start on your phone');assert.equal(h.get('verifyNext').disabled,true);
 assert.equal(h.get('verifyHeading').textContent,'Start sharing on your phone');
 assert.equal(visible(h,'phoneStartInstructions'),true);assert.equal(visible(h,'verifyNext'),false);
 assert.match(h.get('verificationMessage').textContent,/automatically/);assert.equal(h.get('feedback').textContent,'');
 Object.assign(h.status,{connected:true});await h.refresh();
 assert.equal(h.get('stepTitle').textContent,'Use your proxy');assert.equal(h.get('connectedState').textContent,'Connected');
 assert.equal(visible(h,'phoneStartInstructions'),false);
});

test('phone instructions wait for pairing and preserve unrelated action errors',async()=>{
 const h=await harness();await h.get('stepVerify').onclick();
 assert.equal(visible(h,'phoneStartInstructions'),false);assert.match(h.get('verificationMessage').textContent,/scan/i);
 await h.get('stepPair').onclick();await h.get('issue').onclick();
 Object.assign(h.status,{phase:'acknowledging'});await h.refresh();
 assert.equal(h.get('feedback').textContent,'');assert.equal(visible(h,'phoneStartInstructions'),false);
 h.get('feedback').textContent='Connection update could not be copied.';
 Object.assign(h.status,{paired:true,phase:'ready'});await h.refresh();
 assert.equal(h.get('feedback').textContent,'Connection update could not be copied.');
 assert.equal(visible(h,'phoneStartInstructions'),true);
});

test('reactivated paired Client presents connection recovery on dashboard and in setup',async()=>{
 const h=await harness({status:{transport:'hosted',activationState:'authorized',gatewayState:'connected',paired:true,generation:2,updatePending:true,connected:false},api:{ExportEndpointUpdate:async()=>{h.calls.push(['export-update']);return {bundle:'signed-update',qrDataUrl:'data:image/png;base64,aA=='};}}});
 assert.equal(visible(h,'dashboardHeader'),true);assert.equal(visible(h,'recoveryPanel'),true);
 assert.equal(h.get('recoveryPanel').open,true);assert.equal(h.get('recoveryHeading').textContent,'Reconnect your phone');
 await h.get('reviewSetup').onclick();await h.get('stepVerify').onclick();
 assert.equal(visible(h,'recoveryPanel'),true);assert.equal(h.get('stepTitle').textContent,'Reconnect your phone');
 assert.equal(visible(h,'phoneStartInstructions'),false);assert.match(h.get('verificationMessage').textContent,/update/i);
 await h.get('exportUpdate').onclick();assert.equal(h.get('update').value,'signed-update');
 assert.deepEqual(operations(h),[['export-update']]);assert.equal(h.status.generation,2);assert.equal(h.status.paired,true);
});

test('pending update cannot complete setup and acknowledgement clears exported recovery QR',async()=>{
 const h=await harness({status:{paired:true,updatePending:true,generation:2}});
 await h.get('reviewSetup').onclick();await h.get('stepVerify').onclick();await h.get('exportUpdate').onclick();
 h.status.connected=true;await h.refresh();
 assert.equal(h.get('stepTitle').textContent,'Reconnect your phone');assert.equal(h.get('verifyNext').disabled,true);
 await h.get('stepProxy').onclick();assert.equal(h.get('stepTitle').textContent,'Reconnect your phone');
 h.status.updatePending=false;await h.refresh();
 assert.equal(h.get('stepTitle').textContent,'Use your proxy');assert.equal(h.get('update').value,'');
 assert.equal(h.get('updateQR').src,'');assert.equal(h.get('feedback').textContent,'');
});

test('acknowledged recovery leaves the dashboard and remains in Phone settings',async()=>{
 const h=await harness({status:{transport:'direct',paired:true,updatePending:true}});
 assert.equal(visible(h,'recoveryPanel'),true);assert.equal(h.get('recoveryPanel').open,true);
 h.status.updatePending=false;await h.refresh();
 assert.equal(h.get('recoveryPanel').open,false);assert.equal(visible(h,'recoveryPanel'),false);
 await h.get('managePhone').onclick();assert.equal(visible(h,'recoveryPanel'),true);
 h.get('recoveryPanel').open=true;await h.get('exportUpdate').onclick();await h.refresh();
 assert.equal(h.get('recoveryPanel').open,true);assert.equal(h.get('update').value,'signed-update');
});

test('hosted recovery prioritizes activation before exporting a pending route update',async()=>{
 const h=await harness({status:{transport:'hosted',paired:true,updatePending:true,activationState:'access_rejected',gatewayState:'authorization_rejected'}});
 assert.equal(h.get('exportUpdate').disabled,true);assert.equal(h.get('copyUpdate').disabled,true);
 assert.match(h.get('pending').textContent,/activation.*browser/i);assert.match(h.get('recoveryMessage').textContent,/activation.*browser/i);
 assert.equal(h.get('activateHosted').textContent,'Reactivate in browser');
 h.status.activationState='pending';await h.refresh();assert.equal(h.get('exportUpdate').disabled,true);
 h.status.activationState='authorized';await h.refresh();
 assert.equal(h.get('exportUpdate').disabled,false);assert.equal(h.get('recoveryPanel').open,true);
 await h.get('exportUpdate').onclick();assert.equal(h.get('update').value,'signed-update');
 h.status.activationState='access_rejected';await h.refresh();
 assert.equal(h.get('update').value,'');assert.equal(h.get('updateQR').src,'');assert.equal(h.get('exportUpdate').disabled,true);
});

for (const change of ['generation','removed','unavailable']) test(`connection recovery clears a stale QR when ${change}`,async()=>{
 const h=await harness({status:{paired:true,updatePending:true,generation:2}});
 await h.get('exportUpdate').onclick();
 if(change==='generation'){h.status.generation++;h.status.endpoint='https://changed.example';}
 if(change==='removed'){h.status.paired=false;h.status.updatePending=false;}
 if(change==='unavailable')h.api.Status=async()=>{throw new Error('Service unavailable');};
 await h.refresh();assert.equal(h.get('update').value,'');assert.equal(h.get('updateQR').src,'');
 if(change==='unavailable')assert.equal(h.get('exportUpdate').disabled,true);
});
test('finish later and review preserve invitation and service configuration',async()=>{
 const h=await harness();await h.get('networkNext').onclick();await h.get('issue').onclick();
 const before=JSON.stringify(h.status);await h.get('finishLater').onclick();assert.equal(visible(h,'dashboardHeader'),true);
 assert.equal(visible(h,'unfinishedSetup'),true);
 await h.get('reviewSetup').onclick();assert.equal(JSON.stringify(h.status),before);assert.equal(h.calls.filter(c=>c[0]==='issue').length,1);
});
test('finished paired dashboard does not ask the owner to finish setup',async()=>{
 const h=await harness({status:{paired:true}});assert.equal(visible(h,'unfinishedSetup'),false);
});
test('invitation cancellation clears rendered capability after service succeeds',async()=>{
 const h=await harness();await h.get('issue').onclick();assert.equal(h.get('invitation').value,'invitation');
 await h.get('cancelInvitation').onclick();assert.equal(h.get('invitation').value,'');assert.equal(h.get('invitationQR').src,'');assert.deepEqual(operations(h),[['issue'],['cancel']]);
});
for(const reason of ['expired','cancelled elsewhere','new generation'])test(`${reason} clears displayed invitation on refresh`,async()=>{
 const h=await harness();await h.get('issue').onclick();
 if(reason==='expired')h.status.invitationExpiresAt='2026-10-03T11:59:00Z';
 if(reason==='cancelled elsewhere')delete h.status.invitationExpiresAt;
 if(reason==='new generation'){h.status.generation++;h.status.endpoint='https://other.example:8443';}
 await h.refresh();assert.equal(h.get('invitation').value,'');assert.equal(h.get('invitationQR').src,'');
});
test('endpoint changes clear invitation and exported update even when firewall step returns an error',async()=>{
 const h=await harness({api:{Configure:async()=>{throw new Error('Firewall setup failed.');}}});await h.get('issue').onclick();await h.get('exportUpdate').onclick();
 edit(h,'endpoint','other.example');await h.get('configure').onclick();assert.equal(h.get('invitation').value,'');assert.equal(h.get('update').value,'');assert.equal(h.get('updateQR').src,'');
});
test('firewall check and retry never configure or replace pairing',async()=>{
 const h=await harness();await h.get('issue').onclick();const before=JSON.stringify(h.status);
 await h.get('checkFirewall').onclick();await h.get('retryFirewall').onclick();
 assert.equal(JSON.stringify(h.status),before);assert.equal(h.get('invitation').value,'invitation');
 assert.equal(h.calls.some(c=>c[0]==='configure'),false);assert.equal(h.calls.filter(c=>c[0]==='issue').length,1);
 assert.match(h.get('firewallMessage').textContent,/not.*reachability|not.*reachable/i);
});
test('firewall failure from old configuration cannot overwrite a newer check',async()=>{
 let reject;const h=await harness();
 h.api.CheckFirewall=()=>new Promise((_,r)=>{reject=r;});
 const check=h.get('checkFirewall').onclick();
 Object.assign(h.status,{endpoint:'https://other.example:9443',bindAddress:':9443',generation:2});await h.refresh();
 reject(new Error('Old port blocked'));await check;
 assert.doesNotMatch(h.get('firewallMessage').textContent,/Old port blocked/);
});
test('network guidance uses saved listener and public ports plus LAN address',async()=>{
 const h=await harness({status:{bindAddress:':9443',endpoint:'https://public.example:443'}});
 assert.match(h.get('networkInstructions').textContent,/443/);assert.match(h.get('networkInstructions').textContent,/9443/);assert.match(h.get('networkInstructions').textContent,/192\.168\.1\.20/);
 h.get('networkLocation').value='aws';await h.get('networkLocation').onchange();
 assert.match(h.get('networkInstructions').textContent,/9443/);assert.match(h.get('networkInstructions').textContent,/My IP/);assert.match(h.get('networkInstructions').textContent,/cellular/);
});
test('unavailable service waits thirty seconds then offers retry and repair',async()=>{
 const h=await harness({status:{phase:'unavailable',endpoint:'',running:false}});
 assert.equal(visible(h,'serviceRetry'),false);assert.equal(h.get('configure').disabled,true);
 await h.advance(29999);assert.equal(visible(h,'serviceRetry'),false);
 await h.advance(1);assert.equal(visible(h,'serviceRetry'),true);assert.match(h.get('serviceMessage').textContent,/repair/i);
 Object.assign(h.status,{phase:'waiting',running:true});await h.get('serviceRetry').onclick();assert.equal(h.get('stepTitle').textContent,'Computer address');
});
test('transient service loss cannot display stale connected success',async()=>{
 const h=await harness({status:{paired:true,connected:true}});assert.equal(h.get('connectedState').textContent,'Connected');
 h.api.Status=async()=>{throw new Error('IPC failed');};await h.refresh();assert.notEqual(h.get('connectedState').textContent,'Connected');assert.equal(h.get('copyHttp').disabled,true);
});
test('stale poll cannot resurrect an invitation after cancellation',async()=>{
 const h=await harness();await h.get('issue').onclick();
 const stale={...h.status}, pending=deferred();h.api.Status=()=>pending.promise;
 const poll=h.refresh();const cancelled=h.get('cancelInvitation').onclick();
 h.api.Status=async()=>({...h.status});pending.resolve(stale);
 await poll;await cancelled;
 assert.equal(h.get('invitation').value,'');assert.equal(h.get('issue').disabled,false);
});
test('poll started during invitation creation cannot erase the returned QR',async()=>{
 const h=await harness(), invitation=deferred(), pendingPoll=deferred(), stale={...h.status};
 h.api.IssueInvitation=()=>invitation.promise;
 const issuing=h.get('issue').onclick();
 h.api.Status=()=>pendingPoll.promise;
 const poll=h.refresh();
 Object.assign(h.status,{phase:'awaiting_phone',invitationExpiresAt:'2026-10-03T12:10:00Z'});
 invitation.resolve({bundle:'new-invitation',qrDataUrl:'data:image/png;base64,aA=='});
 await flush();assert.equal(h.get('invitation').value,'new-invitation');
 h.api.Status=async()=>({...h.status});pendingPoll.resolve(stale);
 await poll;await issuing;
 assert.equal(h.get('invitation').value,'new-invitation');
 assert.equal(h.get('invitationQR').src,'data:image/png;base64,aA==');
 assert.equal(visible(h,'invitationDetails'),true);assert.equal(h.get('issue').disabled,true);
});
test('invitation and stale poll resolving in the same tick preserve the new QR',async()=>{
 const h=await harness(), invitation=deferred(), pendingPoll=deferred(), stale={...h.status};
 h.api.IssueInvitation=()=>invitation.promise;
 const issuing=h.get('issue').onclick();
 h.api.Status=()=>pendingPoll.promise;
 const poll=h.refresh();
 Object.assign(h.status,{phase:'awaiting_phone',invitationExpiresAt:'2026-10-03T12:10:00Z'});
 h.api.Status=async()=>({...h.status});
 invitation.resolve({bundle:'same-tick-invitation',qrDataUrl:'data:image/png;base64,aA=='});
 pendingPoll.resolve(stale);
 await poll;await issuing;
 assert.equal(h.get('invitation').value,'same-tick-invitation');
 assert.equal(h.get('invitationQR').src,'data:image/png;base64,aA==');
 assert.equal(visible(h,'invitationDetails'),true);assert.equal(h.get('issue').disabled,true);
});
test('removing a phone requires explicit confirmation and preserves copy and update operations',async()=>{
 const h=await harness({status:{paired:true}});
 await h.get('copyHttp').onclick();await h.get('copySocks').onclick();await h.get('managePhone').onclick();
 assert.equal(visible(h,'phoneSettingsPanel'),true);assert.equal(visible(h,'removePhone'),true);
 await h.get('copyUpdate').onclick();await h.get('revoke').onclick();
 assert.equal(h.calls.some(c=>c[0]==='revoke'),false);await h.get('confirmRevoke').onclick();
 assert.deepEqual(operations(h),[['proxy','http'],['proxy','socks'],['copy-update'],['revoke']]);
 assert.equal(visible(h,'pairPanel'),true);assert.equal(visible(h,'phoneSettingsPanel'),false);
 assert.equal(h.get('issue').disabled,false);assert.equal(h.calls.some(c=>c[0]==='issue'),false);
});

for (const transport of ['hosted','direct']) test(`${transport} connected dashboard contains status and proxies, not setup`,async()=>{
 const h=await harness({status:{transport,activationState:'authorized',gatewayState:'connected',paired:true,connected:true}});
 assert.equal(visible(h,'dashboardHeader'),true);assert.equal(visible(h,'connectionSummary'),true);assert.equal(visible(h,'proxyPanel'),true);
 for(const id of ['wizardHeader','hostedPanel','addressPanel','transportChoice','pairPanel','verifyPanel','recoveryPanel','phoneSettingsPanel','dashboardNotice'])assert.equal(visible(h,id),false,id);
 assert.equal(h.get('connectionDetails').open,false);assert.equal(h.get('copyHttp').disabled,false);
 await h.get('managePhone').onclick();assert.equal(visible(h,'phoneSettingsHeader'),true);assert.equal(visible(h,'phoneSettingsPanel'),true);
 assert.equal(visible(h,'proxyPanel'),false);assert.equal(visible(h,'pairPanel'),false);
 await h.get('backToDashboard').onclick();assert.equal(visible(h,'proxyPanel'),true);assert.equal(visible(h,'phoneSettingsPanel'),false);
 assert.deepEqual(operations(h),[]);
});

test('offline dashboard tells the owner what to do on the phone without restarting setup',async()=>{
 const h=await harness({status:{paired:true,phase:'awaiting_phone'}});
 assert.equal(visible(h,'dashboardNotice'),true);assert.equal(visible(h,'noticeAction'),false);
 assert.match(h.get('noticeMessage').textContent,/Start cellular Agent.*Start sharing/);
 assert.match(h.get('noticeMessage').textContent,/iPhone.*open.*unlocked/);
 assert.equal(visible(h,'pairPanel'),false);assert.equal(visible(h,'proxyPanel'),true);
 h.status.connected=true;await h.refresh();assert.equal(visible(h,'dashboardNotice'),false);
 assert.equal(visible(h,'dashboardHeader'),true);assert.deepEqual(operations(h),[]);
});

test('dashboard activation notice takes priority over pairing and pending phone update',async()=>{
 const h=await harness({status:{transport:'hosted',paired:true,activationState:'access_rejected',gatewayState:'authorization_rejected',updatePending:true}});
 assert.equal(visible(h,'dashboardNotice'),true);assert.match(h.get('noticeHeading').textContent,/Inevitable/);
 assert.equal(visible(h,'recoveryPanel'),false);assert.equal(visible(h,'hostedPanel'),false);
 await h.get('noticeAction').onclick();assert.equal(visible(h,'hostedPanel'),true);
 assert.equal(h.get('stepTitle').textContent,'Activate Inevitable');assert.equal(h.status.paired,true);assert.deepEqual(operations(h),[]);
});

test('Finish later gives a resume action that preserves the current invitation',async()=>{
 const h=await harness();await h.get('networkNext').onclick();await h.get('issue').onclick();
 await h.get('finishLater').onclick();assert.equal(visible(h,'pairPanel'),false);assert.equal(visible(h,'dashboardNotice'),true);
 await h.get('noticeAction').onclick();assert.equal(visible(h,'pairPanel'),true);assert.equal(h.get('invitation').value,'invitation');
 assert.equal(h.calls.filter(c=>c[0]==='issue').length,1);
});

test('leaving a QR view clears its instruction without deleting the saved bundle',async()=>{
 const h=await harness();await h.get('networkNext').onclick();await h.get('issue').onclick();
 assert.match(h.get('feedback').textContent,/Scan/);await h.get('finishLater').onclick();
 assert.equal(h.get('feedback').textContent,'');assert.equal(h.get('invitation').value,'invitation');
 const paired=await harness({status:{paired:true,connected:true}});
 await paired.get('managePhone').onclick();await paired.get('exportUpdate').onclick();
 assert.match(paired.get('feedback').textContent,/Scan/);await paired.get('backToDashboard').onclick();
 assert.equal(paired.get('feedback').textContent,'');assert.equal(paired.get('update').value,'signed-update');
 paired.get('feedback').textContent='Proxy line copied.';await paired.get('managePhone').onclick();await paired.get('backToDashboard').onclick();
 assert.equal(paired.get('feedback').textContent,'Proxy line copied.');
});

test('dashboard uses the saved transport after abandoning a mode change',async()=>{
 const h=await harness({status:{transport:'hosted',activationState:'access_rejected',paired:true}});
 await h.get('reviewSetup').onclick();await h.get('advancedDirect').onclick();edit(h,'endpoint','unsaved.example');
 await h.get('finishLater').onclick();await h.refresh();
 assert.equal(visible(h,'addressPanel'),false);assert.match(h.get('noticeHeading').textContent,/Inevitable/);
 await h.get('noticeAction').onclick();assert.equal(visible(h,'hostedPanel'),true);assert.deepEqual(operations(h),[]);
});

test('service loss suppresses stale dashboard guidance and gateway health',async()=>{
 const h=await harness({status:{transport:'hosted',activationState:'authorized',gatewayState:'connected',paired:true}});
 h.api.Status=async()=>{throw new Error('offline');};await h.refresh();
 assert.equal(visible(h,'servicePanel'),true);assert.equal(visible(h,'dashboardNotice'),false);
 assert.equal(visible(h,'hostedPanel'),false);assert.equal(h.get('copyHttp').disabled,true);
 assert.doesNotMatch(h.get('listeningState').textContent,/connected/);
});

test('fresh hosted setup activates without address lookup or firewall',async()=>{
 const h=await harness({status:{transport:'hosted',endpoint:'',bindAddress:'',displayName:'',phase:'waiting',generation:0},api:{StartHostedActivation:async name=>{h.calls.push(['activate',name]);h.status.activationState='pending';return {state:'pending'};}}});
 assert.equal(h.get('stepTitle').textContent,'Activate Inevitable');
 assert.equal(h.calls.some(c=>c[0]==='discover'||c[0]==='check-firewall'),false);
 assert.equal(visible(h,'hostedPanel'),true);assert.equal(visible(h,'addressPanel'),false);
 await h.get('activateHosted').onclick();
 assert.deepEqual(operations(h),[['activate','My computer']]);
});

test('hosted gateway attachment never verifies a phone connection',async()=>{
 const h=await harness({status:{transport:'hosted',bindAddress:'',endpoint:'https://route.example',gatewayState:'connected',activationState:'authorized',paired:true,connected:false}});
 assert.equal(h.get('connectedState').textContent,'Waiting');assert.equal(h.get('listeningState').textContent,'Gateway connected');
 assert.equal(h.get('verifyNext').disabled,true);assert.equal(h.calls.some(c=>c[0]==='check-firewall'),false);
 await h.get('reviewSetup').onclick();await h.get('advancedDirect').onclick();
 assert.equal(visible(h,'addressPanel'),true);assert.equal(h.get('stepTitle').textContent,'Computer address');
});

test('hosted access rejection directs reactivation and preserves phone pairing',async()=>{
 const h=await harness({status:{transport:'hosted',endpoint:'https://route.example',bindAddress:'',paired:true,connected:false,gatewayState:'authorization_rejected',activationState:'access_rejected'}});
 assert.match(h.get('activationMessage').textContent,/account access or reactivate/);
 assert.match(h.get('gatewayMessage').textContent,/pairing is preserved/);
 assert.doesNotMatch(h.get('gatewayMessage').textContent,/Internet connection/);
 assert.equal(h.status.paired,true);assert.equal(h.get('verifyNext').disabled,true);
});

test('browser approval advances fresh hosted setup without verifying the phone',async()=>{
 const h=await harness({status:{transport:'hosted',endpoint:'',bindAddress:'',phase:'waiting',activationState:'pending'}});
 Object.assign(h.status,{endpoint:'https://route.example',activationState:'authorized',gatewayState:'connected',phase:'awaiting_phone'});
 await h.refresh();
 assert.equal(h.get('stepTitle').textContent,'Gateway connection');
 assert.equal(visible(h,'gatewayPanel'),true);assert.equal(h.get('verifyNext').disabled,true);
 await h.get('gatewayNext').onclick();
 assert.equal(h.get('stepTitle').textContent,'Pair phone');
 assert.deepEqual(operations(h),[]);
});

test('approval completed before the first post-action poll advances without a second click',async()=>{
 const h=await harness({status:{transport:'hosted',endpoint:'',bindAddress:'',phase:'waiting',activationState:'inactive'},api:{StartHostedActivation:async()=>{
  h.calls.push(['activate']);
  Object.assign(h.status,{endpoint:'https://route.example',activationState:'authorized',gatewayState:'connected',phase:'awaiting_phone'});
  return {state:'pending'};
 }}});
 await h.get('activateHosted').onclick();
 assert.equal(h.get('stepTitle').textContent,'Gateway connection');
 assert.equal(h.get('verifyNext').disabled,true);assert.deepEqual(operations(h),[['activate']]);
});

test('an authorization snapshot before endpoint readiness still advances when configuration finishes',async()=>{
 const h=await harness({status:{transport:'hosted',endpoint:'',bindAddress:'',phase:'waiting',activationState:'pending'}});
 Object.assign(h.status,{activationState:'authorized'});await h.refresh();
 assert.equal(h.get('stepTitle').textContent,'Activate Inevitable');
 Object.assign(h.status,{endpoint:'https://route.example',gatewayState:'connected',phase:'awaiting_phone'});await h.refresh();
 assert.equal(h.get('stepTitle').textContent,'Gateway connection');assert.equal(h.get('verifyNext').disabled,true);
 assert.deepEqual(operations(h),[]);
});

test('reviewing approved hosted setup continues without replacing activation',async()=>{
 const h=await harness({status:{transport:'hosted',endpoint:'https://route.example',bindAddress:'',activationState:'authorized',paired:true},api:{StartHostedActivation:async()=>{h.calls.push(['activate']);}}});
 await h.get('reviewSetup').onclick();await h.refresh();
 assert.equal(h.get('stepTitle').textContent,'Activate Inevitable');
 assert.equal(h.get('activateHosted').textContent,'Continue to gateway connection');
 await h.get('activateHosted').onclick();
 assert.equal(h.get('stepTitle').textContent,'Gateway connection');
 assert.deepEqual(operations(h),[]);assert.equal(h.status.paired,true);
});

test('approval after Finish later keeps the dashboard visible',async()=>{
 const h=await harness({status:{transport:'hosted',endpoint:'',bindAddress:'',phase:'waiting',activationState:'pending'}});
 await h.get('finishLater').onclick();
 Object.assign(h.status,{endpoint:'https://route.example',activationState:'authorized',phase:'awaiting_phone'});
 await h.refresh();
 assert.equal(visible(h,'dashboardHeader'),true);assert.equal(visible(h,'wizardHeader'),false);
 assert.deepEqual(operations(h),[]);
});

test('late approval does not navigate away from an explicitly selected direct form',async()=>{
 const h=await harness({status:{transport:'hosted',endpoint:'',bindAddress:'',phase:'waiting',activationState:'pending'}});
 await h.get('advancedDirect').onclick();edit(h,'endpoint','direct.example');
 Object.assign(h.status,{endpoint:'https://route.example',activationState:'authorized',phase:'awaiting_phone'});
 await h.refresh();
 assert.equal(h.get('stepTitle').textContent,'Computer address');assert.equal(h.get('endpoint').value,'direct.example');
 assert.equal(visible(h,'addressPanel'),true);assert.deepEqual(operations(h),[]);
});
