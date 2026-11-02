// No browser dependencies: exercise the shipped script against a minimal DOM contract.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const test = require('node:test');

function page(opts = {}) {
 const names = ['sing-box', 'surge4', 'surge5', 'clash', 'loon', 'shadowrocket', 'v2ray', 'quantumultx'];
 const imports = names.map(format => ({dataset:{subImport:format},href:''}));
 const modes = ['ordinary','quic','tailscale','tailscale-quic'].map(mode => ({dataset:{sbMode:mode,caption:mode+' caption'},attrs:{},
  classList:{active:false,toggle(_,on){this.active=on;}},setAttribute(k,v){this.attrs[k]=v;}}));
 const message = {textContent:''};
 const versionOptions = [{dataset:{caption:'1.14 caption'}},{dataset:{caption:'1.15 caption'}}];
 const fields = {'sb-version':{value:'1.14',selectedIndex:0,options:versionOptions},'sb-mode':{value:'ordinary'},
  'sb-version-caption':{textContent:''},'sb-mode-caption':{textContent:''}};
 for (const key of ['type','class','noclass','regex','emoji','extend']) fields['sub-'+key]={value:''};
 if (opts.lines) fields['sub-line'] = {options:opts.lines.map(value=>({value})),selectedIndex:0,get value(){return this.options[this.selectedIndex].value;}};
 const listeners = {};
 const root = {dataset:{subBase:'https://panel.example.com/link/test-only-token'},
  querySelectorAll:selector=>selector==='[data-sb-mode]'?modes:imports,
  addEventListener:(name,callback)=>{listeners[name]=callback;}};
 let copied = '';
 const document = {getElementById:id=>id==='subscription-center'?root:fields[id]};
 const window = {location:{href:'https://panel.example.com/user'},isSecureContext:true};
 if (opts.storage) window.localStorage = opts.storage;
 vm.runInNewContext(fs.readFileSync('web/static/assets/js/subscription-center.js','utf8'),{
  document,window,URL,navigator:{clipboard:{writeText:async value=>{copied=value;}}},
  btoa:value=>Buffer.from(value).toString('base64')
 });
 const card = {querySelector:()=>message};
 const click = target => listeners.click({target:{closest:selector=>selector==='[data-sb-mode]'?(target.dataset.sbMode?target:null):(target.dataset.sbMode?null:{...target,closest:()=>card})}});
 return {imports,modes,fields,listeners,message,click,getCopied:()=>copied};
}

test('mode buttons and version select drive eight sing-box variants',async()=>{
 const p=page();
 for(const [i,version] of ['1.14','1.15'].entries()) for(const mode of p.modes) {
  p.fields['sb-version'].value=version;p.fields['sb-version'].selectedIndex=i;p.listeners.change();
  await p.click(mode);
  assert.equal(p.fields['sb-mode'].value,mode.dataset.sbMode);
  assert.ok(mode.classList.active);assert.equal(mode.attrs['aria-pressed'],'true');
  assert.equal(p.modes.filter(m=>m.classList.active).length,1);
  assert.equal(p.fields['sb-mode-caption'].textContent,mode.dataset.caption);
  assert.equal(p.fields['sb-version-caption'].textContent,version+' caption');
  const url=new URL(new URL(p.imports[0].href).searchParams.get('url'));
  assert.equal(url.searchParams.get('sb_version'),version);
  assert.equal(url.searchParams.get('quic'),mode.dataset.sbMode.includes('quic')?'1':null);
  assert.equal(url.searchParams.get('tailscale'),mode.dataset.sbMode.includes('tailscale')?'1':null);
  await p.click({dataset:{subCopy:'sing-box'}});
  assert.equal(p.getCopied(),url.href);
 }
});

test('Surge 4 and 5 import different subscription URLs',()=>{
 const p=page();
 for(const version of ['4','5']) {
  const link=p.imports.find(a=>a.dataset.subImport==='surge'+version);
  const nested=new URL(new URL(link.href).searchParams.get('url'));
  assert.equal(nested.searchParams.get('surge'),version);
 }
});

test('Quantumult X imports the subscription as a server_remote resource',()=>{
 const p=page();
 const link=new URL(p.imports.find(a=>a.dataset.subImport==='quantumultx').href);
 assert.equal(link.protocol,'quantumult-x:');
 const resource=JSON.parse(link.searchParams.get('remote-resource'));
 const [url,tag]=resource.server_remote[0].split(', ');
 assert.equal(new URL(url).searchParams.get('quantumultx'),'1');
 assert.equal(tag,'tag=Subscription');
});

