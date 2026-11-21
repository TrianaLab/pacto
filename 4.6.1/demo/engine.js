// engine.js — runs the Pacto engine (app.wasm) in a Web Worker.
//
// The engine's boot and its heavier answers take whole seconds of CPU. On the
// page's main thread that froze every animation and the loading counter until
// it finished, so the engine runs here and boot.js talks to it by message:
//
//   page -> worker  { id, op: "serve" | "persona", args }
//   worker -> page  { id, result } or { id, error }
//                   { type: "size", bytes }    the download's Content-Length
//                   { type: "ready", ... }     the values main_wasm.go publishes
//                   { type: "refresh" }        the engine's pacto:refresh event
//                   { type: "failed" }
//
// Sibling assets carry the same cache-busting query this script was loaded with.
"use strict";

var v = self.location.search;
importScripts("wasm_exec.js" + v);

self.__pactoOnReady = function () {
  postMessage({
    type: "ready",
    deliveryKey: self.__pactoDeliveryKey,
    pactifyPlan: self.__pactoPactifyPlan,
    persona: typeof self.__pactoPersona === "function",
  });
};

// main_wasm.go dispatches pacto:refresh on its global, which is this worker.
self.addEventListener("pacto:refresh", function () { postMessage({ type: "refresh" }); });

self.onmessage = function (e) {
  var m = e.data;
  try {
    var fn = m.op === "persona" ? self.__pactoPersona : self.__pactoServe;
    postMessage({ id: m.id, result: fn.apply(null, m.args) });
  } catch (err) {
    postMessage({ id: m.id, error: String(err) });
  }
};

function instantiate(url, importObject) {
  // Fetch first and hand the Response to instantiateStreaming, only so the page
  // can show the real transfer size from Content-Length.
  function announce(r) {
    postMessage({ type: "size", bytes: r.headers.get("content-length") });
    return r;
  }
  function viaBuffer() {
    return fetch(url)
      .then(announce)
      .then(function (r) { return r.arrayBuffer(); })
      .then(function (buf) { return WebAssembly.instantiate(buf, importObject); });
  }
  if (typeof WebAssembly.instantiateStreaming === "function") {
    // Falls back when the host serves app.wasm with the wrong MIME type.
    return WebAssembly.instantiateStreaming(fetch(url).then(announce), importObject).catch(viaBuffer);
  }
  return viaBuffer();
}

var go = new Go();
instantiate("app.wasm" + v, go.importObject)
  .then(function (result) { go.run(result.instance); })
  .catch(function (err) {
    console.error("Pacto engine failed to load:", err);
    postMessage({ type: "failed" });
  });
