// Passa: działa offline. Pliki apki trzymane w pamięci telefonu, aktualizowane w tle, gdy jest internet.
const CACHE = 'passa-v6';
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
  e.respondWith(
    caches.match(e.request, { ignoreSearch: true }).then(cached => {
      const net = fetch(e.request).then(res => {
        if (res && res.ok && new URL(e.request.url).origin === location.origin) {
          const copy = res.clone();
          caches.open(CACHE).then(c => c.put(e.request, copy));
        }
        return res;
      }).catch(() => cached || caches.match('./index.html'));
      return cached || net;
    })
  );
});
