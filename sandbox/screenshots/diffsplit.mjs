import * as L from './lib.mjs'; const {ev,sleep}=L;
const theme = process.argv[2];
await L.load(theme,'',`localStorage.setItem('gs.filesView',JSON.stringify({preview:false})); localStorage.setItem('gs.diffView',JSON.stringify({layout:'split'}))`);
const gd='/home/dana/projects/storefront/.git';
const h = L.sh(`cd /home/dana/projects/storefront && git log --format=%H --grep="Cap discounts" -1`);
await ev(`document.querySelector('.alert:has(.plist)')?.remove()`);
await ev(`showDiff('${gd}','${h}','internal/cart/cart.go'); 1`); await sleep(2500);
console.log(await ev(`document.querySelector('#diffDlgBody').innerText.slice(0,500)`));
await L.shot('diff-split-'+theme); process.exit(0)
