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
  const makeNode=(tag='div',initialClasses='')=>{
    const classes=new Set(initialClasses.split(/\s+/).filter(Boolean));
    return {tagName:tag.toUpperCase(),value:'',textContent:'',src:'',disabled:false,className:'',hidden:false,dataset:{},children:[],
      classList:{toggle(name,on){if(on===undefined)on=!classes.has(name);if(on)classes.add(name);else classes.delete(name);},add(name){classes.add(name);},remove(name){classes.delete(name);},contains(name){return classes.has(name);}},
      append(...nodes){this.children.push(...nodes);},replaceChildren(...nodes){this.children=[...nodes];},
      setAttribute(name,value){this[name]=value;},removeAttribute(name){this[name]='';},focus(){context.document.activeElement=this;}};
  };
  const get=id=>{
    assert.ok(markup.has(id),'Missing HTML element: '+id);
    if (!elements.has(id)) {
      elements.set(id,makeNode(markup.get(id).match(/^<(\w+)/)[1],markup.get(id).match(/class="([^"]*)"/)?.[1] || ''));
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
  const context=vm.createContext({document:{getElementById:get,createElement:makeNode,activeElement:null},go:{clientapp:{App:api}},setInterval(fn,delay){timers.push({fn,delay,at:now+delay,interval:true});},setTimeout(fn,delay){timers.push({fn,delay,at:now+delay});},Date:Clock,URL,console});
  context.window=context;
  vm.runInContext(fs.readFileSync(new URL('./app.js',import.meta.url),'utf8'),context);
  await flush();
  return {get,calls,status,context,api,refresh:async()=>{await vm.runInContext('refresh()',context);await flush();},advance:async(ms)=>{now+=ms;for(const timer of [...timers])if(timer.at<=now){timer.at=timer.interval?now+timer.delay:Infinity;await timer.fn();}await flush();}};
}
const visible=(h,id)=>!h.get(id).classList.contains('hidden');
const edit=(h,id,value)=>{h.get(id).value=value;h.get(id).oninput();};
const operations=h=>h.calls.filter(c=>!['check-firewall','discover'].includes(c[0]));

const phoneFixture=(phoneId,name,slot,extra={})=>({phoneId,name,slot,paired:true,connected:false,updatePending:false,phase:'ready',message:'Waiting for phone',socksAddress:`127.0.0.1:${1080+2*slot}`,httpAddress:`127.0.0.1:${1081+2*slot}`,proxyRunning:true,...extra});
async function multiHarness(options={}) {
 const phones={maxPhones:10,phones:[phoneFixture('phone-a','Home',0,{connected:true,phase:'connected'}),phoneFixture('phone-b','Travel',1,{updatePending:true})],...options.phones};
 const calls=[];
 const h=await harness({status:{paired:true,connected:true,...options.status},api:{
  Phones:async()=>JSON.parse(JSON.stringify(phones)),
  CopyPhoneProxy:async(id,kind)=>calls.push(['phone-proxy',id,kind]),
  RenamePhone:async(id,name)=>{calls.push(['rename-phone',id,name]);phones.phones.find(p=>p.phoneId===id).name=name;},
  RetryPhoneProxy:async id=>calls.push(['retry-phone-proxy',id]),
  RevokePhone:async id=>{calls.push(['revoke-phone',id]);phones.phones=phones.phones.filter(p=>p.phoneId!==id);},
  ExportPhoneEndpointUpdate:async id=>({bundle:'update-'+id,qrDataUrl:'data:image/png;base64,aA=='}),
  CopyPhoneEndpointUpdate:async id=>calls.push(['copy-phone-update',id]),
  AddPhone:async name=>{calls.push(['add-phone',name]);phones.pendingPhoneId='phone-c';phones.phones.push(phoneFixture('phone-c',name||'Phone 3',2,{paired:false,phase:'awaiting_phone',invitationExpiresAt:'2026-10-03T12:10:00Z'}));return {phoneId:'phone-c',bundle:'invitation-c',qrDataUrl:'data:image/png;base64,aA=='};},
  CancelPhoneInvitation:async id=>{calls.push(['cancel-phone-invitation',id]);phones.phones=phones.phones.filter(p=>p.phoneId!==id);delete phones.pendingPhoneId;},
  ...options.api
 }});h.phones=phones;h.phoneCalls=calls;return h;
}
const row=(h,id)=>h.get('phoneList').children.find(p=>p.dataset.phoneId===id);
const descendants=node=>node.children.flatMap(child=>[child,...descendants(child)]);
const rowButton=(h,id,text)=>descendants(row(h,id)).find(n=>n.tagName==='BUTTON'&&n.textContent===text);

test('app runtime explains close quit sign-out and minimized traffic lifetime',async()=>{
 const h=await harness({api:{SetupInfo:async()=>({platform:'darwin',runtimeMode:'app'})}});
 assert.equal(visible(h,'runtimeLifetime'),true);
 assert.match(h.get('runtimeLifetime').textContent,/close.*Quit.*sign out/i);
 assert.match(h.get('runtimeLifetime').textContent,/minimized/i);
 const service=await harness({api:{SetupInfo:async()=>({platform:'darwin',runtimeMode:'service'})}});
 assert.equal(visible(service,'runtimeLifetime'),false);
});

test('app direct firewall retry gives manual policy guidance without service wording',async()=>{
 const h=await harness({api:{SetupInfo:async()=>({platform:'darwin',runtimeMode:'app'}),CheckFirewall:async()=>({state:'manual',scope:'application',port:8443,message:'Allow Inevitable Mobile Relay in System Settings > Network > Firewall if local policy requires it.'})}});
 await h.get('checkFirewall').onclick();
 assert.match(h.get('firewallState').textContent,/manual/i);
 assert.match(h.get('firewallMessage').textContent,/this app/i);
 assert.doesNotMatch(h.get('firewallMessage').textContent,/service application/i);
 assert.equal(h.get('retryFirewall').textContent,'Review firewall policy');
});

test('app locked protected state explains Keychain recovery and keeps traffic unavailable',async()=>{
 const h=await harness({api:{SetupInfo:async()=>({platform:'darwin',runtimeMode:'app'}),Phones:async()=>{throw new Error('Protected Client state is unavailable. Repair the installation.');}}});
 assert.match(h.get('serviceMessage').textContent,/Keychain Access/);
 assert.doesNotMatch(h.get('serviceMessage').textContent,/Repair the installation/);
 assert.equal(h.get('copyHttp').disabled,true);
});

test('phone list exposes separate addresses and copies the chosen stable ID',async()=>{
 const h=await multiHarness();assert.equal(visible(h,'phonesPanel'),true);
 assert.equal(h.get('phoneList').children.length,2);
 assert.match(descendants(row(h,'phone-b')).map(n=>n.textContent).join(' '),/Travel.*1083/s);
 await rowButton(h,'phone-b','Copy HTTP proxy').onclick();await rowButton(h,'phone-a','Copy SOCKS5 proxy').onclick();
 assert.deepEqual(h.phoneCalls,[['phone-proxy','phone-b','http'],['phone-proxy','phone-a','socks']]);
 assert.deepEqual(operations(h),[]);
});
test('selected offline phone never inherits aggregate connection success',async()=>{
 const h=await multiHarness();await rowButton(h,'phone-b','Phone settings').onclick();
 assert.notEqual(h.get('phase').textContent,'Connected');assert.equal(h.get('verifyNext').disabled,true);
 assert.equal(h.get('phoneRename').value,'Travel');assert.equal(h.get('recoveryPanel').open,true);
 await h.get('copyUpdate').onclick();assert.deepEqual(h.phoneCalls,[['copy-phone-update','phone-b']]);
});
test('add phone reuses pairing without computer activation or sibling readiness',async()=>{
 const h=await multiHarness();await h.get('addPhone').onclick();
 assert.equal(visible(h,'pairPanel'),true);assert.equal(visible(h,'hostedPanel'),false);
 h.get('phoneName').value='Road';await h.get('issue').onclick();
 assert.equal(h.get('invitation').value,'invitation-c');assert.equal(visible(h,'proxyPanel'),false);assert.equal(h.get('verifyNext').disabled,true);
 assert.deepEqual(h.phoneCalls,[['add-phone','Road']]);assert.deepEqual(operations(h),[]);
 await h.get('cancelInvitation').onclick();assert.equal(visible(h,'dashboardHeader'),true);
 assert.equal(h.phones.phones.length,2);assert.deepEqual(h.phoneCalls.at(-1),['cancel-phone-invitation','phone-c']);
});
test('ten slots disable add and a pending invitation resumes without a duplicate',async()=>{
 const h=await multiHarness({phones:{phones:Array.from({length:10},(_,i)=>phoneFixture('phone-'+i,'Phone '+(i+1),i))}});
 assert.equal(h.get('addPhone').disabled,true);
 const p=await multiHarness({phones:{pendingPhoneId:'phone-c',phones:[phoneFixture('phone-a','Home',0),phoneFixture('phone-c','Pending',1,{paired:false,phase:'awaiting_phone',invitationExpiresAt:'2026-10-03T12:10:00Z'})]},api:{AddPhone:async()=>({phoneId:'phone-c',bundle:'resumed',qrDataUrl:'data:image/png;base64,aA=='})}});
 await p.get('addPhone').onclick();await p.get('issue').onclick();assert.equal(p.get('invitation').value,'resumed');assert.equal(p.phones.phones.length,2);
});
test('phone settings rename retry and confirmed removal affect only selected phone',async()=>{
 const h=await multiHarness();await rowButton(h,'phone-b','Phone settings').onclick();
 h.get('phoneRename').value='Travel renamed';await h.get('renamePhone').onclick();assert.equal(h.phones.phones[1].name,'Travel renamed');
 await h.get('retryPhoneProxy').onclick();await h.get('revoke').onclick();assert.equal(h.phones.phones.length,2);
 await h.get('confirmRevoke').onclick();assert.equal(h.phones.phones.length,1);assert.equal(h.phones.phones[0].phoneId,'phone-a');assert.equal(visible(h,'dashboardHeader'),true);
 assert.deepEqual(h.phoneCalls,[['rename-phone','phone-b','Travel renamed'],['retry-phone-proxy','phone-b'],['revoke-phone','phone-b']]);assert.deepEqual(operations(h),[]);
});
test('late connection update cannot appear under a newly selected phone',async()=>{
 const wait=deferred();const h=await multiHarness({api:{ExportPhoneEndpointUpdate:()=>wait.promise}});
 await rowButton(h,'phone-b','Phone settings').onclick();const request=h.get('exportUpdate').onclick();
 // Exercise selection change outside disabled buttons as a future native event can.
 vm.runInContext("selectPhone('phone-a',true)",h.context);
 wait.resolve({bundle:'secret-for-b',qrDataUrl:'data:image/png;base64,aA=='});await request;
 assert.equal(h.get('update').value,'');assert.equal(visible(h,'updateDetails'),false);
});
test('phone-list failure disables phone actions without legacy fallback',async()=>{
 const h=await multiHarness({api:{Phones:async()=>{throw new Error('Protected state unavailable');}}});
 assert.equal(h.get('addPhone').disabled,true);assert.equal(h.get('copyHttp').disabled,true);
 assert.match(h.get('serviceMessage').textContent,/Protected state unavailable/);assert.deepEqual(operations(h),[]);
});
test('phone list polling preserves keyboard focus on the same action',async()=>{
 const h=await multiHarness();const button=rowButton(h,'phone-b','Copy HTTP proxy');button.focus();
 await h.refresh();assert.equal(h.context.document.activeElement,rowButton(h,'phone-b','Copy HTTP proxy'));
});
test('first phone setup keeps account steps and completes only for that phone',async()=>{
 const h=await multiHarness({status:{transport:'hosted',endpoint:'',paired:false,connected:false,phase:'waiting',activationState:'inactive'},phones:{phones:[]}});
 assert.equal(visible(h,'hostedPanel'),true);assert.equal(visible(h,'dashboardHeader'),false);
 Object.assign(h.status,{endpoint:'https://client.example',running:true,activationState:'authorized',gatewayState:'connected'});await h.refresh();
 await h.get('gatewayNext').onclick();await h.get('issue').onclick();assert.deepEqual(h.phoneCalls,[['add-phone','']]);
 Object.assign(h.phones.phones[0],{paired:true,connected:false,phase:'ready',invitationExpiresAt:undefined});await h.refresh();
 assert.equal(visible(h,'verifyPanel'),true);assert.equal(h.get('verifyNext').disabled,true);
 Object.assign(h.phones.phones[0],{connected:true,phase:'connected'});await h.refresh();assert.equal(visible(h,'proxyPanel'),true);
});
test('a blocked phone proxy never hides a usable sibling or claims success',async()=>{
 const h=await multiHarness({phones:{phones:[phoneFixture('phone-a','Home',0,{connected:true,phase:'connected'}),phoneFixture('phone-b','Travel',1,{connected:true,proxyRunning:false,phase:'error',message:'HTTP port 1083 is occupied.'})]}});
 assert.match(descendants(row(h,'phone-a')).map(n=>n.textContent).join(' '),/Connected/);
 await rowButton(h,'phone-b','Phone settings').onclick();assert.equal(h.get('verifyNext').disabled,true);assert.match(h.get('phoneProxyMessage').textContent,/1083/);
});
test('removal failure retains the selected phone and its retry confirmation',async()=>{
 const h=await multiHarness({api:{RevokePhone:async()=>{throw new Error('Removal could not be saved. Restarting may restore access.');}}});
 await rowButton(h,'phone-b','Phone settings').onclick();await h.get('revoke').onclick();await h.get('confirmRevoke').onclick();
 assert.equal(h.phones.phones.length,2);assert.equal(visible(h,'phoneSettingsPanel'),true);assert.equal(visible(h,'revokeConfirmation'),true);
 assert.match(h.get('feedback').textContent,/could not be saved.*Restarting/);assert.deepEqual(operations(h),[]);
});
test('late add invitation cannot replace a newly selected phone view',async()=>{
 const wait=deferred(),h=await multiHarness({api:{AddPhone:()=>wait.promise}});await h.get('addPhone').onclick();const request=h.get('issue').onclick();
 vm.runInContext("selectPhone('phone-a',true)",h.context);wait.resolve({phoneId:'phone-c',bundle:'private-code-c',qrDataUrl:'data:image/png;base64,aA=='});await request;
 assert.equal(h.get('invitation').value,'');assert.equal(visible(h,'invitationDetails'),false);assert.equal(h.get('feedback').textContent,'');
});
test('shared fatal service failure stays visible for a selected healthy phone',async()=>{
 const h=await multiHarness({status:{phase:'error',running:false,message:'The authenticated listener could not start.'}});
 assert.notEqual(h.get('phase').textContent,'Connected');assert.match(h.get('message').textContent,/Connection details/);
 assert.equal(h.get('diagnosticMessage').textContent,'The authenticated listener could not start.');assert.equal(h.get('verifyNext').disabled,true);
});
test('dashboard return cannot resurrect healthy phone state after a failed poll',async()=>{
 const h=await multiHarness();await rowButton(h,'phone-a','Phone settings').onclick();
 h.api.Phones=async()=>{throw new Error('Protected state unavailable');};await h.refresh();
 await h.get('backToDashboard').onclick();assert.equal(visible(h,'servicePanel'),true);assert.equal(h.get('addPhone').disabled,true);
 assert.equal(rowButton(h,'phone-a','Copy HTTP proxy').disabled,true);assert.notEqual(h.get('phase').textContent,'Connected');
});

test('routine dashboard translates service language and keeps details optional',async()=>{
 const h=await harness({status:{transport:'hosted',activationState:'authorized',gatewayState:'connected',paired:true,message:'Awaiting authenticated Agent transport session.'}});
 assert.doesNotMatch(h.get('message').textContent,/authenticated|Agent|transport|session|gateway/i);
 assert.equal(h.get('diagnosticMessage').textContent,h.status.message);
 assert.equal(h.get('connectionDetails').open,false);assert.equal(h.get('phase').textContent,'Waiting for phone');
 assert.equal(h.get('verifyNext').disabled,true);
});

test('reviewing an approved account does not present activation or renaming as unfinished',async()=>{
 const h=await harness({status:{transport:'hosted',activationState:'authorized',gatewayState:'connected',paired:true}});
 await h.get('reviewSetup').onclick();
 assert.equal(visible(h,'hostedNameField'),false);assert.match(h.get('hostedHeading').textContent,/account.*connected/i);
 assert.match(h.get('hostedDescription').textContent,/already approved/i);
 assert.equal(visible(h,'connectionSummary'),false);
 await h.get('activateHosted').onclick();assert.equal(visible(h,'gatewayPanel'),true);assert.deepEqual(operations(h),[]);
});

test('unknown connection states do not display raw enums or claim sharing',async()=>{
 const h=await harness({status:{transport:'hosted',activationState:'authorized',gatewayState:'future_internal_state',phase:'future_phase',paired:true,message:'Opaque internal state.'}});
 for(const id of ['message','gatewayMessage','listeningState','noticeMessage'])assert.doesNotMatch(h.get(id).textContent,/future_internal_state|Opaque internal/);
 assert.equal(h.get('phase').textContent,'Checking connection');assert.equal(h.get('verifyNext').disabled,true);
 assert.match(h.get('listeningState').textContent,/checking/i);
});

for(const message of ['HTTP proxy port 1081 is occupied. Close the application using it, then restart the Client.','Access is disabled because revocation could not be saved. Repair protected storage.'])test(`actionable error details are retained: ${message.split('.')[0]}`,async()=>{
 const h=await harness({status:{paired:true,phase:'error',running:false,message}});
 assert.match(h.get('message').textContent,/Connection details/);assert.equal(h.get('diagnosticMessage').textContent,message);
 assert.equal(h.get('connectionDetails').open,true);assert.equal(h.get('verifyNext').disabled,true);
 assert.doesNotMatch(h.get('noticeMessage').textContent,/Internet connection/);
 h.get('connectionDetails').open=false;await h.refresh();assert.equal(h.get('connectionDetails').open,false);
});

test('pending phone updates stay incomplete even when a session is already open',async()=>{
 const h=await harness({status:{transport:'hosted',activationState:'authorized',gatewayState:'connected',paired:true,connected:true,updatePending:true}});
 assert.notEqual(h.get('phase').textContent,'Connected');assert.match(h.get('message').textContent,/update|Reconnect/i);
 assert.equal(h.get('verifyNext').disabled,true);assert.equal(visible(h,'recoveryPanel'),true);
 h.status.updatePending=false;await h.refresh();assert.equal(h.get('phase').textContent,'Connected');
 assert.match(h.get('message').textContent,/phone.*mobile data/i);
});

test('proxy instructions explain application setup without claiming to route the whole computer',async()=>{
 const h=await harness({status:{paired:true,connected:true}});
 assert.match(h.get('proxyConnection').textContent,/app.*proxy settings/i);
 await h.get('copyHttp').onclick();assert.match(h.get('feedback').textContent,/app.*proxy settings/i);
 await h.get('copySocks').onclick();assert.match(h.get('feedback').textContent,/app.*proxy settings/i);
 assert.deepEqual(operations(h),[['proxy','http'],['proxy','socks']]);
});

test('hosted setup errors take priority over a saved gateway connection or retry message',async()=>{
 const h=await harness({status:{transport:'hosted',activationState:'authorized',gatewayState:'connected',paired:true,connected:true,phase:'error',message:'Access is disabled because revocation could not be saved. Repair protected storage.'}});
 await h.get('reviewSetup').onclick();await h.get('stepNetwork').onclick();
 assert.equal(visible(h,'connectionSummary'),true);assert.match(h.get('gatewayMessage').textContent,/Connection details/);
 assert.doesNotMatch(h.get('gatewayMessage').textContent,/start sharing|retry automatically/i);assert.notEqual(h.get('phase').textContent,'Connected');
 h.status.gatewayState='disconnected';await h.refresh();assert.match(h.get('gatewayMessage').textContent,/Connection details/);
 assert.equal(h.get('verifyNext').disabled,true);
});

test('completed browser approval removes its obsolete instruction without clearing other feedback',async()=>{
 const h=await harness({status:{transport:'hosted',endpoint:'',phase:'waiting',activationState:'inactive'},api:{StartHostedActivation:async()=>{Object.assign(h.status,{endpoint:'https://route.example',activationState:'authorized',gatewayState:'connected'});}}});
 await h.get('activateHosted').onclick();assert.equal(h.get('feedback').textContent,'');
 assert.equal(visible(h,'hostedApprovalHelp'),false);
 h.get('feedback').textContent='An unrelated action failed.';await h.refresh();assert.equal(h.get('feedback').textContent,'An unrelated action failed.');
});

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
 assert.equal(h.get('stepTitle').textContent,'Connect phone');assert.equal(h.calls.some(c=>c[0]==='issue'),false);
 await h.get('issue').onclick();await h.refresh();await h.refresh();assert.equal(h.get('invitation').value,'invitation');
 assert.equal(h.calls.filter(c=>c[0]==='issue').length,1);assert.equal(h.get('issue').disabled,true);
});
test('lost pairing acknowledgement resumes verification without replacing invitation',async()=>{
 const h=await harness({status:{phase:'acknowledging',invitationExpiresAt:'2026-10-03T12:10:00Z'}});
 assert.equal(h.get('stepTitle').textContent,'Start sharing');assert.equal(h.calls.some(c=>c[0]==='issue'),false);
 assert.match(h.get('verificationMessage').textContent,/finishes|finishing/i);assert.equal(h.get('verifyNext').disabled,true);
 assert.equal(visible(h,'phoneStartInstructions'),false);assert.equal(visible(h,'cancelPairing'),true);
});
test('verification requires connected status even if paired or phase says ready',async()=>{
 const h=await harness();await h.get('networkNext').onclick();await h.get('issue').onclick();
 Object.assign(h.status,{paired:true,phase:'ready',connected:false});await h.refresh();
 assert.equal(h.get('stepTitle').textContent,'Start sharing');assert.equal(h.get('verifyNext').disabled,true);
 assert.equal(h.get('verifyHeading').textContent,'Start sharing on your phone');
 assert.equal(visible(h,'phoneStartInstructions'),true);assert.equal(visible(h,'verifyNext'),false);
 assert.match(h.get('verificationMessage').textContent,/automatically/);assert.equal(h.get('feedback').textContent,'');
 Object.assign(h.status,{connected:true});await h.refresh();
 assert.equal(h.get('stepTitle').textContent,'Use in your apps');assert.equal(h.get('connectedState').textContent,'Connected');
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
 assert.equal(h.get('stepTitle').textContent,'Use in your apps');assert.equal(h.get('update').value,'');
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
 assert.match(h.get('pending').textContent,/Approve.*browser/i);assert.match(h.get('recoveryMessage').textContent,/Approve.*browser/i);
 assert.equal(h.get('activateHosted').textContent,'Reconnect in browser');
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
 assert.equal(h.get('stepTitle').textContent,'Your account');assert.equal(h.status.paired,true);assert.deepEqual(operations(h),[]);
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
 assert.equal(h.get('stepTitle').textContent,'Your account');
 assert.equal(h.calls.some(c=>c[0]==='discover'||c[0]==='check-firewall'),false);
 assert.equal(visible(h,'hostedPanel'),true);assert.equal(visible(h,'addressPanel'),false);
 await h.get('activateHosted').onclick();
 assert.deepEqual(operations(h),[['activate','My computer']]);
});

