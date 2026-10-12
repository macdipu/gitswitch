// Takes the README screenshots of the desktop app from the demo server
// (see run.sh): every page, light and dark, at 1280x760 on a 2x screen.
import { chromium } from 'playwright';

const base = process.argv[2], out = process.argv[3];
const browser = await chromium.launch();
for (const scheme of ['light', 'dark']) {
  const page = await browser.newPage({ viewport: { width: 1280, height: 760 }, deviceScaleFactor: 2, colorScheme: scheme });
  await page.addInitScript(() => sessionStorage.setItem('gs.setupSkipped', '1'));
  await page.goto(base);
  await page.waitForSelector('#accounts .acct');
  // "signed in as" comes from the saved login test; wait for fonts too.
  await page.evaluate(() => document.fonts.ready);
  const shot = async name => { await page.mouse.move(0, 0); await page.waitForTimeout(300); await page.screenshot({ path: `${out}/web-${name}-${scheme}.png` }); };
  const tab = async t => { await page.click(`nav button[data-tab=${t}]`); await page.waitForTimeout(400); };

  await shot('accounts');
  await tab('repos');
  await page.waitForSelector('#tab-repos table tbody tr');
  await shot('repos');
  await tab('history');
  const acme = await page.$eval('#histRepo', s => [...s.options].find(o => o.text.startsWith('acme-api'))?.value);
  await page.selectOption('#histRepo', acme);
  await page.waitForSelector('.hrow');
  await page.locator('.hrow').nth(1).click();
  await page.waitForSelector('#cdetail .cfiles');
  await shot('history');
  await tab('rules');
  await shot('rules');
  await tab('settings');
  await page.waitForSelector('.checks-sum, .check');
  await shot('settings');
  await page.close();
}
await browser.close();
