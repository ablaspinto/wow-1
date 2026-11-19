// The Cellar's only JavaScript (besides htmx). The site works without it; this adds the quick-look
// dialog, the 360° bottle turn, the pointer tilt and the hero lamp.
const calm = matchMedia("(prefers-reduced-motion: reduce)").matches;

// Quick look: htmx loads the wine into #quick-body, then we open the dialog.
const quick = document.getElementById("quick");
document.addEventListener("htmx:afterSwap", (e) => {
  if (e.detail.target.id === "quick-body" && !quick.open) quick.showModal();
});
quick?.addEventListener("click", (e) => { if (e.target === quick) quick.close(); }); // click on the backdrop
quick?.addEventListener("close", () => document.getElementById("quick-body").replaceChildren()); // also stops its spinner

// 360° turn from one cut-out photo. A bottle's outline doesn't change as it turns, so we keep each
// row's outline and slide the picture around it as if it were wrapped on a cylinder. The far side
// shows the front again, like a back label.
// shortcut: a true 360 needs 24–36 turntable photos; swap this for a frame sequence if you ever shoot them.
async function spin(img) {
  // A fresh copy, because with srcset img.naturalWidth is divided by the screen density. crossOrigin lets
  // an image CDN that sends CORS headers allow reading the pixels; otherwise we keep the still photo.
  const pic = new Image();
  pic.crossOrigin = "anonymous";
  pic.src = img.currentSrc || img.src;
  try { await pic.decode(); } catch { return; }
  // Never draw more pixels than the screen shows: the per-frame loop below is per pixel.
  const k = Math.min(1, ((img.clientHeight || pic.naturalHeight) * devicePixelRatio) / pic.naturalHeight);
  const W = Math.round(pic.naturalWidth * k), H = Math.round(pic.naturalHeight * k);
  const canvas = document.createElement("canvas");
  canvas.width = W;
  canvas.height = H;
  const ctx = canvas.getContext("2d");
  ctx.drawImage(pic, 0, 0, W, H);
  let src;
  try { src = ctx.getImageData(0, 0, W, H).data; } catch { return; } // other-domain image without CORS: keep the still photo
  const out = ctx.createImageData(W, H), dst = out.data;

  // For each opaque pixel: its index, its angle on the cylinder, and its row's centre, radius and edges.
  const at = [], ang = [], mid = [], rad = [], lo = [], hi = [];
  for (let y = 0; y < H; y++) {
    let l = -1, r = -1;
    for (let x = 0; x < W; x++) if (src[(y * W + x) * 4 + 3] > 40) { if (l < 0) l = x; r = x; }
    if (r - l < 4) continue;
    const c = (l + r) / 2, h = (r - l) / 2;
    const m = Math.max(1, Math.round(h * 0.08)); // sample the far side from just inside the edge, past any rim highlight
    for (let x = l; x <= r; x++) {
      at.push(y * W + x); ang.push(Math.asin((x - c) / h));
      mid.push(y * W + c); rad.push(h); lo.push(y * W + l + m); hi.push(y * W + r - m);
    }
  }
  const n = at.length, TAU = 2 * Math.PI, Q = Math.PI / 2;
  const draw = (t) => {
    for (let i = 0; i < n; i++) {
      let p = ((((ang[i] + t + Q) % TAU) + TAU) % TAU) - Q; // -90°..270° around the bottle
      if (p > Q) p -= Math.PI;                              // back half repeats the front
      const s = Math.min(hi[i], Math.max(lo[i], Math.round(mid[i] + rad[i] * Math.sin(p)))) * 4, d = at[i] * 4;
      dst[d] = src[s]; dst[d + 1] = src[s + 1]; dst[d + 2] = src[s + 2]; dst[d + 3] = src[d + 3];
    }
    ctx.putImageData(out, 0, 0);
  };

  draw(0);
  img.replaceWith(canvas);

  // Starts with a quick whirl, settles into a slow idle turn; drag to spin it yourself.
  let angle = 0, shown = 0, vel = calm ? 0 : 9, drag = null, last = performance.now();
  const idle = calm ? 0 : 0.45; // radians per second
  canvas.addEventListener("pointerdown", (e) => {
    drag = { x: e.clientX, t: e.timeStamp };
    vel = 0;
    canvas.setPointerCapture(e.pointerId);
  });
  canvas.addEventListener("pointermove", (e) => {
    if (!drag) return;
    const da = (e.clientX - drag.x) * -0.012, dt = Math.max(e.timeStamp - drag.t, 8) / 1000;
    angle += da;
    vel = da / dt;
    drag = { x: e.clientX, t: e.timeStamp };
  });
  const release = () => { drag = null; };
  canvas.addEventListener("pointerup", release);
  canvas.addEventListener("pointercancel", release);

  const tick = (now) => {
    if (!canvas.isConnected) return; // page changed or dialog closed
    const dt = Math.min(now - last, 50) / 1000;
    last = now;
    if (!drag) {
      vel += (idle - vel) * Math.min(1, dt * 1.5);
      angle += vel * dt;
    }
    if (angle !== shown) { draw(angle); shown = angle; }
    requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
}

const initSpin = (root) =>
  root.querySelectorAll?.(".spin-stage img").forEach((img) => img.decode().then(() => spin(img), () => {}));
if (window.htmx) htmx.onLoad(initSpin); else initSpin(document);

// "Enter the cellar": velvet curtains close over the page, then part on the wine list like a show starting.
// The parting is a cross-page view transition (the curtains only exist on this page, so they animate out),
// which Firefox doesn't do yet: there the link just works as a link.
if (!calm && "onpagereveal" in window) {
  document.addEventListener("click", (e) => {
    const a = e.target.closest?.("a[data-curtain]");
    if (!a || e.button || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    if (document.querySelector(".curtain")) return; // already closing
    const c = document.createElement("div");
    c.className = "curtain";
    c.innerHTML = '<div class="curtain-l"></div><div class="curtain-r"></div><div class="curtain-top"></div>';
    document.body.append(c);
    try { sessionStorage.setItem("curtain", "1"); } catch {}
    let gone = false;
    const go = () => { if (!gone) { gone = true; location.assign(a.href); } };
    c.firstChild.addEventListener("animationend", go, { once: true }); // the side curtains finish last
    setTimeout(go, 1000); // in case the animation never runs (tab hidden, motion turned off meanwhile)
  });
  // Coming back with the Back button restores this page from cache with the curtains still shut.
  addEventListener("pageshow", () => document.querySelector(".curtain")?.remove());
}

// Decoration: bottles tilt toward the pointer, and the hero's lamp follows it.
if (!calm) {
  const set = (el, k, v) => el.style.setProperty(k, v);

  document.addEventListener("pointermove", (e) => {
    const tilt = e.target.closest?.("[data-tilt]");
    if (tilt) {
      const r = tilt.getBoundingClientRect();
      set(tilt, "--rx", ((e.clientY - r.top) / r.height - 0.5) * -12 + "deg");
      set(tilt, "--ry", ((e.clientX - r.left) / r.width - 0.5) * 18 + "deg");
    }
    const hero = e.target.closest?.(".hero");
    if (hero) {
      const r = hero.getBoundingClientRect();
      set(hero, "--mx", ((e.clientX - r.left) / r.width) * 100 + "%");
      set(hero, "--my", ((e.clientY - r.top) / r.height) * 100 + "%");
    }
  }, { passive: true });

  document.addEventListener("pointerout", (e) => {
    const tilt = e.target.closest?.("[data-tilt]");
    if (tilt && !tilt.contains(e.relatedTarget)) {
      tilt.style.removeProperty("--rx");
      tilt.style.removeProperty("--ry");
    }
  });
}