test('hosted gateway attachment never verifies a phone connection',async()=>{
 const h=await harness({status:{transport:'hosted',bindAddress:'',endpoint:'https://route.example',gatewayState:'connected',activationState:'authorized',paired:true,connected:false}});
 assert.equal(h.get('connectedState').textContent,'Waiting');assert.equal(h.get('listeningState').textContent,'Computer connected');
 assert.equal(h.get('verifyNext').disabled,true);assert.equal(h.calls.some(c=>c[0]==='check-firewall'),false);
 await h.get('reviewSetup').onclick();await h.get('advancedDirect').onclick();
 assert.equal(visible(h,'addressPanel'),true);assert.equal(h.get('stepTitle').textContent,'Computer address');
});

test('hosted access rejection directs reactivation and preserves phone pairing',async()=>{
 const h=await harness({status:{transport:'hosted',endpoint:'https://route.example',bindAddress:'',paired:true,connected:false,gatewayState:'authorization_rejected',activationState:'access_rejected'}});
 assert.match(h.get('activationMessage').textContent,/account access.*approve/);
 assert.match(h.get('gatewayMessage').textContent,/saved phone is kept/);
 assert.doesNotMatch(h.get('gatewayMessage').textContent,/Internet connection/);
 assert.equal(h.status.paired,true);assert.equal(h.get('verifyNext').disabled,true);
});

