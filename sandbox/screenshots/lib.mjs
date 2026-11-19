import fs from 'node:fs'; import {execSync} from 'node:child_process';
export const OUT = new URL('../../docs/images/v2', import.meta.url).pathname;
fs.mkdirSync(OUT, {recursive: true});
export const DSF = +(process.env.DSF || 2), Q = +(process.env.Q || 85);
const list = await (await fetch('http://127.0.0.1:9340/json/list')).json();
const ws = new WebSocket(list.find(t => t.type === 'page').webSocketDebuggerUrl);
await new Promise(r => ws.onopen = r);
let id = 0; const wait = new Map(); export const errs = [];
ws.onmessage = m => { const d = JSON.parse(m.data); if (d.id && wait.has(d.id)) { wait.get(d.id)(d); wait.delete(d.id); } if (d.method === 'Runtime.exceptionThrown') errs.push(d.params.exceptionDetails.exception?.description || d.params.exceptionDetails.text); };
export const send = (method, params = {}) => new Promise(r => { const i = ++id; wait.set(i, r); ws.send(JSON.stringify({id: i, method, params})); });
export const sleep = ms => new Promise(r => setTimeout(r, ms));
export const ev = async e => { const r = await send('Runtime.evaluate', {expression: e, awaitPromise: true, returnByValue: true}); if (r.result?.exceptionDetails) console.log('JS error:', JSON.stringify(r.result.exceptionDetails).slice(0, 300)); return r.result?.result?.value; };
export const sh = c => execSync(`docker exec gsshots sh -c ${JSON.stringify(c)}`).toString().trim();
export const reset = () => { sh('rm -rf /home/dana && cp -a /home/dana.bak /home/dana'); };
export async function load(theme, pre = '', ls = '') {
  await send('Page.enable'); await send('Runtime.enable');
  await send('Emulation.setDeviceMetricsOverride', {width: 1440, height: 900, deviceScaleFactor: DSF, mobile: false});
  await send('Emulation.setEmulatedMedia', {features: [{name: 'prefers-color-scheme', value: theme === 'dim' ? 'dark' : theme}]});
  await send('Page.navigate', {url: 'about:blank'}); await sleep(200);
  await send('Page.navigate', {url: 'http://127.0.0.1:8091/'}); await sleep(1500);
  await ev(`localStorage.setItem('gs.theme', JSON.stringify('${theme}')); localStorage.setItem('gs.fetchOnOpen','false'); localStorage.setItem('gs.repoTabs', JSON.stringify(['/home/dana/projects/storefront/.git','/home/dana/projects/docs-site/.git'])); localStorage.setItem('gs.repoTabsActive','/home/dana/projects/storefront/.git')`);
  if (ls) await ev(ls);
  await send('Page.reload'); await sleep(2500);
  await ev(`(async()=>{const st=document.createElement('style');st.textContent='#installBanner,#gitBanner{display:none!important}';document.head.appendChild(st); document.querySelectorAll('.banner').forEach(b=>{if(/GitSwitch|Git is not/.test(b.textContent)&&b.id!=='opBanner')b.hidden=true});
    const rs=[...document.querySelector('#histRepo').options].map(o=>o.value); const d=rs.find(v=>v.includes('docs-site')), s=rs.find(v=>v.includes('storefront'));
    document.querySelectorAll('aside *').forEach(e=>{if(e.children.length===0&&e.textContent.trim()==='GitSwitch dev')e.textContent='GitSwitch 2.0.0'});})()`);
  await ev(`show('history')`); await sleep(2500); await ev(`openTab('/home/dana/projects/storefront/.git')`); await sleep(3000);
  if (pre) await ev(pre);
}
export async function shot(name) {
  await ev(`document.querySelectorAll('aside *').forEach(e=>{if(e.children.length===0&&e.textContent.trim()==='GitSwitch dev')e.textContent='GitSwitch 2.0.0'}); document.querySelectorAll('.toast').forEach(t=>t.remove()); document.activeElement?.blur?.()`);
  await send('Input.dispatchMouseEvent', {type: 'mouseMoved', x: 1430, y: 890}); await sleep(900);
  const r = await send('Page.captureScreenshot', {format: 'webp', quality: Q});
  const f = `${OUT}/${name}.webp`; fs.writeFileSync(f, Buffer.from(r.result.data, 'base64'));
  console.log(name, (fs.statSync(f).size / 1024).toFixed(0) + ' KB');
}