test('protocol subscriptions copy their own links; VMess uses sub=3',async()=>{
 const p=page();
 for(const [format,key,value] of [['ss2022','ss2022','1'],['vmess','sub','3'],['vless','vless','1'],['anytls','anytls','1']]) {
  await p.click({dataset:{subCopy:format}});
  assert.equal(new URL(p.getCopied()).searchParams.get(key),value);
  assert.match(p.message.textContent,/订阅链接已复制/);
 }
});

test('全部 URL copies the page text and reports an empty list',async()=>{
 const p=page();
 await p.click({dataset:{copyText:'vmess://a\nvmess://b\n'}});
 assert.equal(p.getCopied(),'vmess://a\nvmess://b\n');
 assert.match(p.message.textContent,/全部节点链接已复制/);
 await p.click({dataset:{copyText:''}});
 assert.equal(p.getCopied(),'vmess://a\nvmess://b\n');
 assert.match(p.message.textContent,/暂无可用节点/);
});

test('filters apply to copies and imports of legacy clients',async()=>{
 const p=page();
 for(const [key,value] of Object.entries({type:'vmess',class:'0+1',noclass:'2',regex:'/香港|HK/i',emoji:'1',extend:'0'})) p.fields['sub-'+key].value=value;
 p.listeners.change();
 const mappings={kitsunebi:['kitsunebi','1'],surfboard:['surfboard','1'],quantumult:['quantumult','3'],'quantumult-source':['quantumult','2']};
 for(const [format,[key,value]] of Object.entries(mappings)) {
  await p.click({dataset:{subCopy:format}});
  const url=new URL(p.getCopied());
  assert.equal(url.searchParams.get(key),value);
  assert.equal(url.searchParams.get('class'),'0+1');
  assert.equal(url.searchParams.get('regex'),'/香港|HK/i');
  assert.equal(url.searchParams.get('extend'),'0');
 }
 const nested=new URL(new URL(p.imports[0].href).searchParams.get('url'));
 assert.equal(nested.searchParams.get('class'),'0+1');
});

test('subscription line select switches every copied and imported link, and is remembered',async()=>{
 const lines=['https://panel.example.com/link/test-only-token','https://sub2.example.net/link/test-only-token','https://sub3.example.org/link/test-only-token'];
 const store={};
 const storage={getItem:k=>k in store?store[k]:null,setItem:(k,v)=>{store[k]=String(v);}};
 const p=page({lines,storage});
 assert.equal(p.fields['sub-line'].selectedIndex,0);
 await p.click({dataset:{subCopy:'vmess'}});
 assert.equal(new URL(p.getCopied()).host,'panel.example.com');
 p.fields['sub-line'].selectedIndex=1;p.listeners.change();
 assert.equal(store['niuniu-sub-line'],'1');
 for(const [format,key] of [['vmess','sub'],['clash','clash'],['kitsunebi','kitsunebi']]) {
  await p.click({dataset:{subCopy:format}});
  const url=new URL(p.getCopied());
  assert.equal(url.host,'sub2.example.net');assert.equal(url.pathname,'/link/test-only-token');assert.ok(url.searchParams.has(key));
 }
 const backup='https://sub2.example.net/link/test-only-token';
 for(const a of p.imports) {
  const ok=a.href.includes(encodeURIComponent(backup))||a.href.includes(encodeURIComponent(encodeURIComponent(backup)))||a.href.startsWith('sub://'+Buffer.from(backup).toString('base64').slice(0,40));
  assert.ok(ok,a.dataset.subImport+' '+a.href);
 }
 // 重新打开页面：使用上次选择的线路。
 store['niuniu-sub-line']='2';
 const q=page({lines,storage});
 assert.equal(q.fields['sub-line'].selectedIndex,2);
 await q.click({dataset:{subCopy:'anytls'}});
 assert.equal(new URL(q.getCopied()).host,'sub3.example.org');
});

test('invalid or unavailable remembered line falls back to the main line',async()=>{
 const lines=['https://panel.example.com/link/t','https://sub2.example.net/link/t'];
 for(const storage of [{getItem:()=>'7',setItem:()=>{}},{getItem:()=>'abc',setItem:()=>{}},{getItem:()=>{throw new Error('blocked');},setItem:()=>{throw new Error('blocked');}}]) {
  const p=page({lines,storage});
  assert.equal(p.fields['sub-line'].selectedIndex,0);
  p.listeners.change();
  await p.click({dataset:{subCopy:'vless'}});
  assert.equal(new URL(p.getCopied()).host,'panel.example.com');
 }
});