test('browser approval advances fresh hosted setup without verifying the phone',async()=>{
 const h=await harness({status:{transport:'hosted',endpoint:'',bindAddress:'',phase:'waiting',activationState:'pending'}});
 Object.assign(h.status,{endpoint:'https://route.example',activationState:'authorized',gatewayState:'connected',phase:'awaiting_phone'});
 await h.refresh();
 assert.equal(h.get('stepTitle').textContent,'Connect this computer');
 assert.equal(visible(h,'gatewayPanel'),true);assert.equal(h.get('verifyNext').disabled,true);
 await h.get('gatewayNext').onclick();
 assert.equal(h.get('stepTitle').textContent,'Connect phone');
 assert.deepEqual(operations(h),[]);
});

test('approval completed before the first post-action poll advances without a second click',async()=>{
 const h=await harness({status:{transport:'hosted',endpoint:'',bindAddress:'',phase:'waiting',activationState:'inactive'},api:{StartHostedActivation:async()=>{
  h.calls.push(['activate']);
  Object.assign(h.status,{endpoint:'https://route.example',activationState:'authorized',gatewayState:'connected',phase:'awaiting_phone'});
  return {state:'pending'};
 }}});
 await h.get('activateHosted').onclick();
 assert.equal(h.get('stepTitle').textContent,'Connect this computer');
 assert.equal(h.get('verifyNext').disabled,true);assert.deepEqual(operations(h),[['activate']]);
});

