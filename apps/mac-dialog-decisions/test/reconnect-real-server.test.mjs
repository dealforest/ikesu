import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import {spawn,execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {MacDialogDecisionsDaemon,spawnDecisionWatch,createGetDecision,createRunAnswer} from '../mac-dialog-decisions.mjs';
import {manageDaemon,requestSwitch} from '../lifecycle.mjs';
const exec=promisify(execFile);
async function until(predicate){const end=Date.now()+10000;while(!predicate()){if(Date.now()>end)throw Error('real server condition deadline');await new Promise(r=>setTimeout(r,10));}}
async function fixture(t, {submitAnswers = false, dropFirstAnswerResponse = false} = {}){
 const binary=process.env.MAC_DIALOG_TEST_IKESU;assert.ok(binary,'real ikesu binary required');
 const root=fs.mkdtempSync('/tmp/ikesu-reconcile-'),stateDir=path.join(root,'plugin-state');fs.mkdirSync(stateDir);
 const config=path.join(root,'config.toml');fs.writeFileSync(config,'[experimental]\nallow_nested=true\n');
 const env={...process.env,HOME:root,IKESU_CONFIG:config,IKESU_STATE_DIR:path.join(root,'state'),IKESU_SOCKET_PATH:path.join(root,'state/default/ikesu.sock'),IKESU_SOCKET_PATH_EXPLICIT:'1',SHELL:'/bin/sh'};
 const server=spawn(binary,['server'],{env,stdio:['ignore','ignore','pipe']});let stderr='';server.stderr.on('data',b=>stderr+=b);
 const exited=new Promise(resolve=>server.once('close',resolve));
 const rpc=(method,params={})=>new Promise((resolve,reject)=>{
  const socket=net.createConnection(env.IKESU_SOCKET_PATH);let data='';
  socket.on('connect',()=>socket.write(JSON.stringify({id:'reconcile-test',method,params})+'\n'));
  socket.on('error',reject);socket.on('data',b=>{data+=b;if(!data.includes('\n'))return;socket.end();const reply=JSON.parse(data.split('\n')[0]);reply.error?reject(Error(JSON.stringify(reply.error))):resolve(reply.result);});
 });
 await until(()=>{if(server.exitCode!==null)throw Error(stderr);return fs.existsSync(env.IKESU_SOCKET_PATH)});
 const watches=[],logs=[];let answerSends=0;
 const runAnswer=createRunAnswer({ikesuBin:binary},()=>{},env);
 let runFirstAnswer=runAnswer;
 if(dropFirstAnswerResponse){
  const answerSocket=path.join(root,'answer.sock');
  const proxy=net.createServer(client=>{
   const backend=net.createConnection(env.IKESU_SOCKET_PATH);
   client.pipe(backend);
   const disconnect=()=>{client.destroy();backend.destroy()};
   backend.once('data',disconnect);
   backend.once('error',disconnect);
   client.once('error',disconnect);
   client.once('close',disconnect);
  });
  await new Promise((resolve,reject)=>{proxy.once('error',reject);proxy.listen(answerSocket,resolve)});
  t.after(()=>new Promise(resolve=>proxy.close(resolve)));
  runFirstAnswer=createRunAnswer({ikesuBin:binary},()=>{},{...env,IKESU_SOCKET_PATH:answerSocket});
 }
 const daemon=new MacDialogDecisionsDaemon({config:{stateDir,presentDelayMs:60000,answerRetryMs:10},log:message=>{logs.push(message);t.diagnostic(message)},
  getDecision:createGetDecision({ikesuBin:binary},()=>{},env),
  runAnswer:async args=>{
   answerSends++;
   if(!submitAnswers)throw Error('unexpected additional answer');
   return (answerSends===1?runFirstAnswer:runAnswer)(args);
  },
 });
 async function watch(){
  const handle=spawnDecisionWatch({ikesuBin:binary},()=>{},env);
  const events=[];
  const done=(async()=>{try{for await(const row of handle.lines){events.push(row);daemon.onWatchLine(row)}}catch{}})();
  const item={handle,done,events};watches.push(item);return item;
 }
 t.after(async()=>{for(const w of watches){w.handle.close();await w.done}await daemon.stop();try{await rpc('server.stop')}catch{}await exited;fs.rmSync(root,{recursive:true,force:true});});
 const create=async title=>(await rpc('decision.create',{kind:'ask',title,allow_text:true,options:[{id:'yes',label:'Yes',role:'approve'}]})).decision;
 return {root,stateDir,env,exited,logs,daemon,rpc,watch,create,get answerSends(){return answerSends}};
}

async function startCodexQuestion(f, firstInput='answer'){
 const emulator=path.join(f.root,'codex.py');
 const rollout=path.join(f.root,'rollout.jsonl');
 const inputFile=path.join(f.root,'keys');
 const question='Report format?';
 fs.writeFileSync(rollout,[
  {type:'session_meta',payload:{id:'reconnect-test'}},
  {timestamp:new Date().toISOString(),type:'response_item',payload:{
   type:'function_call',name:'request_user_input',call_id:'call',
   arguments:JSON.stringify({questions:[{id:'format',question,options:[{label:'Text',description:'Plain text.'}]}]}),
  }},
 ].map(JSON.stringify).join('\n')+'\n');
 fs.writeFileSync(emulator,`#!/usr/bin/env python3
import json, os, sys, tty

screen = "\\x1b[2J\\x1b[HQuestion 1/1 (1 unanswered)\\r\\nReport format?\\r\\n\\r\\n  › 1. Text    Plain text.\\r\\n    2. None of the above\\r\\n\\r\\n  tab to add notes | enter to submit answer | esc to interrupt"
mode = sys.argv[3]

def record(rollout, payload):
    rollout.write(json.dumps({"type": "response_item", "payload": payload}) + "\\n")

with open(sys.argv[1], "a", buffering=1) as rollout, open(sys.argv[2], "ab", buffering=0) as keys:
    tty.setraw(0)
    os.write(1, screen.encode())
    first = True
    message = bytearray()
    other = None
    while data := os.read(0, 1):
        keys.write(data)
        if first:
            first = False
            selected = ["Text"] if mode == "answer" else []
            result = json.dumps({"answers": {"format": {"answers": selected}}})
            record(rollout, {"type": "function_call_output", "call_id": "call", "output": result})
            if mode == "switch":
                rollout.close()
                other = open(sys.argv[1] + ".other.jsonl", "a", buffering=1)
                other.write(json.dumps({"type": "session_meta", "payload": {"id": "other"}}) + "\\n")
            os.write(1, "\\x1b]0;Codex ready\\x07\\x1b[2J\\x1b[H› Ask Codex to do anything".encode())
        elif data == b"\\r" and mode == "empty":
            text = message.decode().removeprefix("\\x1b[200~").removesuffix("\\x1b[201~")
            record(rollout, {"type": "message", "role": "user", "content": [{"type": "input_text", "text": text}]})
        else:
            message.extend(data)
`,{mode:0o700});
 const {agent}=await f.rpc('agent.start',{name:'codex-reconnect-test',argv:[emulator,rollout,inputFile,firstInput],cwd:f.root});
 const watch=await f.watch();
 await until(()=>[...f.daemon.entries.values()].some(entry=>entry.decision.origin?.pane_id===agent.pane_id));
 const entry=[...f.daemon.entries.values()].find(entry=>entry.decision.origin?.pane_id===agent.pane_id);
 assert.equal(entry.decision.kind,'agent_prompt');
 assert.equal(entry.decision.origin.agent,'codex');
 return {entry,watch,inputFile};
}

test('通常回答を保存してから応答だけ失ってもCodexのPTY入力は1回',async t=>{
 const f=await fixture(t,{submitAnswers:true,dropFirstAnswerResponse:true});
 const {entry,watch,inputFile}=await startCodexQuestion(f);
 // Deliberately end only the subscription to exercise same-ID lookup.
 watch.handle.close();
 await watch.done;
 await f.daemon.submitAnswer(entry,{optionIds:[entry.decision.options[0].id]});
 await until(()=>f.daemon.entries.size===0);
 const {decision}=await f.rpc('decision.get',{decision_id:entry.decision.decision_id});
 assert.equal(decision.status,'answered');
 assert.equal(f.answerSends,1);
 assert.equal(fs.readFileSync(inputFile,'utf8'),'1');
});

for(const [mode,status] of [['answer','delivered'],['empty','delivered'],['switch','failed']]){
 test(`Codexの回答結果に応じた配達状態をwatchで受け取る (${mode})`,async t=>{
  const f=await fixture(t,{submitAnswers:true});
  const {entry,watch,inputFile}=await startCodexQuestion(f,mode);
  const id=entry.decision.decision_id;
  await f.daemon.submitAnswer(entry,{optionIds:[entry.decision.options[0].id]});
  const result=(event,delivery)=>watch.events.find(row=>row.event===event &&
   row.decision?.decision_id===id && row.decision.delivery?.status===delivery);
  await until(()=>result('updated',status));
  assert.ok(result('resolved','pending'));
  const keys=fs.readFileSync(inputFile,'utf8').replaceAll(/\x1b\[20[01]~/g,'');
  if(mode==='empty')assert.equal(keys,'1さっきの質問『Report format?』への回答：Text\r');
  else{
   if(mode==='switch')assert.match(result('updated','failed').decision.delivery.reason,/元の Codex セッション/);
   assert.equal(keys,'1');
  }
  assert.equal(f.answerSends,1);
 });
}

test('Codexの回答接続が切れた後は同じ判断待ちIDを照会してから元の回答を扱う',async t=>{
 const f=await fixture(t,{submitAnswers:true});
 const origin={agent:'codex',reason:'screen_prompt'};
 const options=[{id:'yes',label:'Yes',role:'other'}];
 const create=async title=>(await f.rpc('decision.create',{kind:'agent_prompt',title,origin,allow_text:true,options})).decision;
 const decision=await create('Codex question'),other=await create('Unrelated Codex question');
 f.daemon.onCreated('local',{id:'local'},decision);
 const [entry]=f.daemon.entries.values();
 const socket=f.env.IKESU_SOCKET_PATH,hidden=socket+'.held';
 fs.renameSync(socket,hidden);
 try{
  await f.daemon.submitAnswer(entry,{optionIds:['yes'],text:'元の回答'});
  await until(()=>f.logs.some(line=>line.includes('再送前に判断待ちを確認できませんでした')));
  assert.equal(f.answerSends,1);
  assert.equal(entry.retry.text,'元の回答');
 }finally{
  fs.renameSync(hidden,socket);
 }
 await until(()=>f.daemon.entries.size===0);
 assert.equal(f.answerSends,2);
 // The server explicitly retires this question because it has no matching pane.
 const lookup=async id=>(await f.rpc('decision.get',{decision_id:id})).decision;
 assert.equal((await lookup(decision.decision_id)).status,'cancelled');
 assert.equal((await lookup(other.decision_id)).status,'pending');
 f.daemon.onCreated('local',{id:'local'},other);
 const [resolvedElsewhere]=f.daemon.entries.values();
 await f.rpc('decision.cancel',{decision_id:other.decision_id});
 await f.daemon.submitAnswer(resolvedElsewhere,{optionIds:['yes']});
 await until(()=>f.daemon.entries.size===0);
 assert.equal(f.answerSends,3);
 assert.equal(resolvedElsewhere.resolvedStatus,'cancelled');
});

test('実照会が切れたentryは保持し次の再接続で保存済みの既決だけ回収する',async t=>{
 const f=await fixture(t),decision=await f.create('lookup disconnect');
 f.daemon.onWatchLine({event:'created',machine:{id:'local'},decision});
 await f.rpc('decision.cancel',{decision_id:decision.decision_id});
 f.daemon.onMachineDown('local','disconnect');
 const hidden=f.env.IKESU_SOCKET_PATH+'.held';fs.renameSync(f.env.IKESU_SOCKET_PATH,hidden);
 try{
  f.daemon.onWatchLine({event:'machine_up',machine:{id:'local'}});
  await until(()=>f.logs.some(line=>line.includes('保持します')));
  assert.equal(f.daemon.entries.size,1);assert.equal([...f.daemon.entries.values()][0].machineDown,true);
 }finally{fs.renameSync(hidden,f.env.IKESU_SOCKET_PATH)}
 f.daemon.onMachineDown('local','reconnect');f.daemon.onWatchLine({event:'machine_up',machine:{id:'local'}});
 await until(()=>f.daemon.entries.size===0);assert.equal(f.answerSends,0);
});

test('decision getの指定なしとlocal指定は同じ保存済み出力で不明マシンは明示エラーになる',async t=>{
 const f=await fixture(t),decision=await f.create('CLI saved lookup');
 const binary=process.env.MAC_DIALOG_TEST_IKESU,args=['decision','get',decision.decision_id];
 const plain=JSON.parse((await exec(binary,args,{env:f.env})).stdout);
 const local=JSON.parse((await exec(binary,[...args,'--machine','local'],{env:f.env})).stdout);
 assert.deepEqual(local,plain);
 await assert.rejects(exec(binary,[...args,'--machine','not-saved'],{env:f.env}),error=>error.code===1&&/machine/.test(error.stderr));
});

test('Astra最小再現のイベント列は実保存先の既決照合後にentryを回収する',async t=>{
 const f=await fixture(t),decision=await f.create('Astra minimum');
 await f.rpc('decision.answer',{decision_id:decision.decision_id,option_id:'yes'});
 f.daemon.onWatchLine({event:'created',machine:{id:'local'},decision});
 f.daemon.onWatchLine({event:'machine_down',machine:{id:'local'}});
 f.daemon.onWatchLine({event:'machine_up',machine:{id:'local'}});
 clearTimeout(f.daemon._pumpTimer);
 await until(()=>f.daemon.entries.size===0);
 assert.equal(f.answerSends,0);
});

test('購読だけ切断中の別クライアント回答と取消を回収しpending状態を保って1回だけ切り替える',async t=>{
 const f=await fixture(t),decisions=[];
 for(const title of ['answered elsewhere','cancelled elsewhere','pending','snoozed','in flight','retrying'])decisions.push(await f.create(title));
 const first=await f.watch();await until(()=>f.daemon.entries.size===decisions.length);
 first.handle.close();await first.done;f.daemon.onMachineDown('local','actual subscription closed');
 const entry=id=>f.daemon.entries.get('local:'+id)||[...f.daemon.entries.values()].find(e=>e.decision.decision_id===id);
 const answered=entry(decisions[0].decision_id),cancelled=entry(decisions[1].decision_id);
 answered.retry={optionIds:['yes'],text:'must not resend'};answered.answerRetryTimer=setTimeout(()=>{throw Error('resolved retry survived')},60000);
 cancelled.dismissed=true;cancelled.snoozeTimer=setTimeout(()=>{throw Error('resolved snooze survived')},60000);
 const pending=decisions.slice(2).map(d=>entry(d.decision_id));
 pending[0].draft={option_ids:['yes'],text:'keep draft'};pending[1].dismissed=true;pending[2].answerPending=true;pending[3].retry={optionIds:['yes'],text:'keep retry'};
 await f.rpc('decision.answer',{decision_id:decisions[0].decision_id,option_id:'yes'});
 await f.rpc('decision.cancel',{decision_id:decisions[1].decision_id});
 const running=path.join(f.root,'old'),next=path.join(f.root,'next'),registry=path.join(f.root,'registration.json');
 fs.mkdirSync(running);fs.mkdirSync(next);fs.writeFileSync(path.join(running,'VERSION'),'0.1.0\n');fs.writeFileSync(registry,JSON.stringify({root:next}));
 let switches=0;
 const lifecycle=manageDaemon(f.daemon,{stateDir:f.stateDir,daemonOwner:'real-reconnect'},{root:running,registeredRoot:async()=>JSON.parse(fs.readFileSync(registry)).root,onSwitch:async()=>{switches++}});t.after(()=>lifecycle.stop());
 requestSwitch(f.stateDir,next);await f.watch();
 await until(()=>f.daemon.entries.size===pending.length&&pending.every(e=>!e.machineDown));
 assert.equal(answered.retry,null);assert.equal(answered.answerRetryTimer,null);assert.equal(cancelled.snoozeTimer,null);
 assert.deepEqual(pending[0].draft,{option_ids:['yes'],text:'keep draft'});assert.equal(pending[1].dismissed,true);assert.equal(pending[2].answerPending,true);assert.equal(pending[3].retry.text,'keep retry');
 assert.equal(f.answerSends,0);assert.equal(switches,0);
 for(const decision of decisions.slice(2))await f.rpc('decision.cancel',{decision_id:decision.decision_id});
 await until(()=>switches===1);assert.equal(f.daemon.entries.size,0);await lifecycle.check();assert.equal(switches,1);assert.equal(f.answerSends,0);
});
