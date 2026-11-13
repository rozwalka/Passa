// Passa: działa offline. Pliki apki trzymane w pamięci telefonu, aktualizowane w tle, gdy jest internet.
const CACHE = 'passa-v11';
const FILES = ['./', './index.html', './manifest.webmanifest', './icon-192.png', './icon-512.png', './icon-maskable.png'];
const IMGS = ["./img/abductor.jpg", "./img/abwheel.jpg", "./img/adductor.jpg", "./img/backext.jpg", "./img/bench.jpg", "./img/bulgarian.jpg", "./img/bwsquat.jpg", "./img/cablecross.jpg", "./img/cablecrunch.jpg", "./img/cablecurl.jpg", "./img/cablekick.jpg", "./img/cablelateral.jpg", "./img/cablerow.jpg", "./img/calves.jpg", "./img/chestpress.jpg", "./img/closegrip.jpg", "./img/concentration.jpg", "./img/curl.jpg", "./img/dbbench.jpg", "./img/dbcurl.jpg", "./img/dbincline.jpg", "./img/dbpress.jpg", "./img/dbrow.jpg", "./img/deadlift.jpg", "./img/decline.jpg", "./img/dips.jpg", "./img/facepull.jpg", "./img/flyes.jpg", "./img/french.jpg", "./img/frontsquat.jpg", "./img/goblet.jpg", "./img/hacksquat.jpg", "./img/hammer.jpg", "./img/handstand.jpg", "./img/hipthrust.jpg", "./img/incline.jpg", "./img/kickback.jpg", "./img/lateral.jpg", "./img/legcurl.jpg", "./img/legext.jpg", "./img/legpress.jpg", "./img/legraise.jpg", "./img/machinerow.jpg", "./img/muscleup.jpg", "./img/ohp.jpg", "./img/onearmcable.jpg", "./img/overheadcable.jpg", "./img/pecdeck.jpg", "./img/pistol.jpg", "./img/plank.jpg", "./img/preacher.jpg", "./img/pulldown.jpg", "./img/pulldownclose.jpg", "./img/pulldownsup.jpg", "./img/pullups.jpg", "./img/pushdown.jpg", "./img/pushups.jpg", "./img/rdl.jpg", "./img/reardelt.jpg", "./img/rope.jpg", "./img/ropehammer.jpg", "./img/row.jpg", "./img/shoulderpress.jpg", "./img/shrugs.jpg", "./img/situps.jpg", "./img/smithsquat.jpg", "./img/squat.jpg", "./img/straightarm.jpg", "./img/sumo.jpg", "./img/tbar.jpg", "./img/uprightrow.jpg", "./img/wdips.jpg", "./img/woodchop.jpg", "./img/wpullup.jpg"];

self.addEventListener('install', e => {
  e.waitUntil(caches.open(CACHE).then(async c => {
    await c.addAll(FILES);
    // obrazki ćwiczeń: pojedynczo, brak któregoś nie psuje instalacji
    await Promise.all(IMGS.map(u => c.add(u).catch(() => {})));
  }));
  self.skipWaiting();
});

self.addEventListener('activate', e => {
  e.waitUntil(caches.keys().then(keys => Promise.all(keys.filter(k => k !== CACHE).map(k => caches.delete(k)))));
  self.clients.claim();
});

self.addEventListener('fetch', e => {
  if (e.request.method !== 'GET') return;
  const url = new URL(e.request.url);
  if (url.origin !== location.origin) return;
  const isPage = e.request.mode === 'navigate' || url.pathname.endsWith('/') || url.pathname.endsWith('index.html');
  if (isPage) {
    // strona: najpierw najnowsza z internetu (max 4 s), bez netu z pamięci telefonu
    e.respondWith((async () => {
      try {
        const ctrl = new AbortController(); const t = setTimeout(() => ctrl.abort(), 4000);
        const res = await fetch(e.request, { cache: 'no-cache', signal: ctrl.signal }); clearTimeout(t);
        if (res && res.ok) { const c = await caches.open(CACHE); c.put('./index.html', res.clone()); }
        return res;
      } catch (err) {
        return (await caches.match('./index.html')) || (await caches.match(e.request, { ignoreSearch: true }));
      }
    })());
    return;
  }
  // obrazki i reszta: z pamięci telefonu, w tle odświeżane
  e.respondWith(
    caches.match(e.request, { ignoreSearch: true }).then(cached => {
      const net = fetch(e.request).then(res => {
        if (res && res.ok) { const copy = res.clone(); caches.open(CACHE).then(c => c.put(e.request, copy)); }
        return res;
      }).catch(() => cached);
      return cached || net;
    })
  );
});