test('an authorization snapshot before endpoint readiness still advances when configuration finishes',async()=>{
 const h=await harness({status:{transport:'hosted',endpoint:'',bindAddress:'',phase:'waiting',activationState:'pending'}});
 Object.assign(h.status,{activationState:'authorized'});await h.refresh();
 assert.equal(h.get('stepTitle').textContent,'Your account');
 Object.assign(h.status,{endpoint:'https://route.example',gatewayState:'connected',phase:'awaiting_phone'});await h.refresh();
 assert.equal(h.get('stepTitle').textContent,'Connect this computer');assert.equal(h.get('verifyNext').disabled,true);
 assert.deepEqual(operations(h),[]);
});

test('reviewing approved hosted setup continues without replacing activation',async()=>{
 const h=await harness({status:{transport:'hosted',endpoint:'https://route.example',bindAddress:'',activationState:'authorized',paired:true},api:{StartHostedActivation:async()=>{h.calls.push(['activate']);}}});
 await h.get('reviewSetup').onclick();await h.refresh();
 assert.equal(h.get('stepTitle').textContent,'Your account');
 assert.equal(h.get('activateHosted').textContent,'Continue');
 await h.get('activateHosted').onclick();
 assert.equal(h.get('stepTitle').textContent,'Connect this computer');
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
