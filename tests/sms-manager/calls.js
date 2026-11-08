'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const root = path.join(__dirname,'../../packages/luci-app-sms-manager/htdocs/luci-static/resources');
let historyFail=false, historyBefore=[];
let current = {state:'idle',ready:true,peer:''}, closed = 0, dialed = [], releaseMic, claimed = 0, left = 0;
const contacts = [{name:'가족',addresses:[{address:'+821000000001',label:'휴대전화'},{address:'+8221234567',label:'집'}]}];
const api = {status:async()=>current,closeAudio:()=>closed++,openAudio:()=>new Promise(resolve=>releaseMic=resolve),
    claim:async()=>{claimed++;api.claimed=true;},lease:async()=>{},release:async()=>{api.claimed=false;},leave:()=>{left++;api.claimed=false;},
    dial:async(...args)=>{dialed.push(args);current={state:'dialing',ready:true,peer:args[0],owned:true};},answer:async()=>{},exchangeAudio:async()=>{}};
const messaging = vm.runInNewContext('(function(){'+fs.readFileSync(path.join(root,'messaging.js'),'utf8')+'})()', {baseclass:{extend:value=>value},rpc:{declare:spec=>{ assert.equal(spec.object,'modemconfig-ko');return async (...args)=>{if(spec.method==='snapshot') return {contacts,service:{slot:1}};if(spec.method==='call_history'){assert.deepEqual(Array.from(spec.params),['before']);assert.equal(typeof args[0],'number');historyBefore.push(args[0]);if(historyFail)throw new Error('history unavailable');return {calls:[],more:false};}throw new Error('unexpected method');};}}});
const model = vm.runInNewContext('(function(){'+fs.readFileSync(path.join(root,'calling_model.js'),'utf8')+'})()',{
    baseclass:{extend:value=>value},calling:{create:()=>api},messaging,window:{clearTimeout(){},setTimeout(){return 1;}}
});
(async()=>{
    const state=model.create();await model.refresh(state);assert.deepEqual(historyBefore,[0]);
    historyFail=true;current={state:'active',owned:true,occupied:true};await model.refresh(state);assert.equal(state.call.state,'active','history failure blocked live call status');historyFail=false;current={state:'idle',ready:true};await model.refresh(state);
    assert.equal(model.contactOptions(state,'가족').length,2,'multiple contact numbers were lost');
    assert.equal(model.contactOptions(state,'02-123-4567')[0].number,'+8221234567');
    assert.equal(model.contactOptions(state,'없는 사람').length,0);
    state.call={peer:'01000000001'};assert.equal(model.peerLabel(state),'가족 · 01000000001');
    state.call={peer:'+821099999999'};assert.equal(model.peerLabel(state),'+821099999999');
    const before=closed, call=model.dial(state,'+821000000001');
    assert(state.pending);await Promise.resolve();await model.refresh(state);assert.equal(closed,before,'status polling closed the pending microphone');
    assert.equal(claimed,1);assert.equal(dialed.length,0,'dial occurred before microphone readiness');
    await assert.rejects(model.dial(state,'+821000000002'),/연결 중/);
    let prevented=0;model.guardLeave(state,{preventDefault(){prevented++;}});assert.equal(prevented,1);
    releaseMic();await call;assert.equal(dialed.length,1);assert.equal(dialed[0][1],'browser');
    assert(!state.pending);
    current={state:'active',owned:true,occupied:true,duration:65};await model.refresh(state);
    let screen=model.presentation(state);assert(screen.active&&screen.connected&&!screen.idle);assert.equal(screen.duration,'01:05');
    current={state:'ringing',direction:'incoming',occupied:true,owned:false};await model.refresh(state);
    screen=model.presentation(state);assert(screen.incoming&&screen.locked);
    current={state:'ended'};await model.refresh(state);assert(closed>before);
    api.claimed=true;current={state:'failed',owned:false,occupied:false};await model.refresh(state);
    assert(!api.claimed,'failed call kept a stale microphone claim');
    const failedDialCount=dialed.length;await model.refresh(state);assert.equal(dialed.length,failedDialCount,'failed call redialed from status polling');
    state.route='bluetooth:headset';await model.dial(state,'+821000000001');
    assert.equal(dialed[1][1],'bluetooth');assert.equal(dialed[1][2],'headset');
    current={state:'ended'};await model.refresh(state);state.route='browser';
    releaseMic=null;const canceled=model.dial(state,'+821000000001');
    while(!releaseMic)await Promise.resolve();
    await Promise.resolve();
    const beforeCancel=closed;await model.hangup(state);assert(closed>beforeCancel,'hangup kept microphone open');
    releaseMic();await canceled;assert.equal(dialed.length,2,'canceled microphone request started a call');
    assert.equal(state.call.state,'ended');assert(!state.pending,'canceled microphone prompt kept UI busy');
    const claimNormal=api.claim;let finishClaim;api.claim=()=>new Promise(resolve=>{finishClaim=()=>{api.claimed=true;resolve();};});
    const acquiring=model.dial(state,'+821000000001');while(!finishClaim)await Promise.resolve();
    const ending=model.hangup(state);finishClaim();await ending;await acquiring;assert(!api.claimed,'late claim survived hangup');api.claim=claimNormal;
    const releaseNormal=api.release;state.call={state:'active'};state.pending=true;api.claimed=true;api.release=async()=>{throw new Error('release unavailable');};
    await assert.rejects(model.hangup(state),/release unavailable/);assert(!state.pending&&!state.canceling,'release failure left UI busy');assert.equal(state.call.state,'active','failed release reported a completed call');api.release=releaseNormal;await model.hangup(state);
    model.close(state);assert(state.closed);assert.equal(left,1,'pagehide did not send a hangup');
    await assert.rejects(model.dial(state,'+821000000001'),/화면을 벗어/);assert.equal(dialed.length,2,'closed page started a new call');
    let micReady=[],stopped=0;
    const liveCalling=vm.runInNewContext('(function(){'+fs.readFileSync(path.join(root,'calling.js'),'utf8')+'})()', {
        baseclass:{extend:value=>value},rpc:{declare:()=>async()=>({})},messaging,
        L:{Class:{extend:body=>{function API(){}API.prototype=body;return API;}}},
        window:{isSecureContext:true,crypto:{getRandomValues:bytes=>bytes.fill(1)},AudioContext:function(){throw new Error('canceled stream opened audio');}},
        navigator:{mediaDevices:{getUserMedia:()=>new Promise(resolve=>micReady.push(resolve))}}
    });
    const live=liveCalling.create(),audioPending=live.openAudio();live.closeAudio();
    micReady.shift()({getTracks:()=>[{stop(){stopped++;}}]});await assert.rejects(audioPending,/취소/);assert.equal(stopped,1,'late microphone stayed open after cancel');
    let processor,uploaded=[],constraintsSeen=[],audioTimers=[];
    const audioCalling=vm.runInNewContext('(function(){'+fs.readFileSync(path.join(root,'calling.js'),'utf8')+'})()', {
        baseclass:{extend:value=>value},messaging,
        rpc:{declare:spec=>{assert.equal(spec.nobatch,true,'voice RPC waited for a browser paint frame');assert.equal(spec.reject,true,'voice RPC hid transport errors');return async(...args)=>{if(spec.method==='voice_audio')uploaded.push(args);return {}; };}},
        L:{Class:{extend:body=>{function API(){}API.prototype=body;return API;}}},
        btoa:value=>Buffer.from(value,'binary').toString('base64'),atob:value=>Buffer.from(value,'base64').toString('binary'),
        window:{isSecureContext:true,crypto:{getRandomValues:bytes=>bytes.fill(1)},setTimeout:handler=>{audioTimers.push(handler);return audioTimers.length;},AudioContext:function(){
            this.sampleRate=48000;this.destination={};this.resume=async()=>{};this.close=()=>{};
            this.createMediaStreamSource=()=>({connect(){},disconnect(){}});
            this.createScriptProcessor=()=>processor={connect(){},disconnect(){}};
        }},navigator:{mediaDevices:{
            enumerateDevices:async()=>[{kind:'audioinput',deviceId:'physical',label:'Physical microphone'},{kind:'audioinput',deviceId:'default',label:'Default'},{kind:'audiooutput',deviceId:'speaker'}],
            getUserMedia:async constraints=>{constraintsSeen.push(constraints);return {getTracks:()=>[{stop(){}}],getAudioTracks:()=>[{label:'Physical microphone',muted:false,enabled:true}]};}
        }}
    });
    const audioAPI=audioCalling.create(),active={id:'test',state:'active',audio:'browser',owned:true};
    await audioAPI.openAudio('physical');await audioAPI.exchangeAudio({},active);
    assert.equal(constraintsSeen[0].audio.deviceId.exact,'physical','selected microphone was ignored');
    assert.equal(audioAPI.microphones.length,1,'non-microphone or duplicate default device was offered');
    for(let block=0;block<6;block++) {
        const input=Float32Array.from({length:2048},(_,i)=>0.5*Math.sin(2*Math.PI*1000*(block*2048+i)/48000));
        processor.onaudioprocess({inputBuffer:{getChannelData:()=>input}});
    }
    await audioAPI.exchangeAudio({},active);await audioAPI.exchangeAudio({},active);
    const firstPCM=Buffer.from(uploaded[1][4],'base64'),secondPCM=Buffer.from(uploaded[2][4],'base64');
    assert.equal(firstPCM.length,6400,'microphone batch exceeded the device limit');
    assert.equal(secondPCM.length,1280,'resampling lost complete PCM frames');
    for(let i=0;i<3200;i++)assert(Math.abs(firstPCM.readInt16LE(i*2)-Math.round(16383.5*Math.sin(2*Math.PI*1000*i/16000)))<=1,'microphone samples changed before upload');
    const micInfo=audioAPI.microphoneInfo();assert(micInfo.level>0&&!micInfo.silent&&!micInfo.muted);
    const sequenceBefore=audioAPI.sequence;
    await audioAPI.openAudio('physical');await audioAPI.exchangeAudio({},active);
    assert.equal(audioAPI.sequence,sequenceBefore+1,'changing microphones reset the live call sequence');
    audioAPI.micLastSignal=Date.now()-5000;assert(audioAPI.microphoneInfo().silent,'zero microphone input was hidden');
    audioAPI.closeAudio();
    const beforeTestUploads=uploaded.length,microphoneTest=audioAPI.testAudio('physical');
    for(let i=0;!audioTimers.length&&i<20;i++)await Promise.resolve();
    assert.equal(audioTimers.length,1);
    processor.onaudioprocess({inputBuffer:{getChannelData:()=>new Float32Array(2048).fill(0.1)}});
    audioTimers[0]();const testInfo=await microphoneTest;
    assert(testInfo.signal&&testInfo.peak>0&&!audioAPI.context,'microphone test missed input or left the microphone open');
    assert.equal(uploaded.length,beforeTestUploads,'local microphone test transmitted audio');
    const canceledTest=audioAPI.testAudio('physical');
    for(let i=0;audioTimers.length<2&&i<20;i++)await Promise.resolve();
    await audioAPI.openAudio('physical');audioTimers[1]();
    await assert.rejects(canceledTest,/취소/);assert(audioAPI.context,'an old microphone test closed the new input');audioAPI.closeAudio();
    let finishTest;api.testAudio=()=>new Promise(resolve=>finishTest=resolve);
    const previewState=model.create(),beforePreview=closed;current={state:'idle',ready:true};
    const preview=model.testMicrophone(previewState);await model.refresh(previewState);
    assert.equal(closed,beforePreview,'idle polling closed the microphone test');
    await assert.rejects(model.dial(previewState,'+821000000001'),/마이크 확인/);
    finishTest({signal:true,peak:0.1});await preview;assert(previewState.microphoneTest.signal&&!previewState.microphoneTesting);
    let now=0,delays=[];
    const cadenceModel=vm.runInNewContext('(function(){'+fs.readFileSync(path.join(root,'calling_model.js'),'utf8')+'})()', {
        baseclass:{extend:value=>value},calling:{create:()=>({exchangeAudio:async()=>{now+=60;}})},messaging,Date:{now:()=>now},
        window:{setTimeout:(_,delay)=>{delays.push(delay);return 1;}}
    });
    cadenceModel.startAudio(cadenceModel.create(),error=>{throw error;});
    await Promise.resolve();await Promise.resolve();await Promise.resolve();
    assert.equal(delays[0],40,'audio upload response time was added to each 100 ms interval');
    api.listMicrophones=async()=>api.microphones=[{id:'physical',label:'Physical microphone'}];
    api.microphoneInfo=()=>({level:0.5,label:'Physical microphone',muted:false,silent:false});
    function element(tag,attributes,children) {
        const node={tag,attributes:attributes || {},value:attributes&&attributes.value || '',hidden:false,classList:{toggle(){}},
            setAttribute(key,value){this.attributes[key]=value;},appendChild(child){this.children.push(child);},
            replaceChildren(...items){this.children=items;}};
        node.children=(Array.isArray(children)?children:children==null?[]:[children]).map(child=>typeof child==='string'?{textContent:child}:child);
        Object.defineProperty(node,'lastChild',{get(){return this.children[this.children.length-1];}});return node;
    }
    current={state:'idle',ready:true,owned:false,occupied:false};
    const screenView=vm.runInNewContext('(function(){'+fs.readFileSync(path.join(root,'view/modem/calls.js'),'utf8')+'})()', {
        view:{extend:value=>value},callModel:model,E:element,URLSearchParams,
        ui:{createHandlerFn:(object,handler)=>handler.bind(object)},poll:{add(){}},
        L:{hasViewPermission:()=>true,url:()=>'/messages',resource:name=>name,bind:(fn,context)=>fn.bind(context)},
        window:{location:{search:''},setTimeout(){},addEventListener(){}}
    });
    await screenView.load();screenView.render();
    assert(!screenView.microphonePanel.hidden&&screenView.microphone.children.length===2,'idle phone did not offer its microphone devices');
    current={state:'active',ready:true,owned:true,occupied:true,audio:'browser'};
    await model.refresh(screenView.state);screenView.paint();
    assert(!screenView.microphoneLevel.hidden&&screenView.microphoneLevel.value===0.5,'active phone hid microphone input');
    screenView.state.microphonePending=true;screenView.paint();assert(screenView.microphone.disabled,'microphone change overlapped another change');
    screenView.state.microphonePending=false;current={state:'active',ready:true,owned:false,occupied:true};
    await model.refresh(screenView.state);screenView.paint();assert(screenView.microphonePanel.hidden,'another page could change the microphone');
    console.log('Contacts, call states, page ownership, microphone readiness and leave/hangup checks passed');
})().catch(error=>{console.error(error);process.exitCode=1;});
