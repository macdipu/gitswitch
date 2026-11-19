import * as L from './lib.mjs'; const {ev,sleep}=L;
const theme = process.argv[2];
await L.load(theme); await sleep(1000);
await ev(`const b=document.querySelector('#sideBtn'); if(b&&b.getAttribute('aria-expanded')==='true') b.click(); openChanges(); 1`); await sleep(2500);
await ev(`document.querySelector('.alert:has(.plist)')?.remove()`);
console.log(await ev(`[...document.querySelectorAll('#history *')].filter(e=>e.children.length===0&&e.textContent.trim()==='handler.go').length`));
await ev(`document.querySelector('.crow[data-path="internal/checkout/handler.go"] .frow').click()`); await sleep(2000);
console.log(await ev(`[...document.querySelectorAll('#hdiff .dl[data-l]')].map(e=>e.className+'|'+e.dataset.h+':'+e.dataset.l+'|'+e.textContent.trim().slice(0,40)).join('\\n')`));
console.log(L.errs);
process.exit(0)
