import * as L from './lib.mjs'; const {ev,sleep}=L;
const theme = process.argv[2];
await L.load(theme);
await sleep(1500);
await ev(`const st=document.createElement('style');st.id='hide';st.textContent='.alert:has(.plist){display:none!important}';document.head.appendChild(st);
 const b=document.querySelector('#sideBtn'); if(b.getAttribute('aria-expanded')==='true') b.click(); 1`);
await sleep(500);
await ev(`openChanges(); 1`); await sleep(2500);
await ev(`{const m=document.querySelector('#wipMsg'); m.value='Add the idempotency key to card charges'; m.dispatchEvent(new Event('input'));}`);
await sleep(2500);
await L.shot('history-changes-'+theme); process.exit(0)
