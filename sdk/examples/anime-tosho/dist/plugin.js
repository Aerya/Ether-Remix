var __plugin = (() => {
  var __defProp = Object.defineProperty;
  var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
  var __getOwnPropNames = Object.getOwnPropertyNames;
  var __hasOwnProp = Object.prototype.hasOwnProperty;
  var __defNormalProp = (obj, key, value) => key in obj ? __defProp(obj, key, { enumerable: true, configurable: true, writable: true, value }) : obj[key] = value;
  var __export = (target, all) => {
    for (var name in all)
      __defProp(target, name, { get: all[name], enumerable: true });
  };
  var __copyProps = (to, from, except, desc) => {
    if (from && typeof from === "object" || typeof from === "function") {
      for (let key of __getOwnPropNames(from))
        if (!__hasOwnProp.call(to, key) && key !== except)
          __defProp(to, key, { get: () => from[key], enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable });
    }
    return to;
  };
  var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
  var __publicField = (obj, key, value) => __defNormalProp(obj, typeof key !== "symbol" ? key + "" : key, value);

  // sdk/examples/anime-tosho/plugin.ts
  var plugin_exports = {};
  __export(plugin_exports, {
    DEFAULT_SETTINGS: () => DEFAULT_SETTINGS,
    animeTosho: () => animeTosho,
    base32ToHex: () => base32ToHex,
    buildSearchPlan: () => buildSearchPlan,
    decodeXmlEntities: () => decodeXmlEntities,
    handle: () => handle,
    itemsToStreams: () => itemsToStreams,
    normalizeSettings: () => normalizeSettings,
    parseMagnet: () => parseMagnet,
    parseMediaID: () => parseMediaID,
    parseTorznabFeed: () => parseTorznabFeed,
    validateSettings: () => validateSettings
  });

  // sdk/typescript/src/types.ts
  var PluginHttpError = class extends Error {
    constructor(status, code, message) {
      super(message);
      __publicField(this, "status");
      __publicField(this, "code");
      this.name = "PluginHttpError";
      this.status = status;
      this.code = code;
    }
  };

  // sdk/typescript/src/host.ts
  var MinDataReadBytes = 8 * 1024 * 1024;
  var MaxDataReadBytes = 512 * 1024 * 1024;
  var MaxArchiveReadItems = 4096;
  var MAX_KIND_VISIBLE_IDS = 500;
  var HEADER_VIEWER_TOKEN = "X-Ether-Viewer-Token";
  var B64_ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  var B64_TABLE = (() => {
    const t = new Int8Array(128).fill(-1);
    for (let i = 0; i < B64_ALPHABET.length; i++) {
      t[B64_ALPHABET.charCodeAt(i)] = i;
    }
    return t;
  })();
  function checkDataRange(r) {
    if (!Number.isSafeInteger(r.offset) || r.offset < 0 || !Number.isSafeInteger(r.length) || r.length <= 0 || r.length > MaxDataReadBytes) {
      throw new Error(`Data range needs nonnegative offset and length 1..${MaxDataReadBytes}`);
    }
  }
  function checkArchiveReads(reads) {
    if (!Array.isArray(reads) || reads.length === 0 || reads.length > MaxArchiveReadItems) {
      throw new Error(`archive.read takes 1..${MaxArchiveReadItems} reads`);
    }
    let total = 0;
    for (let i = 0; i < reads.length; i++) {
      const item = reads[i];
      if (!Number.isSafeInteger(item.member) || item.member < 0) {
        throw new Error("archive.read member index must be a nonnegative integer");
      }
      checkDataRange(item);
      total += item.length;
      if (total > MaxDataReadBytes) {
        throw new Error(`archive.read requested bytes exceed maximum readBytes ceiling (${MaxDataReadBytes})`);
      }
    }
  }
  function checkPartIndex(partIndex) {
    if (partIndex !== void 0 && (!Number.isSafeInteger(partIndex) || partIndex < 0)) {
      throw new Error("stream url: partIndex must be a non-negative integer");
    }
  }
  function encodeData(data) {
    const parts = [];
    for (let i = 0; i < data.length; i += 3) {
      const n = data[i] << 16 | (data[i + 1] ?? 0) << 8 | (data[i + 2] ?? 0);
      parts.push(
        B64_ALPHABET[n >>> 18 & 63] + B64_ALPHABET[n >>> 12 & 63] + (i + 1 < data.length ? B64_ALPHABET[n >>> 6 & 63] : "=") + (i + 2 < data.length ? B64_ALPHABET[n & 63] : "=")
      );
    }
    return parts.join("");
  }
  var sharedTextEncoder;
  function encodeText(text) {
    if (text.length === 0) return new Uint8Array(0);
    const g = globalThis;
    if (typeof g.__ether_text_encode === "function") {
      return new Uint8Array(g.__ether_text_encode(text));
    }
    if (typeof TextEncoder === "function") {
      sharedTextEncoder ?? (sharedTextEncoder = new TextEncoder());
      return sharedTextEncoder.encode(text);
    }
    const bytes = [];
    for (let i = 0; i < text.length; i++) {
      let cp = text.charCodeAt(i);
      if (cp >= 55296 && cp <= 56319 && i + 1 < text.length) {
        const lo = text.charCodeAt(i + 1);
        if (lo >= 56320 && lo <= 57343) {
          cp = 65536 + (cp - 55296 << 10) + (lo - 56320);
          i++;
        } else {
          cp = 65533;
        }
      } else if (cp >= 55296 && cp <= 57343) {
        cp = 65533;
      }
      if (cp <= 127) {
        bytes.push(cp);
      } else if (cp <= 2047) {
        bytes.push(192 | cp >> 6, 128 | cp & 63);
      } else if (cp <= 65535) {
        bytes.push(224 | cp >> 12, 128 | cp >> 6 & 63, 128 | cp & 63);
      } else {
        bytes.push(
          240 | cp >> 18,
          128 | cp >> 12 & 63,
          128 | cp >> 6 & 63,
          128 | cp & 63
        );
      }
    }
    return new Uint8Array(bytes);
  }
  function decodeData(r) {
    const text = r.bodyBase64;
    if (text.length === 0) return new Uint8Array(0);
    const g = globalThis;
    if (typeof g.__ether_b64_decode === "function") {
      return new Uint8Array(g.__ether_b64_decode(text));
    }
    const len = text.length;
    if ((len & 3) !== 0) throw new Error("Invalid base64 host result");
    const pad = text.charCodeAt(len - 1) === 61 ? text.charCodeAt(len - 2) === 61 ? 2 : 1 : 0;
    const out = new Uint8Array((len * 3 >> 2) - pad);
    const mainEnd = pad > 0 ? len - 4 : len;
    let pos = 0;
    for (let i = 0; i < mainEnd; i += 4) {
      const c0 = text.charCodeAt(i);
      const c1 = text.charCodeAt(i + 1);
      const c2 = text.charCodeAt(i + 2);
      const c3 = text.charCodeAt(i + 3);
      const b0 = c0 < 128 ? B64_TABLE[c0] : -1;
      const b1 = c1 < 128 ? B64_TABLE[c1] : -1;
      const b2 = c2 < 128 ? B64_TABLE[c2] : -1;
      const b3 = c3 < 128 ? B64_TABLE[c3] : -1;
      if ((b0 | b1 | b2 | b3) < 0) throw new Error("Invalid base64 host result");
      const n = b0 << 18 | b1 << 12 | b2 << 6 | b3;
      out[pos++] = n >> 16 & 255;
      out[pos++] = n >> 8 & 255;
      out[pos++] = n & 255;
    }
    if (pad > 0) {
      const c0 = text.charCodeAt(len - 4);
      const c1 = text.charCodeAt(len - 3);
      const b0 = c0 < 128 ? B64_TABLE[c0] : -1;
      const b1 = c1 < 128 ? B64_TABLE[c1] : -1;
      if ((b0 | b1) < 0) throw new Error("Invalid base64 host result");
      if (pad === 2) {
        out[pos++] = (b0 << 2 | b1 >> 4) & 255;
      } else {
        const c2 = text.charCodeAt(len - 2);
        const b2 = c2 < 128 ? B64_TABLE[c2] : -1;
        if (b2 < 0) throw new Error("Invalid base64 host result");
        const n = b0 << 12 | b1 << 6 | b2;
        out[pos++] = n >> 10 & 255;
        out[pos++] = n >> 2 & 255;
      }
    }
    return out;
  }
  function isHostedRuntime() {
    const g = globalThis;
    return typeof g.__ether_host === "function" || typeof g.ether === "object";
  }
  function createHostedClient() {
    const g = globalThis;
    const callHost = (name, args) => {
      if (typeof g.__ether_host === "function") {
        const raw = g.__ether_host(name, JSON.stringify(args));
        return raw ? JSON.parse(raw) : null;
      }
      throw new Error(`Host service ${name} is not available outside the Ether runtime`);
    };
    return {
      runtime: {
        limits: () => callHost("runtime.limits", [])
      },
      archive: {
        list: (blob) => callHost("archive.list", [blob]),
        open: (blob, member, range) => {
          checkDataRange(range);
          if (typeof g.__ether_host_binary === "function") {
            return new Uint8Array(g.__ether_host_binary("archive.open", JSON.stringify([blob, member, range])));
          }
          return decodeData(callHost("archive.open", [blob, member, range]));
        },
        read: (blob, reads) => {
          checkArchiveReads(reads);
          if (typeof g.__ether_host_binary === "function") {
            const packed = g.__ether_host_binary("archive.read", JSON.stringify([blob, reads]));
            const meta = JSON.parse(packed.metaJson);
            return meta.map((m) => ({ index: m.index, member: m.member, size: m.size, body: new Uint8Array(packed.buffer, m.offset, m.size) }));
          }
          const rows = callHost("archive.read", [blob, reads]);
          return rows.map((row) => ({ index: row.index, member: row.member, size: row.size, body: decodeData(row) }));
        },
        extract: (blob, member, opts) => callHost("archive.extract", opts == null ? [blob, member] : [blob, member, opts])
      },
      cache: {
        put: (lib, name, data) => callHost("cache.put", [lib, name, { bodyBase64: encodeData(data) }]).blob,
        open: (blob) => {
          const r = callHost("cache.open", [blob]);
          return r.bodyBase64 ? decodeData({ bodyBase64: r.bodyBase64 }) : encodeText(r.body ?? "");
        },
        read: (blob, range) => {
          checkDataRange(range);
          if (typeof g.__ether_host_binary === "function") {
            return new Uint8Array(g.__ether_host_binary("cache.read", JSON.stringify([blob, range])));
          }
          return decodeData(callHost("cache.read", [blob, range]));
        }
      },
      settings: { get: () => callHost("settings.get", []) },
      kv: {
        get(key) {
          if (g.ether?.kv) return g.ether.kv.get(key) ?? null;
          return callHost("kv.get", [key]);
        },
        put(key, value) {
          if (g.ether?.kv) {
            g.ether.kv.put(key, String(value));
            return;
          }
          callHost("kv.put", [key, String(value)]);
        },
        delete(key) {
          if (g.ether?.kv) {
            g.ether.kv.delete(key);
            return;
          }
          callHost("kv.delete", [key]);
        },
        list(prefix = "") {
          if (g.ether?.kv) return g.ether.kv.list(prefix) ?? [];
          return callHost("kv.list", [prefix]) ?? [];
        },
        usage() {
          if (g.ether?.kv?.usage) return g.ether.kv.usage();
          return callHost("kv.usage", []);
        }
      },
      badges: {
        set(setKey, entries) {
          if (g.ether?.badges) {
            g.ether.badges.set(setKey, entries);
            return;
          }
          callHost("badges.set", [setKey, entries]);
        },
        clear(setKey) {
          if (g.ether?.badges) {
            g.ether.badges.clear(setKey);
            return;
          }
          callHost("badges.clear", [setKey]);
        },
        usage() {
          if (g.ether?.badges) return g.ether.badges.usage();
          return callHost("badges.usage", []);
        }
      },
      secret: {
        get(name) {
          try {
            if (g.ether?.secret) return g.ether.secret.get(name) ?? "";
            return callHost("secret.get", [name]) ?? "";
          } catch (err2) {
            if (err2 instanceof Error && /is not saved/i.test(err2.message)) return "";
            throw err2;
          }
        }
      },
      http: {
        hash: (req) => callHost("http.hash", [req]),
        fetchToCache: (req) => callHost("http.fetchToCache", [req]),
        fetch(req) {
          const normalized = {
            timeoutMs: req.timeoutMs,
            method: req.method ?? "GET",
            url: req.url,
            headers: req.headers ?? {},
            ...req.bodyBase64 !== void 0 ? { bodyBase64: req.bodyBase64 } : { body: req.body ?? "" }
          };
          if (g.ether?.http) return g.ether.http.fetch(normalized);
          return callHost("http.fetch", [normalized]);
        }
      },
      catalog: {
        search(query, limit = 50, offset = 0) {
          if (typeof g.__ether_host === "function") {
            return callHost("catalog.search", [
              query,
              limit,
              offset
            ]);
          }
          if (g.ether?.catalog) return g.ether.catalog.search(query, limit, offset);
          return { titles: [] };
        },
        get(id) {
          if (g.ether?.catalog) return g.ether.catalog.get(id);
          return callHost("catalog.get", [id]);
        }
      },
      viewers: {
        granted() {
          return callHost("viewers.granted", []) ?? [];
        },
        history(viewerId, limit = 50, offset = 0) {
          if (typeof g.__ether_host === "function") {
            return callHost("viewers.history", [viewerId, limit, offset]) ?? [];
          }
          if (g.ether?.viewers) return g.ether.viewers.history(viewerId, limit, offset) ?? [];
          return [];
        },
        setWatchState(viewerId, titleId, watched) {
          if (typeof g.__ether_host === "function") {
            callHost("viewers.setWatchState", [viewerId, titleId, watched]);
            return;
          }
          if (g.ether?.viewers) {
            g.ether.viewers.setWatchState(viewerId, titleId, watched);
            return;
          }
          callHost("viewers.setWatchState", [viewerId, titleId, watched]);
        }
      },
      collections: {
        upsert(collection) {
          if (g.ether?.collections) {
            g.ether.collections.upsert(collection);
            return;
          }
          callHost("collections.upsert", [collection]);
        }
      },
      folders: {
        list(root, path = "") {
          if (g.ether?.folders) return g.ether.folders.list(root, path) ?? [];
          return callHost("folders.list", [root, path]) ?? [];
        },
        hashStart(root, path) {
          if (g.ether?.folders) return g.ether.folders.hashStart(root, path);
          return callHost("folders.hashStart", [root, path]);
        },
        hashPoll(id) {
          if (g.ether?.folders) return g.ether.folders.hashPoll(id);
          return callHost("folders.hashPoll", [id]);
        },
        hashCancel(id) {
          if (g.ether?.folders) return g.ether.folders.hashCancel(id);
          return callHost("folders.hashCancel", [id]);
        },
        hash(root, path) {
          if (g.ether?.folders) return g.ether.folders.hash(root, path);
          return callHost("folders.hash", [root, path]);
        },
        stat(root, path) {
          if (g.ether?.folders) return g.ether.folders.stat(root, path);
          return callHost("folders.stat", [root, path]);
        }
      },
      notify: {
        owner(text) {
          if (g.ether?.notify) {
            g.ether.notify.owner(text);
            return;
          }
          callHost("notify.owner", [text]);
        }
      },
      kinds: {
        upsert(kind, items) {
          if (g.ether?.kinds?.upsert) {
            const r2 = g.ether.kinds.upsert(kind, items);
            return typeof r2 === "number" ? r2 : r2?.accepted ?? 0;
          }
          const r = callHost("kinds.upsert", [kind, items]);
          return r?.accepted ?? 0;
        },
        delete(kind, externalIds) {
          if (g.ether?.kinds?.delete) {
            g.ether.kinds.delete(kind, externalIds);
            return;
          }
          callHost("kinds.delete", [kind, externalIds]);
        },
        items(kind, cursor = "", limit = 200) {
          if (g.ether?.kinds?.items) {
            return g.ether.kinds.items(kind, cursor, limit) ?? { items: [] };
          }
          return callHost("kinds.items", [kind, cursor, limit]) ?? { items: [] };
        },
        visible(kind, ids, _viewerToken) {
          if (ids.length > MAX_KIND_VISIBLE_IDS) {
            throw new Error(`kinds.visible: batch larger than ${MAX_KIND_VISIBLE_IDS} ids`);
          }
          if (g.ether?.kinds?.visible) {
            return g.ether.kinds.visible(kind, ids) ?? [];
          }
          return callHost("kinds.visible", [kind, ids]) ?? [];
        },
        browse(kind, query = {}, _viewerToken) {
          if (g.ether?.kinds?.browse) {
            return g.ether.kinds.browse(kind, query) ?? { items: [] };
          }
          return callHost("kinds.browse", [kind, query]) ?? { items: [] };
        },
        resolveSources(q) {
          const r = callHost("sources.resolve", [q]);
          return r?.candidates ?? [];
        }
      },
      frontend: {
        streamUrl(viewerId, itemRef, partIndex) {
          void viewerId;
          checkPartIndex(partIndex);
          const r = callHost(
            "frontend.streamURL",
            partIndex === void 0 ? [itemRef] : [itemRef, partIndex]
          );
          return r?.url ?? "";
        }
      },
      log(level, message, attrs) {
        if (typeof g.__ether_host === "function") {
          try {
            g.__ether_host(
              "log",
              JSON.stringify(attrs ? [level, message, attrs] : [level, message])
            );
          } catch {
          }
          return;
        }
        if (g.ether?.log) {
          g.ether.log(level, message, attrs ? JSON.stringify(attrs) : "");
        }
      },
      abort(message = "aborted") {
        if (g.ether?.abort) {
          return g.ether.abort(message);
        }
        throw new Error(
          "__ETHER_FAULT__" + JSON.stringify({ code: "aborted", message: String(message) })
        );
      }
    };
  }
  var ether = isHostedRuntime() ? createHostedClient() : createRemoteHostClient();
  function readEnv(name) {
    const g = globalThis;
    return g.process?.env?.[name];
  }
  function createRemoteHostClient(opts = {}) {
    const rawBase = opts.baseUrl ?? readEnv("ETHER_HOST_URL") ?? "http://127.0.0.1:32450";
    const base = rawBase.replace(/\/+$/, "");
    const credential = opts.credential ?? readEnv("ETHER_PLUGIN_CREDENTIAL") ?? "";
    const doFetch = opts.fetchImpl ?? fetch;
    const callJson = async (method, path, body, allow404 = false, extraHeaders) => {
      const headers = {
        "X-Ether-Plugin-Credential": credential,
        Accept: "application/json",
        ...extraHeaders ?? {}
      };
      let payload;
      if (body !== void 0) {
        headers["Content-Type"] = "application/json";
        payload = JSON.stringify(body);
      }
      const resp = await doFetch(`${base}${path}`, {
        method,
        headers,
        body: payload
      });
      if (allow404 && resp.status === 404) {
        return null;
      }
      const text = await resp.text();
      const parsed = text ? JSON.parse(text) : null;
      if (!resp.ok) {
        const errObj = parsed && typeof parsed.error === "object" ? parsed.error : null;
        throw new Error(
          errObj?.message ?? `Host call ${method} ${path} failed with HTTP ${resp.status}`
        );
      }
      return parsed;
    };
    return {
      runtime: {
        limits: async () => await callJson("GET", "/ether/plugin-host/limits") ?? {}
      },
      archive: {
        list: async (blob) => await callJson("POST", "/ether/plugin-host/data/archive.list", [blob]) ?? [],
        open: async (blob, member, range) => {
          checkDataRange(range);
          return decodeData(await callJson("POST", "/ether/plugin-host/data/archive.open", [blob, member, range]));
        },
        read: async (blob, reads) => {
          checkArchiveReads(reads);
          const rows = await callJson("POST", "/ether/plugin-host/data/archive.read", [blob, reads]) ?? [];
          return rows.map((row) => ({ index: row.index, member: row.member, size: row.size, body: decodeData(row) }));
        },
        extract: async (blob, member, opts2) => await callJson("POST", "/ether/plugin-host/data/archive.extract", opts2 == null ? [blob, member] : [blob, member, opts2])
      },
      ...credential ? { settings: { get: async () => (await callJson("GET", "/ether/plugin-host/settings"))?.settings ?? {} } } : {},
      kv: {
        async get(key) {
          const res = await callJson(
            "GET",
            `/ether/plugin-host/kv/${encodeURIComponent(key)}`,
            void 0,
            true
          );
          return res ? res.value : null;
        },
        async put(key, value) {
          await callJson("PUT", `/ether/plugin-host/kv/${encodeURIComponent(key)}`, {
            value: String(value)
          });
        },
        async delete(key) {
          await callJson("DELETE", `/ether/plugin-host/kv/${encodeURIComponent(key)}`);
        },
        async list(prefix = "") {
          const q = prefix ? `?prefix=${encodeURIComponent(prefix)}` : "";
          const res = await callJson("GET", `/ether/plugin-host/kv${q}`);
          return res?.keys ?? [];
        },
        async usage() {
          return await callJson("GET", "/ether/plugin-host/kv-usage");
        }
      },
      badges: {
        async set(setKey, entries) {
          await callJson("PUT", `/ether/plugin-host/badges/${encodeURIComponent(setKey)}`, {
            entries
          });
        },
        async clear(setKey) {
          await callJson("DELETE", `/ether/plugin-host/badges/${encodeURIComponent(setKey)}`);
        },
        async usage() {
          return await callJson("GET", "/ether/plugin-host/badges-usage");
        }
      },
      secret: {
        get(_name) {
          return Promise.reject(
            new Error(
              "secret.get is only available in hosted plugins; remote plugins receive secrets via /ether/configure"
            )
          );
        }
      },
      http: {
        hash: async (req) => await callJson("POST", "/ether/plugin-host/data/http.hash", [req]),
        fetchToCache: async (req) => await callJson("POST", "/ether/plugin-host/data/http.fetchToCache", [req]),
        async fetch(req) {
          const resp = await doFetch(req.url, {
            method: req.method ?? "GET",
            headers: req.headers,
            body: req.body,
            signal: req.timeoutMs && req.timeoutMs > 0 ? AbortSignal.timeout(req.timeoutMs) : void 0
          });
          const outHeaders = {};
          resp.headers.forEach((v, k) => {
            outHeaders[k] = v;
          });
          return {
            status: resp.status,
            headers: outHeaders,
            body: await resp.text()
          };
        }
      },
      catalog: {
        async search(query, limit = 50, offset = 0) {
          const params = new URLSearchParams({
            q: query,
            limit: String(limit),
            offset: String(offset)
          });
          const res = await callJson(
            "GET",
            `/ether/plugin-host/catalog/search?${params.toString()}`
          );
          return res ?? { titles: [] };
        },
        async get(id) {
          return callJson(
            "GET",
            `/ether/plugin-host/catalog/${encodeURIComponent(id)}`,
            void 0,
            true
          );
        }
      },
      viewers: {
        async granted() {
          return await callJson("GET", "/ether/plugin-host/viewers/granted") ?? [];
        },
        async history(viewerId, limit = 50, offset = 0) {
          const params = new URLSearchParams({
            limit: String(limit),
            offset: String(offset)
          });
          const res = await callJson(
            "GET",
            `/ether/plugin-host/viewers/${viewerId}/history?${params.toString()}`
          );
          return res ?? [];
        },
        async setWatchState(viewerId, titleId, watched) {
          await callJson("POST", "/ether/plugin-host/viewers/watch-state", {
            viewerId,
            titleId,
            watched
          });
        }
      },
      collections: {
        async upsert(collection) {
          await callJson("POST", "/ether/plugin-host/collections/upsert", collection);
        }
      },
      folders: {
        list() {
          return Promise.reject(
            new Error(
              "folders.list is for hosted plugins; sidecar plugins read their local fileRoot directly"
            )
          );
        },
        hashStart() {
          return Promise.reject(new Error("folders.hash requires the hosted runtime"));
        },
        hashPoll() {
          return Promise.reject(new Error("folders.hash requires the hosted runtime"));
        },
        hashCancel() {
          return Promise.reject(new Error("folders.hash requires the hosted runtime"));
        },
        hash() {
          return Promise.reject(new Error("folders.hash requires the hosted runtime"));
        },
        stat() {
          return Promise.reject(
            new Error(
              "folders.stat is for hosted plugins; sidecar plugins read their local fileRoot directly"
            )
          );
        }
      },
      notify: {
        async owner(text) {
          await callJson("POST", "/ether/plugin-host/notify", { text });
        }
      },
      kinds: {
        async upsert(kind, items) {
          const r = await callJson("POST", `/ether/plugin-host/kinds/${encodeURIComponent(kind)}/upsert`, { items });
          return r?.accepted ?? 0;
        },
        async delete(kind, externalIds) {
          await callJson("POST", `/ether/plugin-host/kinds/${encodeURIComponent(kind)}/delete`, { externalIds });
        },
        async items(kind, cursor = "", limit = 200) {
          return await callJson("GET", `/ether/plugin-host/kinds/${encodeURIComponent(kind)}/items?cursor=${encodeURIComponent(cursor)}&limit=${limit}`) ?? { items: [] };
        },
        async visible(kind, ids, viewerToken) {
          const tok = (viewerToken ?? opts.viewerToken ?? "").trim();
          if (!tok) {
            throw new Error("kinds.visible: no authenticated frontend adapter viewer token on this request");
          }
          if (ids.length > MAX_KIND_VISIBLE_IDS) {
            throw new Error(`kinds.visible: batch larger than ${MAX_KIND_VISIBLE_IDS} ids`);
          }
          const r = await callJson(
            "POST",
            `/ether/plugin-host/kinds/${encodeURIComponent(kind)}/visible`,
            { ids },
            false,
            { [HEADER_VIEWER_TOKEN]: tok }
          );
          return r?.ids ?? [];
        },
        async browse(kind, query = {}, viewerToken) {
          const tok = (viewerToken ?? opts.viewerToken ?? "").trim();
          if (!tok) {
            throw new Error("kinds.browse: no authenticated frontend adapter viewer token on this request");
          }
          const r = await callJson(
            "POST",
            `/ether/plugin-host/kinds/${encodeURIComponent(kind)}/browse`,
            query,
            false,
            { [HEADER_VIEWER_TOKEN]: tok }
          );
          return r ?? { items: [] };
        },
        async resolveSources(q) {
          const r = await callJson("POST", "/ether/plugin-host/sources/resolve", q);
          return r?.candidates ?? [];
        }
      },
      cache: {
        read: async (blob, range) => {
          checkDataRange(range);
          return decodeData(await callJson("POST", "/ether/plugin-host/data/cache.read", [blob, range]));
        },
        async put(libraryKey, name, data) {
          const resp = await doFetch(`${base}/ether/plugin-host/cache/${encodeURIComponent(libraryKey)}?name=${encodeURIComponent(name)}`, {
            method: "POST",
            headers: credential ? { "X-Ether-Plugin-Credential": credential } : {},
            body: data
          });
          if (!resp.ok) throw new Error(`plugin-host cache put: status ${resp.status}`);
          return (await resp.json()).blob ?? "";
        },
        async open(blobRef) {
          const resp = await doFetch(`${base}/ether/plugin-host/cache/${blobRef.replace(/^\//, "")}`, {
            headers: credential ? { "X-Ether-Plugin-Credential": credential } : {}
          });
          if (!resp.ok) throw new Error(`plugin-host cache open: status ${resp.status}`);
          return new Uint8Array(await resp.arrayBuffer());
        }
      },
      frontend: {
        async streamUrl(viewerId, itemRef, partIndex) {
          checkPartIndex(partIndex);
          const r = await callJson("POST", "/ether/plugin-host/frontend/stream-url", {
            viewerId,
            itemRef,
            ...partIndex === void 0 ? {} : { partIndex }
          });
          return r?.url ?? "";
        }
      },
      async log(level, message, attrs) {
        await callJson("POST", "/ether/plugin-host/log", { level, message, attrs });
      },
      abort(message = "aborted") {
        throw new Error(message);
      }
    };
  }

  // sdk/typescript/src/kinds.ts
  function createKindHandlers() {
    return { catalog: /* @__PURE__ */ new Map(), resolve: /* @__PURE__ */ new Map(), match: /* @__PURE__ */ new Map(), frontend: /* @__PURE__ */ new Map() };
  }
  function ok(body) {
    return { status: 200, headers: { "Content-Type": "application/json" }, body };
  }
  function err(status, code, message) {
    return { status, headers: { "Content-Type": "application/json" }, body: { error: { code, message } } };
  }
  function parseBody(body) {
    if (typeof body === "string" && body.trim()) {
      const parsed = JSON.parse(body);
      return parsed && typeof parsed === "object" ? parsed : {};
    }
    if (body && typeof body === "object") return body;
    return {};
  }
  function frontendQuery(req) {
    const query = new URLSearchParams();
    if (req.query) {
      for (const [k, v] of Object.entries(req.query)) {
        if (Array.isArray(v)) {
          if (v.length > 0) query.set(k, String(v[0]));
        } else if (v !== void 0 && v !== null) {
          query.set(k, String(v));
        }
      }
    }
    const qIndex = (req.path || "").indexOf("?");
    if (qIndex >= 0) {
      for (const [k, v] of new URLSearchParams((req.path || "").slice(qIndex + 1))) {
        if (!query.has(k)) query.set(k, v);
      }
    }
    return query;
  }
  async function dispatchKinds(handlers, req, ctx) {
    const method = (req.method || "GET").toUpperCase();
    const path = (req.path || "/").split("?")[0] ?? "/";
    if (!path.startsWith("/ether/v1/")) return null;
    const parts = path.slice("/ether/v1/".length).split("/").map((p) => decodeURIComponent(p));
    if (parts.length >= 2 && parts[0] === "frontend") {
      const adapter = handlers.frontend.get(parts[1] ?? "");
      if (!adapter) return err(404, "not_found", `No frontend adapter is registered for "${parts[1]}".`);
      const headers = {};
      for (const [k, v] of Object.entries(req.headers ?? {})) headers[k] = Array.isArray(v) ? v[0] ?? "" : v;
      let subPath = `/${parts.slice(2).join("/")}`;
      const query = frontendQuery(req);
      const qs = query.toString();
      if (qs) subPath += `?${qs}`;
      const res = await adapter.handle(method, subPath, headers, typeof req.body === "string" ? req.body : "", ctx);
      if (res.body.length > 10 * 1024 * 1024) return err(502, "adapter_error", "Frontend adapter response is larger than 10 MiB; serve media through Ether stream URLs.");
      return { status: res.status, headers: res.headers ?? {}, body: method === "HEAD" ? "" : res.body };
    }
    if (method !== "GET" && method !== "POST") {
      return err(405, "method_not_allowed", "Kind routes use GET or POST.");
    }
    let body;
    try {
      body = parseBody(req.body);
    } catch {
      return err(400, "invalid_json", "Request body must be valid JSON.");
    }
    if (parts.length === 3 && parts[0] === "setup" && parts[2] === "test") {
      if (!handlers.setup) return err(404, "not_found", "Setup test handler is not registered.");
      const input = { step: String(body.step || parts[1]), fields: body.fields };
      return ok(await handlers.setup.testSetup(input, ctx));
    }
    if (parts.length < 3 || parts[0] !== "kinds") return null;
    const kind = parts[1] ?? "";
    const tail = parts.slice(2);
    const browseReq = { ...body, kind };
    const page = async (p) => {
      const result = { ...await p };
      const limit = Number(browseReq.limit || 200);
      result.items = (result.items ?? []).slice(0, limit);
      if (!result.nextCursor) delete result.nextCursor;
      return ok(result);
    };
    if (tail.length === 1 && (tail[0] === "browse" || tail[0] === "search")) {
      const provider = handlers.catalog.get(kind);
      if (!provider) return err(404, "not_found", `No catalogue provider is registered for kind "${kind}".`);
      if (tail[0] === "search") {
        if (!provider.search) return err(501, "not_implemented", "This provider does not search.");
        return page(provider.search(browseReq, ctx));
      }
      return page(provider.browse(browseReq, ctx));
    }
    if (tail.length === 1 && tail[0] === "expand") {
      const provider = handlers.catalog.get(kind);
      if (!provider?.expand) return err(501, "not_implemented", "This provider does not expand facets.");
      const result = { ...await provider.expand({ ...body, kind }, ctx) };
      if (!result.job) result.items = result.items ?? [];
      return ok(result);
    }
    if (tail.length === 2 && tail[0] === "items") {
      const provider = handlers.catalog.get(kind);
      if (!provider) return err(404, "not_found", `No catalogue provider is registered for kind "${kind}".`);
      return ok(await provider.detail(kind, tail[1] ?? "", ctx));
    }
    if (tail.length === 1 && tail[0] === "changes") {
      const provider = handlers.catalog.get(kind);
      if (!provider?.changes) return err(501, "not_implemented", "This provider has no changes feed.");
      const result = { ...await provider.changes({ kind, cursor: body.cursor, limit: body.limit }, ctx) };
      result.upserts = result.upserts ?? [];
      return ok(result);
    }
    if (tail.length === 1 && tail[0] === "resolve") {
      const resolver = handlers.resolve.get(kind);
      if (!resolver) return err(404, "not_found", `No resolver is registered for kind "${kind}".`);
      const result = { ...await resolver.resolve({ ...body, kind }, ctx) };
      result.sources = result.sources ?? [];
      return ok(result);
    }
    if (tail.length === 1 && tail[0] === "match") {
      const matcher = handlers.match.get(kind);
      if (!matcher) return err(404, "not_found", `No matcher is registered for kind "${kind}".`);
      const result = { ...await matcher.match({ ...body, kind }, ctx) };
      result.matches = result.matches ?? [];
      return ok(result);
    }
    return err(404, "not_found", "No kind route matched.");
  }

  // sdk/typescript/src/i18n.ts
  function tRef(key) {
    return `$t:${key}`;
  }
  function createI18n(strings = {}, defaultLocale = "en") {
    const resolveKey = (rawKey, params, locale = defaultLocale) => {
      const key = rawKey.startsWith("$t:") ? rawKey.slice(3) : rawKey;
      const base = locale ? locale.split("-")[0] ?? "" : "";
      const candidates = [locale, base, "en"];
      let template;
      for (const loc of candidates) {
        if (!loc) continue;
        const table = strings[loc];
        if (table && Object.prototype.hasOwnProperty.call(table, key)) {
          template = table[key];
          break;
        }
      }
      if (template === void 0) {
        template = key;
      }
      if (!params) return template;
      return template.replace(/\{([A-Za-z0-9_]+)\}/g, (match, name) => {
        const val = params[name];
        return val !== void 0 ? String(val) : match;
      });
    };
    return {
      t: resolveKey,
      ref: tRef,
      catalog: () => strings
    };
  }

  // sdk/typescript/src/settings.ts
  function collectSettingFields(schema) {
    if (!schema || typeof schema !== "object") return [];
    const out = [];
    const walk = (node) => {
      if (!node || typeof node !== "object") return;
      if (Array.isArray(node)) {
        for (const item of node) walk(item);
        return;
      }
      const obj = node;
      const key = typeof obj.key === "string" ? obj.key : typeof obj.name === "string" ? obj.name : typeof obj.id === "string" ? obj.id : typeof obj.field === "string" ? obj.field : "";
      const kind = typeof obj.kind === "string" ? obj.kind : "";
      if (key && kind && kind !== "group") {
        out.push({
          key,
          kind,
          label: typeof obj.label === "string" ? obj.label : void 0,
          hint: typeof obj.hint === "string" ? obj.hint : void 0,
          required: obj.required === true,
          default: obj.default,
          min: typeof obj.min === "number" ? obj.min : void 0,
          max: typeof obj.max === "number" ? obj.max : void 0
        });
      }
      for (const v of Object.values(obj)) {
        if (v && typeof v === "object") walk(v);
      }
    };
    walk(schema);
    return out;
  }
  function decodeSettings(schema, incoming, defaults) {
    const fields = collectSettingFields(schema);
    const result = { ...defaults };
    const errors = {};
    for (const field of fields) {
      if (field.default !== void 0 && result[field.key] === void 0) {
        result[field.key] = field.default;
      }
    }
    if (incoming && typeof incoming === "object") {
      for (const [k, v] of Object.entries(incoming)) {
        result[k] = v;
      }
    }
    for (const field of fields) {
      const val = result[field.key];
      const ptr = `/${field.key}`;
      if (field.required && (val === void 0 || val === null || val === "")) {
        errors[ptr] = "is required";
        continue;
      }
      if (val === void 0 || val === null || val === "") {
        continue;
      }
      switch (field.kind) {
        case "number":
        case "bytes":
        case "viewer": {
          const num = typeof val === "number" ? val : Number(val);
          if (!Number.isFinite(num)) {
            errors[ptr] = "must be a number";
          } else {
            if (field.min !== void 0 && num < field.min) {
              errors[ptr] = `must be at least ${field.min}`;
            }
            if (field.max !== void 0 && num > field.max) {
              errors[ptr] = `must be at most ${field.max}`;
            }
            result[field.key] = num;
          }
          break;
        }
        case "bool": {
          if (typeof val !== "boolean") {
            errors[ptr] = "must be true or false";
          }
          break;
        }
        case "list":
        case "multiSelect":
        case "orderedList": {
          if (!Array.isArray(val)) {
            errors[ptr] = "must be a list";
          }
          break;
        }
        case "url": {
          if (typeof val !== "string" || !val.startsWith("http://") && !val.startsWith("https://")) {
            errors[ptr] = "must be an http or https URL";
          }
          break;
        }
        default:
          break;
      }
    }
    return {
      settings: result,
      errors
    };
  }

  // sdk/typescript/src/router.ts
  function getHeader(headers, name) {
    if (!headers) return void 0;
    const target = name.toLowerCase();
    for (const [k, v] of Object.entries(headers)) {
      if (k.toLowerCase() === target) {
        return Array.isArray(v) ? v[0] : v;
      }
    }
    return void 0;
  }
  function parseUrlPathAndQuery(rawPath, rawQuery) {
    let pathOnly = rawPath || "/";
    const query = {};
    if (rawQuery) {
      for (const [k, v] of Object.entries(rawQuery)) {
        if (Array.isArray(v)) {
          if (v.length > 0) query[k] = String(v[0]);
        } else if (v !== void 0 && v !== null) {
          query[k] = String(v);
        }
      }
    }
    const qIndex = pathOnly.indexOf("?");
    if (qIndex >= 0) {
      const qs = pathOnly.slice(qIndex + 1);
      pathOnly = pathOnly.slice(0, qIndex) || "/";
      const params = parseParams(qs);
      Object.entries(params).forEach(([k, v]) => {
        if (query[k] === void 0) {
          query[k] = v;
        }
      });
    }
    if (!pathOnly.startsWith("/")) {
      pathOnly = "/" + pathOnly;
    }
    return { path: pathOnly, query };
  }
  function parseJsonBody(body) {
    if (body === void 0 || body === null || body === "") {
      return {};
    }
    if (typeof body === "string") {
      const trimmed = body.trim();
      if (!trimmed) return {};
      const parsed = JSON.parse(trimmed);
      if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
        return parsed;
      }
      return {};
    }
    if (typeof body === "object" && !Array.isArray(body)) {
      return body;
    }
    return {};
  }
  function okResponse(body, headers, status = 200) {
    return {
      status,
      headers: {
        "Content-Type": "application/json",
        ...headers ?? {}
      },
      body
    };
  }
  function errorResponse(status, code, message) {
    return {
      status,
      headers: { "Content-Type": "application/json" },
      body: { error: { code, message } }
    };
  }
  function isEtherHostFault(err2) {
    if (!err2) return false;
    const msg = err2 instanceof Error ? err2.message : String(err2);
    return msg.includes("__ETHER_FAULT__");
  }
  function parseParams(input) {
    const result = {};
    for (const part of input.split("&")) {
      if (!part) continue;
      const index = part.indexOf("=");
      const decode = (value) => decodeURIComponent(value.replace(/\+/g, " "));
      result[decode(index < 0 ? part : part.slice(0, index))] = decode(index < 0 ? "" : part.slice(index + 1));
    }
    return result;
  }
  function parseExtraSegment(rawExtra) {
    const out = {};
    if (!rawExtra) return out;
    const params = parseParams(rawExtra);
    Object.entries(params).forEach(([k, v]) => {
      out[k] = v;
    });
    return out;
  }
  function matchStremioRoute(path, query) {
    if (!path.endsWith(".json")) return null;
    const trimmed = path.slice(1, -5);
    const parts = trimmed.split("/");
    if (parts.length < 3 || parts.length > 4) return null;
    const resource = parts[0];
    if (resource !== "catalog" && resource !== "meta" && resource !== "stream" && resource !== "subtitles") {
      return null;
    }
    const type = decodeURIComponent(parts[1] ?? "");
    const id = decodeURIComponent(parts[2] ?? "");
    const extra = parseExtraSegment(parts[3]);
    for (const [k, v] of Object.entries(query)) {
      if (extra[k] === void 0) {
        extra[k] = v;
      }
    }
    return { resource, type, id, extra };
  }
  function buildInlineStrings(manifest, explicit) {
    const catalog = {};
    if (manifest.ether?.strings) {
      for (const [loc, val] of Object.entries(manifest.ether.strings)) {
        if (val && typeof val === "object") {
          catalog[loc] = { ...val };
        }
      }
    }
    if (explicit) {
      for (const [loc, table] of Object.entries(explicit)) {
        catalog[loc] = { ...catalog[loc] ?? {}, ...table };
      }
    }
    return catalog;
  }
  function plugin(def) {
    if (!def || !def.manifest) {
      throw new Error("Plugin manifest is required.");
    }
    const manifest = def.manifest;
    if (!manifest.id || !manifest.name || !manifest.version) {
      throw new Error("Plugin manifest requires id, name, and version.");
    }
    const routes = def.routes ?? {};
    const kindHandlers = createKindHandlers();
    const i18n = createI18n(
      buildInlineStrings(manifest, def.strings),
      def.defaultLocale ?? "en"
    );
    const settingsSchema = manifest.ether?.settings;
    let currentSettings = decodeSettings(
      settingsSchema,
      void 0,
      def.defaultSettings
    ).settings;
    const pageRoutes = /* @__PURE__ */ new Map();
    if (manifest.ether?.contributes?.pages) {
      for (const p of manifest.ether.contributes.pages) {
        if (p.route) {
          pageRoutes.set(p.route, p.key);
        }
      }
    }
    if (manifest.ether?.contributes?.slots) {
      for (const s of manifest.ether.contributes.slots) {
        if (s.route) {
          pageRoutes.set(s.route, s.key);
        }
      }
    }
    let settingsFailureLogged = false;
    function buildContext(req) {
      const { query } = parseUrlPathAndQuery(req.path, req.query);
      const localeHeader = req.locale ?? query.locale ?? getHeader(req.headers, "X-Ether-Locale") ?? getHeader(req.headers, "Accept-Language") ?? def.defaultLocale ?? "en";
      const locale = localeHeader.split(",")[0]?.trim() || "en";
      const pickToken = getHeader(req.headers, "X-Ether-Pick");
      const requestId = getHeader(req.headers, "X-Ether-Request-ID");
      const viewerToken = getHeader(req.headers, HEADER_VIEWER_TOKEN);
      const hostClient = def.host ?? (isHostedRuntime() ? createHostedClient() : createRemoteHostClient(
        viewerToken ? { ...def.remoteHostOptions ?? {}, viewerToken } : def.remoteHostOptions
      ));
      const makeContext = (settings) => ({
        manifest,
        settings,
        locale,
        pickToken,
        requestId,
        headers: req.headers ?? {},
        query,
        ether: hostClient,
        i18n,
        t: (key, params) => i18n.t(key, params, locale)
      });
      if (!hostClient.settings || req.path === "/ether/configure") return makeContext(currentSettings);
      const fallback = () => {
        if (!settingsFailureLogged) {
          console.warn("plugin settings lookup failed");
          settingsFailureLogged = true;
        }
        return makeContext({});
      };
      let fresh;
      try {
        fresh = hostClient.settings.get();
      } catch {
        return fallback();
      }
      const resolve = (v) => {
        const merged = { ...v };
        for (const field of collectSettingFields(settingsSchema)) {
          if (field.kind === "secret" && currentSettings[field.key] !== void 0) merged[field.key] = currentSettings[field.key];
        }
        return makeContext(decodeSettings(settingsSchema, merged, def.defaultSettings).settings);
      };
      return fresh instanceof Promise ? fresh.then(resolve, fallback) : resolve(fresh);
    }
    function mapResult(value, transform = (r) => okResponse(r)) {
      if (value instanceof Promise) {
        return value.then(transform);
      }
      return transform(value);
    }
    function dispatchCore(req, ctx) {
      const method = (req.method || "GET").toUpperCase();
      const { path, query } = parseUrlPathAndQuery(req.path, req.query);
      if (method === "GET" && path === "/manifest.json") {
        return okResponse(manifest);
      }
      if (method === "GET" && path === "/ether/health") {
        if (routes.health) {
          return mapResult(routes.health(ctx));
        }
        const fallback = { status: "ok" };
        return okResponse(fallback);
      }
      if (method === "POST" && path === "/ether/configure") {
        let bodyObj;
        try {
          bodyObj = parseJsonBody(req.body);
        } catch {
          return errorResponse(400, "invalid_json", "Request body must be valid JSON.");
        }
        const incoming = bodyObj.settings && typeof bodyObj.settings === "object" && !Array.isArray(bodyObj.settings) ? bodyObj.settings : bodyObj;
        const decoded = decodeSettings(settingsSchema, incoming, def.defaultSettings);
        if (Object.keys(decoded.errors).length > 0) {
          return okResponse({ errors: decoded.errors });
        }
        if (routes.configure) {
          const nextCtx = { ...ctx, settings: decoded.settings };
          return mapResult(routes.configure(decoded.settings, nextCtx), (res) => {
            const errors = res?.errors ?? {};
            if (Object.keys(errors).length === 0) {
              currentSettings = decoded.settings;
            }
            return okResponse({ errors });
          });
        }
        currentSettings = decoded.settings;
        return okResponse({ errors: {} });
      }
      if (method === "POST" && path === "/ether/subtitles/download") {
        if (!ctx.pickToken?.trim()) return errorResponse(403, "pick_required", "A subtitle pick is required.");
        if (!routes.subtitlesDownload) return errorResponse(404, "not_found", "Subtitle download handler is not registered.");
        const body = parseJsonBody(req.body);
        return mapResult(routes.subtitlesDownload({ id: String(body.id || ""), lang: String(body.lang || ""), format: String(body.format || "") }, ctx));
      }
      if (method === "POST" && path === "/ether/test") {
        if (routes.test) {
          return mapResult(routes.test(ctx));
        }
        const fallback = { ok: true };
        return okResponse(fallback);
      }
      if (method === "GET" && path.startsWith("/ether/options/")) {
        const field = decodeURIComponent(path.slice("/ether/options/".length));
        if (!routes.options) {
          return okResponse([]);
        }
        return mapResult(routes.options(field, ctx));
      }
      if (method === "POST" && path === "/ether/stream/open") {
        if (!routes.openStream) {
          return errorResponse(404, "not_found", "Stream open handler is not registered.");
        }
        const bodyObj = parseJsonBody(req.body);
        return mapResult(
          routes.openStream({ streamId: String(bodyObj.streamId ?? "") }, ctx)
        );
      }
      if (method === "GET" && path.startsWith("/ether/markers/") && path.endsWith(".json")) {
        if (!routes.markers) {
          return errorResponse(404, "not_found", "Markers handler is not registered.");
        }
        const trimmed = path.slice("/ether/markers/".length, -5);
        const parts = trimmed.split("/");
        if (parts.length < 2) {
          return errorResponse(400, "invalid_path", "Markers route requires type and id.");
        }
        return mapResult(
          routes.markers(
            {
              type: decodeURIComponent(parts[0] ?? ""),
              id: decodeURIComponent(parts.slice(1).join("/"))
            },
            ctx
          )
        );
      }
      if (method === "GET" && path === "/ether/channels.json") {
        if (!routes.channels) {
          return errorResponse(404, "not_found", "Channels handler is not registered.");
        }
        return mapResult(routes.channels(ctx));
      }
      if (method === "GET" && path.startsWith("/ether/guide/") && path.endsWith(".json")) {
        if (!routes.guide) {
          return errorResponse(404, "not_found", "Guide handler is not registered.");
        }
        const channelId = decodeURIComponent(path.slice("/ether/guide/".length, -5));
        return mapResult(
          routes.guide({ channelId, from: query.from, to: query.to }, ctx)
        );
      }
      if (method === "POST" && path === "/ether/notify") {
        if (!routes.notify) {
          return errorResponse(404, "not_found", "Notify handler is not registered.");
        }
        const bodyObj = parseJsonBody(req.body);
        return mapResult(
          routes.notify(
            {
              title: typeof bodyObj.title === "string" ? bodyObj.title : void 0,
              message: String(bodyObj.message ?? bodyObj.text ?? ""),
              event: typeof bodyObj.event === "string" ? bodyObj.event : void 0,
              settings: bodyObj.settings && typeof bodyObj.settings === "object" ? bodyObj.settings : void 0
            },
            ctx
          )
        );
      }
      if (method === "POST" && path === "/ether/event") {
        if (!routes.event) {
          return errorResponse(404, "not_found", "Event handler is not registered.");
        }
        const bodyObj = parseJsonBody(req.body);
        const events = Array.isArray(bodyObj.events) ? bodyObj.events : [];
        return mapResult(routes.event(events, ctx), (res) => {
          if (res && Array.isArray(res.ack)) {
            return okResponse({ ack: res.ack });
          }
          return okResponse({ ack: events.map((e) => e.id) });
        });
      }
      if (method === "GET" && (path === "/ether/view" || path.startsWith("/ether/view/") || pageRoutes.has(path))) {
        if (!routes.view) {
          return errorResponse(404, "not_found", "View handler is not registered.");
        }
        let page = pageRoutes.get(path);
        if (!page) {
          page = path === "/ether/view" ? "home" : decodeURIComponent(path.slice("/ether/view/".length));
        }
        return mapResult(routes.view({ page, query }, ctx));
      }
      if (method === "POST" && path.startsWith("/ether/action/")) {
        if (!routes.action) {
          return errorResponse(404, "not_found", "Action handler is not registered.");
        }
        const action = decodeURIComponent(path.slice("/ether/action/".length));
        const input = parseJsonBody(req.body);
        return mapResult(routes.action({ action, input }, ctx));
      }
      if (method === "POST" && path.startsWith("/ether/tool/")) {
        if (!routes.tool) {
          return errorResponse(404, "not_found", "Tool handler is not registered.");
        }
        const name = decodeURIComponent(path.slice("/ether/tool/".length));
        const input = parseJsonBody(req.body);
        return mapResult(routes.tool({ name, input }, ctx));
      }
      if (method === "POST" && path.startsWith("/ether/job/")) {
        if (!routes.job) {
          return errorResponse(404, "not_found", "Job handler is not registered.");
        }
        const name = decodeURIComponent(path.slice("/ether/job/".length));
        return mapResult(routes.job({ name, continuation: parseJsonBody(req.body).continuation }, ctx));
      }
      if (method === "POST" && path === "/ether/rank-key") {
        if (!routes.rankKey) {
          return errorResponse(404, "not_found", "Rank key handler is not registered.");
        }
        const bodyObj = parseJsonBody(req.body);
        const candidate = bodyObj.candidate ?? bodyObj;
        const cfg = bodyObj.config ?? currentSettings;
        const score = Number(routes.rankKey(candidate, cfg)) || 0;
        return okResponse({ score });
      }
      if (method === "POST" && path === "/ether/cond-match") {
        if (!routes.condMatch) {
          return errorResponse(404, "not_found", "Condition match handler is not registered.");
        }
        const bodyObj = parseJsonBody(req.body);
        const candidate = bodyObj.candidate ?? bodyObj;
        const operator = String(bodyObj.operator ?? "eq");
        const value = bodyObj.value;
        const matched = Boolean(routes.condMatch(candidate, operator, value));
        return okResponse({ match: matched });
      }
      if ((method === "GET" || method === "HEAD") && path.startsWith("/ether/file/")) {
        if (!routes.file) {
          return errorResponse(404, "not_found", "File handler is not registered.");
        }
        const relPath = decodeURIComponent(path.slice("/ether/file/".length));
        if (!relPath || relPath.startsWith("/") || relPath.split("/").includes("..")) {
          return errorResponse(400, "invalid_path", "File path must be a clean relative path.");
        }
        const rangeHeader = getHeader(req.headers, "Range");
        return mapResult(
          routes.file({ path: relPath, range: rangeHeader, headOnly: method === "HEAD" }, ctx),
          (fileRes) => {
            const headers = {
              "Content-Type": fileRes.contentType || "application/octet-stream",
              "Accept-Ranges": "bytes",
              ...fileRes.headers ?? {}
            };
            if (fileRes.contentRange) {
              headers["Content-Range"] = fileRes.contentRange;
            }
            if (fileRes.totalSize !== void 0 && !fileRes.contentRange) {
              headers["Content-Length"] = String(fileRes.totalSize);
            }
            return {
              status: fileRes.status ?? (fileRes.contentRange ? 206 : 200),
              headers,
              body: method === "HEAD" ? new Uint8Array(0) : fileRes.body
            };
          }
        );
      }
      if (method === "GET") {
        const stremio = matchStremioRoute(path, query);
        if (stremio) {
          if (stremio.resource === "subtitles") {
            if (!routes.subtitles) {
              return errorResponse(404, "not_found", "Subtitles handler is not registered.");
            }
            const videoSize = stremio.extra.videoSize ? Number(stremio.extra.videoSize) : void 0;
            return mapResult(
              routes.subtitles(
                {
                  type: stremio.type,
                  id: stremio.id,
                  extra: stremio.extra,
                  pickToken: ctx.pickToken,
                  videoHash: stremio.extra.videoHash,
                  videoSize: Number.isFinite(videoSize) ? videoSize : void 0,
                  filename: stremio.extra.filename
                },
                ctx
              )
            );
          }
          if (stremio.resource === "catalog") {
            if (!routes.catalog) {
              return errorResponse(404, "not_found", "Catalog handler is not registered.");
            }
            const skip = stremio.extra.skip ? Number(stremio.extra.skip) || 0 : 0;
            return mapResult(
              routes.catalog(
                {
                  type: stremio.type,
                  id: stremio.id,
                  extra: stremio.extra,
                  skip,
                  search: stremio.extra.search,
                  genre: stremio.extra.genre
                },
                ctx
              )
            );
          }
          if (stremio.resource === "meta") {
            if (!routes.meta) {
              return errorResponse(404, "not_found", "Meta handler is not registered.");
            }
            return mapResult(routes.meta({ type: stremio.type, id: stremio.id }, ctx));
          }
          if (stremio.resource === "stream") {
            if (!routes.stream) {
              return errorResponse(404, "not_found", "Stream handler is not registered.");
            }
            return mapResult(
              routes.stream({ type: stremio.type, id: stremio.id, extra: stremio.extra }, ctx)
            );
          }
        }
      }
      if (path.startsWith("/ether/v1/")) {
        return dispatchKinds(kindHandlers, req, ctx).then(
          (r) => r ?? errorResponse(404, "not_found", `No route matched ${method} ${path}.`)
        );
      }
      return errorResponse(404, "not_found", `No route matched ${method} ${path}.`);
    }
    function handleRouteError(err2) {
      if (isEtherHostFault(err2)) {
        throw err2;
      }
      if (err2 instanceof PluginHttpError) {
        return errorResponse(err2.status, err2.code, err2.message);
      }
      if (err2 && typeof err2 === "object" && "status" in err2 && typeof err2.status === "number") {
        const e = err2;
        return errorResponse(e.status, e.code || "plugin_error", e.message || "Request failed.");
      }
      const message = err2 instanceof Error ? err2.message : String(err2);
      return errorResponse(500, "internal_error", message || "Internal plugin error.");
    }
    function handleRequest(req) {
      try {
        const ctx = buildContext(req);
        const res = ctx instanceof Promise ? ctx.then((c) => dispatchCore(req, c)) : dispatchCore(req, ctx);
        return res instanceof Promise ? res.catch(handleRouteError) : res;
      } catch (err2) {
        return handleRouteError(err2);
      }
    }
    async function dispatch(req) {
      try {
        const ctx = await buildContext(req);
        return await dispatchCore(req, ctx);
      } catch (err2) {
        return handleRouteError(err2);
      }
    }
    function rankKey(candidate, config) {
      if (!routes.rankKey) return 0;
      return Number(routes.rankKey(candidate, config ?? currentSettings)) || 0;
    }
    function condMatch(candidate, operator, value) {
      if (!routes.condMatch) return false;
      return Boolean(routes.condMatch(candidate, operator, value));
    }
    const instance = {
      manifest,
      routes,
      getSettings: () => currentSettings,
      setSettings: (incoming) => {
        const decoded = decodeSettings(settingsSchema, incoming, def.defaultSettings);
        if (Object.keys(decoded.errors).length === 0) {
          currentSettings = decoded.settings;
        }
        return decoded;
      },
      handle: handleRequest,
      dispatch,
      rank_key: rankKey,
      cond_match: condMatch,
      onCatalog: (kind, provider) => {
        kindHandlers.catalog.set(kind, provider);
      },
      onResolve: (kind, resolver) => {
        kindHandlers.resolve.set(kind, resolver);
      },
      onMatch: (kind, matcher) => {
        kindHandlers.match.set(kind, matcher);
      },
      onFrontend: (key, adapter) => {
        kindHandlers.frontend.set(key, adapter);
      },
      onSetupTest: (tester) => {
        kindHandlers.setup = tester;
      }
    };
    if (isHostedRuntime()) {
      const g = globalThis;
      g.handle = handleRequest;
      g.rank_key = rankKey;
      g.cond_match = condMatch;
    }
    return instance;
  }

  // sdk/examples/anime-tosho/manifest.json
  var manifest_default = {
    id: "org.ether.examples.anime-tosho",
    version: "1.0.0",
    name: "Anime Tosho",
    description: "Search Anime Tosho NEW for public anime torrents and Usenet NZB mirrors.",
    types: ["movie", "series"],
    idPrefixes: ["tt", "tmdb", "tvdb", "anidb", "mal", "kitsu"],
    resources: [
      {
        name: "stream",
        types: ["movie", "series"],
        idPrefixes: ["tt", "tmdb", "tvdb", "anidb", "mal", "kitsu"]
      }
    ],
    ether: {
      protocol: 1,
      kind: "source",
      capabilities: ["stream_source"],
      summary: "$t:summary",
      author: {
        name: "Ether",
        url: "https://github.com/braedonsaunders/ether"
      },
      license: "MIT",
      repo: "https://github.com/braedonsaunders/ether",
      icon: "assets/icon.svg",
      runtime: {
        kind: "hosted",
        entry: "dist/plugin.js"
      },
      permissions: {
        network: ["feed.animetosho.net"]
      },
      settings: {
        $ref: "settings.schema.json"
      },
      strings: {
        en: "i18n/en.json",
        fr: "i18n/fr.json"
      },
      contributes: {
        source: {
          class: "indexer",
          abilities: ["search"],
          limits: {
            searchPerHour: 120,
            grabPerHour: 60,
            dailySearches: 1e3,
            dailyGrabs: 300
          }
        }
      }
    }
  };

  // sdk/examples/anime-tosho/settings.schema.json
  var settings_schema_default = {
    fields: [
      {
        key: "releaseTypes",
        kind: "select",
        label: "$t:release_types",
        hint: "$t:release_types_hint",
        default: "both",
        options: [
          { value: "both", label: "$t:release_types_both" },
          { value: "torrents", label: "$t:release_types_torrents" },
          { value: "nzbs", label: "$t:release_types_nzbs" }
        ]
      },
      {
        key: "category",
        kind: "select",
        label: "$t:category",
        hint: "$t:category_hint",
        default: "",
        options: [
          { value: "", label: "$t:category_all" },
          { value: "5070", label: "$t:category_anime" },
          { value: "2020", label: "$t:category_movies" }
        ]
      }
    ]
  };

  // sdk/examples/anime-tosho/plugin.ts
  var API_BASE = "https://feed.animetosho.net";
  var USER_AGENT = "Ether/0.3 (+https://github.com/braedonsaunders/ether)";
  var DEFAULT_LIMIT = 75;
  var MAX_XML_BYTES = 4 * 1024 * 1024;
  var MAX_PARSED_ITEMS = 200;
  var MAX_TRACKERS = 8;
  var DEFAULT_SETTINGS = {
    releaseTypes: "both",
    category: ""
  };
  var VALID_RELEASE_TYPES = /* @__PURE__ */ new Set(["both", "torrents", "nzbs"]);
  var CATEGORY_LIST_RE = /^\d+(?:,\d+)*$/;
  var HEX40_RE = /^[a-fA-F0-9]{40}$/;
  var BASE32_32_RE = /^[A-Z2-7]{32}$/i;
  var VIDEO_EXT_RE = /\.(mkv|mp4|m4v|avi|webm|ts|mov)$/i;
  var BASE32_ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  function normalizeSettings(raw) {
    const releaseTypesRaw = typeof raw?.releaseTypes === "string" ? raw.releaseTypes.trim().toLowerCase() : "";
    const releaseTypes = VALID_RELEASE_TYPES.has(releaseTypesRaw) ? releaseTypesRaw : DEFAULT_SETTINGS.releaseTypes;
    const categoryRaw = typeof raw?.category === "string" ? raw.category.replace(/\s+/g, "") : "";
    const category = categoryRaw && CATEGORY_LIST_RE.test(categoryRaw) ? categoryRaw : "";
    return { releaseTypes, category };
  }
  function validateSettings(raw) {
    const errors = {};
    if (raw && raw.releaseTypes !== void 0 && raw.releaseTypes !== "") {
      const val = typeof raw.releaseTypes === "string" ? raw.releaseTypes.trim().toLowerCase() : "";
      if (!VALID_RELEASE_TYPES.has(val)) {
        errors["/releaseTypes"] = "Choose torrents, NZBs, or both.";
      }
    }
    if (raw && raw.category !== void 0 && raw.category !== "") {
      const val = typeof raw.category === "string" ? raw.category.replace(/\s+/g, "") : "";
      if (!CATEGORY_LIST_RE.test(val)) {
        errors["/category"] = "Choose a valid Torznab or Newznab category.";
      }
    }
    return errors;
  }
  function parseNonNegInt(raw) {
    if (!raw || !/^\d+$/.test(raw)) return void 0;
    const n = Number(raw);
    return Number.isSafeInteger(n) && n >= 0 ? n : void 0;
  }
  function parsePosInt(raw) {
    const n = parseNonNegInt(raw);
    return n !== void 0 && n > 0 ? n : void 0;
  }
  function parseMediaID(mediaType, rawId) {
    if (mediaType !== "movie" && mediaType !== "series") return null;
    const trimmed = (rawId || "").trim();
    if (!trimmed) return null;
    const parts = trimmed.split(":");
    if (parts[0].startsWith("tt")) {
      if (!/^tt\d+$/.test(parts[0])) return null;
      if (mediaType === "movie") {
        if (parts.length !== 1) return null;
        return { mediaType, prefix: "tt", id: parts[0], isAnimeEntryId: false };
      }
      if (parts.length === 2) {
        const season = parseNonNegInt(parts[1]);
        if (season === void 0) return null;
        return { mediaType, prefix: "tt", id: parts[0], season, isAnimeEntryId: false };
      }
      if (parts.length === 3) {
        const season = parseNonNegInt(parts[1]);
        const episode = parsePosInt(parts[2]);
        if (season === void 0 || episode === void 0) return null;
        return { mediaType, prefix: "tt", id: parts[0], season, episode, isAnimeEntryId: false };
      }
      return null;
    }
    const prefix = parts[0].toLowerCase();
    if (prefix === "tmdb" || prefix === "tvdb") {
      const numericId = parts[1];
      if (!numericId || !/^\d+$/.test(numericId) || numericId === "0") return null;
      if (mediaType === "movie") {
        if (prefix !== "tmdb" || parts.length !== 2) return null;
        return { mediaType, prefix, id: numericId, isAnimeEntryId: false };
      }
      if (parts.length === 3) {
        const season = parseNonNegInt(parts[2]);
        if (season === void 0) return null;
        return { mediaType, prefix, id: numericId, season, isAnimeEntryId: false };
      }
      if (parts.length === 4) {
        const season = parseNonNegInt(parts[2]);
        const episode = parsePosInt(parts[3]);
        if (season === void 0 || episode === void 0) return null;
        return { mediaType, prefix, id: numericId, season, episode, isAnimeEntryId: false };
      }
      return null;
    }
    if (prefix === "anidb" || prefix === "mal" || prefix === "kitsu") {
      const numericId = parts[1];
      if (!numericId || !/^\d+$/.test(numericId) || numericId === "0") return null;
      if (mediaType === "movie") {
        if (parts.length !== 2) return null;
        return { mediaType, prefix, id: numericId, isAnimeEntryId: true };
      }
      if (parts.length === 2) {
        return { mediaType, prefix, id: numericId, isAnimeEntryId: true };
      }
      if (parts.length === 3) {
        const episode = parsePosInt(parts[2]);
        if (episode === void 0) return null;
        return { mediaType, prefix, id: numericId, episode, isAnimeEntryId: true };
      }
      return null;
    }
    return null;
  }
  function endpointForSettings(settings) {
    if (settings.releaseTypes === "nzbs") return "/api/newznab";
    if (settings.releaseTypes === "torrents") return "/api/torznab";
    return "/api";
  }
  function buildQueryString(params) {
    return params.map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`).join("&");
  }
  function cleanImdbHint(raw) {
    const s = (raw || "").trim().toLowerCase();
    return /^tt\d+$/.test(s) ? s : "";
  }
  function cleanNumericHint(raw) {
    const s = (raw || "").trim();
    return /^\d+$/.test(s) && s !== "0" ? s : "";
  }
  function buildSearchPlan(mediaType, rawId, query, settings) {
    const parsed = parseMediaID(mediaType, rawId);
    if (!parsed) return null;
    const endpointPath = endpointForSettings(settings);
    const categories = settings.category ? settings.category.split(",").filter(Boolean) : [];
    const params = [];
    if (parsed.mediaType === "movie") {
      if (parsed.prefix === "anidb") {
        params.push(["t", "search"], ["aid", parsed.id]);
      } else if (parsed.prefix === "mal") {
        params.push(["t", "movie"], ["malid", parsed.id]);
      } else if (parsed.prefix === "kitsu") {
        params.push(["t", "movie"], ["kitsuid", parsed.id]);
      } else {
        const imdbId = parsed.prefix === "tt" ? parsed.id : cleanImdbHint(query.imdbId);
        const tmdbId = parsed.prefix === "tmdb" ? parsed.id : cleanNumericHint(query.tmdbId);
        if (imdbId) {
          params.push(["t", "movie"], ["imdbid", imdbId]);
        } else if (tmdbId) {
          params.push(["t", "movie"], ["tmdbid", tmdbId]);
        } else {
          return null;
        }
      }
      if (categories.length > 0) params.push(["cat", categories.join(",")]);
      params.push(["limit", String(DEFAULT_LIMIT)]);
      return {
        kind: "movie",
        endpointPath,
        url: `${API_BASE}${endpointPath}?${buildQueryString(params)}`,
        isAnimeEntryId: parsed.isAnimeEntryId,
        categories
      };
    }
    params.push(["t", "tvsearch"]);
    if (parsed.prefix === "anidb") {
      params.push(["aid", parsed.id]);
    } else if (parsed.prefix === "mal") {
      params.push(["malid", parsed.id]);
    } else if (parsed.prefix === "kitsu") {
      params.push(["kitsuid", parsed.id]);
    } else {
      const tmdbId = parsed.prefix === "tmdb" ? parsed.id : cleanNumericHint(query.tmdbId);
      const tvdbId = parsed.prefix === "tvdb" ? parsed.id : "";
      const imdbId = parsed.prefix === "tt" ? parsed.id : cleanImdbHint(query.imdbId);
      if (tmdbId) {
        params.push(["tmdbid", tmdbId]);
      } else if (tvdbId) {
        params.push(["tvdbid", tvdbId]);
      } else if (imdbId) {
        params.push(["imdbid", imdbId]);
      } else {
        return null;
      }
    }
    const querySeason = parseNonNegInt(query.season);
    const hasQuerySeason = querySeason !== void 0;
    const hasQueryEpisode = parsePosInt(query.episode) !== void 0;
    const explicitPack = query.pack === "1" || query.pack === "true";
    const isSeasonPack = parsed.isAnimeEntryId ? parsed.episode === void 0 || explicitPack : parsed.episode === void 0 || explicitPack || hasQuerySeason && !hasQueryEpisode;
    const season = parsed.isAnimeEntryId ? void 0 : parsed.season ?? querySeason;
    const episode = isSeasonPack ? void 0 : parsed.episode;
    if (isSeasonPack) {
      if (!parsed.isAnimeEntryId && season !== void 0 && season > 0) {
        params.push(["season", String(season)]);
      }
    } else if (episode !== void 0) {
      if (parsed.isAnimeEntryId) {
        params.push(["ep", String(episode)]);
      } else if (season === 1 && episode > 12) {
        params.push(["ep", String(episode)]);
      } else {
        if (season !== void 0) params.push(["season", String(season)]);
        params.push(["ep", String(episode)]);
      }
    }
    if (categories.length > 0) params.push(["cat", categories.join(",")]);
    params.push(["limit", String(DEFAULT_LIMIT)]);
    return {
      kind: isSeasonPack ? "season_pack" : "episode",
      endpointPath,
      url: `${API_BASE}${endpointPath}?${buildQueryString(params)}`,
      season,
      episode,
      isAnimeEntryId: parsed.isAnimeEntryId,
      categories
    };
  }
  function decodeXmlEntities(input) {
    if (!input || !input.includes("&")) return input;
    return input.replace(/&(#x[0-9a-fA-F]+|#\d+|amp|lt|gt|quot|apos);/g, (full, ent) => {
      switch (ent) {
        case "amp":
          return "&";
        case "lt":
          return "<";
        case "gt":
          return ">";
        case "quot":
          return '"';
        case "apos":
          return "'";
        default: {
          const code = ent.startsWith("#x") || ent.startsWith("#X") ? parseInt(ent.slice(2), 16) : parseInt(ent.slice(1), 10);
          if (!Number.isFinite(code) || code <= 0 || code > 1114111) return full;
          try {
            return String.fromCodePoint(code);
          } catch {
            return full;
          }
        }
      }
    });
  }
  function unwrapCdata(raw) {
    const trimmed = raw.trim();
    if (trimmed.startsWith("<![CDATA[") && trimmed.endsWith("]]>")) {
      return trimmed.slice(9, -3);
    }
    return decodeXmlEntities(trimmed);
  }
  function parseTagAttributes(tagText) {
    const attrs = {};
    const re = /([a-zA-Z0-9_:-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')/g;
    let m;
    while ((m = re.exec(tagText)) !== null) {
      attrs[m[1].toLowerCase()] = decodeXmlEntities(m[2] ?? m[3] ?? "");
    }
    return attrs;
  }
  function base32ToHex(b32) {
    if (!BASE32_32_RE.test(b32)) return "";
    const upper = b32.toUpperCase();
    let bits = 0;
    let value = 0;
    let out = "";
    for (let i = 0; i < upper.length; i++) {
      const idx = BASE32_ALPHABET.indexOf(upper[i]);
      if (idx < 0) return "";
      value = value << 5 | idx;
      bits += 5;
      if (bits >= 8) {
        bits -= 8;
        const byte = value >> bits & 255;
        out += byte.toString(16).padStart(2, "0");
      }
    }
    return out.length === 40 ? out : "";
  }
  function normalizeInfoHash(raw) {
    const trimmed = (raw || "").trim();
    if (HEX40_RE.test(trimmed)) return trimmed.toLowerCase();
    if (BASE32_32_RE.test(trimmed)) return base32ToHex(trimmed);
    return "";
  }
  function safeDecodeURI(raw) {
    try {
      return decodeURIComponent(raw.replace(/\+/g, " "));
    } catch {
      return raw;
    }
  }
  function parseMagnet(magnetUrl) {
    if (!magnetUrl || !magnetUrl.toLowerCase().startsWith("magnet:?")) {
      return { infoHash: "", trackers: [] };
    }
    let infoHash = "";
    const trackers = [];
    const seenTrackers = /* @__PURE__ */ new Set();
    const query = magnetUrl.slice(magnetUrl.indexOf("?") + 1);
    for (const part of query.split("&")) {
      if (!part) continue;
      const eq = part.indexOf("=");
      const key = (eq < 0 ? part : part.slice(0, eq)).toLowerCase();
      const val = eq < 0 ? "" : safeDecodeURI(part.slice(eq + 1)).trim();
      if (key === "xt" && !infoHash) {
        const m = /^urn:btih:([a-zA-Z0-9]+)$/i.exec(val);
        if (m) infoHash = normalizeInfoHash(m[1]);
      } else if (key === "tr" && trackers.length < MAX_TRACKERS) {
        if (/^(https?|udp):\/\/\S+$/i.test(val) && !seenTrackers.has(val)) {
          seenTrackers.add(val);
          trackers.push(val);
        }
      }
    }
    return { infoHash, trackers };
  }
  function isSafeHttpUrl(raw) {
    return /^https?:\/\/[^\s"'<>]+$/i.test(raw.trim());
  }
  function extractTagContent(block, tagName) {
    const open = `<${tagName}`;
    const close = `</${tagName}>`;
    let pos = block.indexOf(open);
    while (pos >= 0) {
      const nextChar = block[pos + open.length];
      if (nextChar === ">" || nextChar === " " || nextChar === "	" || nextChar === "\n" || nextChar === "\r") {
        const gt = block.indexOf(">", pos + open.length);
        if (gt < 0) return "";
        const end = block.indexOf(close, gt + 1);
        if (end < 0) return "";
        return unwrapCdata(block.slice(gt + 1, end));
      }
      pos = block.indexOf(open, pos + open.length);
    }
    return "";
  }
  function parseTorznabFeed(xmlInput) {
    if (!xmlInput) return { items: [] };
    const xml = xmlInput.length > MAX_XML_BYTES ? xmlInput.slice(0, MAX_XML_BYTES) : xmlInput;
    const errMatch = /<error\b([^>]*?)\/?\s*>/i.exec(xml);
    if (errMatch && !xml.includes("<channel")) {
      const attrs = parseTagAttributes(errMatch[1]);
      return {
        items: [],
        apiErrorCode: attrs.code || "error",
        apiErrorDescription: attrs.description || "Upstream feed error."
      };
    }
    const items = [];
    let cursor = 0;
    while (items.length < MAX_PARSED_ITEMS) {
      const start = xml.indexOf("<item", cursor);
      if (start < 0) break;
      const afterName = xml[start + 5];
      if (afterName !== ">" && afterName !== " " && afterName !== "	" && afterName !== "\n" && afterName !== "\r") {
        cursor = start + 5;
        continue;
      }
      const startGt = xml.indexOf(">", start + 5);
      if (startGt < 0) break;
      const end = xml.indexOf("</item>", startGt + 1);
      if (end < 0) break;
      cursor = end + 7;
      const block = xml.slice(startGt + 1, end);
      const title = extractTagContent(block, "title").trim();
      if (!title) continue;
      let infoHash = "";
      let nzbUrl = "";
      let magnetUrl = "";
      let size = 0;
      let seeders;
      let files;
      let season;
      let episode;
      const catSet = /* @__PURE__ */ new Set();
      const encRe = /<enclosure\b([^>]*?)\/?\s*>/gi;
      let encMatch;
      while ((encMatch = encRe.exec(block)) !== null) {
        const a = parseTagAttributes(encMatch[1]);
        const type = (a.type || "").toLowerCase();
        const url = (a.url || "").trim();
        const len = parseNonNegInt(a.length);
        if (len && len > size) size = len;
        if (type === "application/x-nzb" && isSafeHttpUrl(url) && !nzbUrl) {
          nzbUrl = url;
        }
      }
      const attrRe = /<(?:torznab|newznab):attr\b([^>]*?)\/?\s*>/gi;
      let attrMatch;
      while ((attrMatch = attrRe.exec(block)) !== null) {
        const a = parseTagAttributes(attrMatch[1]);
        const name = (a.name || "").toLowerCase();
        const value = (a.value || "").trim();
        if (!name || !value) continue;
        switch (name) {
          case "infohash": {
            const h = normalizeInfoHash(value);
            if (h) infoHash = h;
            break;
          }
          case "magneturl":
            if (!magnetUrl) magnetUrl = value;
            break;
          case "nzburl":
            if (isSafeHttpUrl(value)) nzbUrl = value;
            break;
          case "size": {
            const n = parseNonNegInt(value);
            if (n !== void 0 && n > 0) size = n;
            break;
          }
          case "seeders": {
            const n = parseNonNegInt(value);
            if (n !== void 0) seeders = n;
            break;
          }
          case "files": {
            const n = parsePosInt(value);
            if (n !== void 0) files = n;
            break;
          }
          case "season": {
            const n = parseNonNegInt(value);
            if (n !== void 0) season = n;
            break;
          }
          case "episode": {
            const n = parsePosInt(value);
            if (n !== void 0) episode = n;
            break;
          }
          case "category":
            if (/^\d+$/.test(value)) catSet.add(value);
            break;
        }
      }
      const magnet = parseMagnet(magnetUrl);
      if (!infoHash && magnet.infoHash) infoHash = magnet.infoHash;
      items.push({
        title,
        infoHash,
        nzbUrl,
        trackers: magnet.trackers,
        size,
        seeders,
        files,
        season,
        episode,
        categories: Array.from(catSet)
      });
    }
    return { items };
  }
  function formatBytes(bytes) {
    if (!Number.isFinite(bytes) || bytes <= 0) return "";
    const units = ["B", "KB", "MB", "GB", "TB"];
    let value = bytes;
    let unit = 0;
    while (value >= 1024 && unit < units.length - 1) {
      value /= 1024;
      unit++;
    }
    return unit === 0 ? `${Math.round(value)} B` : `${value.toFixed(1)} ${units[unit]}`;
  }
  function itemsToStreams(items, plan, settings) {
    const wantedCats = new Set(plan.categories);
    const ranked = [];
    for (let i = 0; i < items.length; i++) {
      const item = items[i];
      if (wantedCats.size > 0 && item.categories.length > 0) {
        if (!item.categories.some((c) => wantedCats.has(c))) continue;
      }
      let tier = 1;
      if (plan.kind === "season_pack") {
        if (item.episode !== void 0) continue;
        if (!plan.isAnimeEntryId && plan.season !== void 0 && plan.season > 0) {
          if (item.season !== void 0) {
            if (item.season !== plan.season) continue;
            tier = 0;
          }
        }
      } else if (plan.kind === "episode" && !plan.isAnimeEntryId && plan.season !== void 0) {
        if (item.season !== void 0) {
          if (item.season === plan.season) {
            tier = 0;
          } else if (plan.season === 1 && plan.episode !== void 0 && plan.episode > 12) {
            tier = 2;
          } else {
            continue;
          }
        }
      }
      ranked.push({ tier, index: i, item });
    }
    ranked.sort((a, b) => a.tier !== b.tier ? a.tier - b.tier : a.index - b.index);
    const includeTorrents = settings.releaseTypes !== "nzbs";
    const includeNzbs = settings.releaseTypes !== "torrents";
    const seenHashes = /* @__PURE__ */ new Set();
    const seenNzbs = /* @__PURE__ */ new Set();
    const streams = [];
    for (const { item } of ranked) {
      const sizeLabel = formatBytes(item.size);
      const behaviorHints = {};
      if (item.size > 0) behaviorHints.videoSize = item.size;
      if (includeTorrents && item.infoHash && !seenHashes.has(item.infoHash)) {
        seenHashes.add(item.infoHash);
        const details = [];
        if (item.seeders !== void 0) details.push(`\u{1F464} ${item.seeders}`);
        if (sizeLabel) details.push(`\u{1F4BE} ${sizeLabel}`);
        const torrentHints = { ...behaviorHints };
        if (plan.kind !== "season_pack" && item.files === 1 && VIDEO_EXT_RE.test(item.title)) {
          torrentHints.filename = item.title;
        }
        const stream = {
          name: "Anime Tosho",
          title: details.length > 0 ? `${item.title}
${details.join("  ")}` : item.title,
          infoHash: item.infoHash
        };
        if (item.trackers.length > 0) {
          stream.sources = item.trackers.map((tr) => `tracker:${tr}`);
        }
        if (Object.keys(torrentHints).length > 0) {
          stream.behaviorHints = torrentHints;
        }
        streams.push(stream);
      }
      if (includeNzbs && item.nzbUrl && !seenNzbs.has(item.nzbUrl)) {
        seenNzbs.add(item.nzbUrl);
        const details = [];
        if (sizeLabel) details.push(`\u{1F4BE} ${sizeLabel}`);
        details.push("Usenet NZB");
        const stream = {
          name: "Anime Tosho NZB",
          title: `${item.title}
${details.join(" \xB7 ")}`,
          nzbUrl: item.nzbUrl
        };
        if (Object.keys(behaviorHints).length > 0) {
          stream.behaviorHints = behaviorHints;
        }
        streams.push(stream);
      }
    }
    return streams;
  }
  function getHeaderCI(headers, name) {
    if (!headers) return "";
    const target = name.toLowerCase();
    for (const [k, v] of Object.entries(headers)) {
      if (k.toLowerCase() === target) return String(v || "").trim();
    }
    return "";
  }
  function parseRetryAfter(headers) {
    const raw = getHeaderCI(headers, "Retry-After");
    return raw || "60";
  }
  var pendingRetryAfter = "";
  var settingsManifest = {
    ...manifest_default,
    ether: { ...manifest_default.ether, settings: settings_schema_default }
  };
  var animeTosho = plugin({
    manifest: settingsManifest,
    defaultSettings: DEFAULT_SETTINGS,
    routes: {
      health() {
        return { status: "ok" };
      },
      configure(settings) {
        const errors = validateSettings(settings);
        return { errors };
      },
      test(ctx) {
        const res = ctx.ether.http.fetch({
          url: `${API_BASE}/api?t=caps`,
          headers: {
            "User-Agent": USER_AGENT,
            Accept: "application/xml, text/xml"
          }
        });
        if (res.status === 429) {
          return { ok: false, message: "Anime Tosho NEW rate limit reached. Try again later." };
        }
        if (res.status !== 200 || !res.body || !res.body.includes("<caps")) {
          return { ok: false, message: "Could not read Anime Tosho NEW feed capabilities." };
        }
        const settings = normalizeSettings(ctx.settings);
        const modeLabel = settings.releaseTypes === "torrents" ? "Torznab torrents" : settings.releaseTypes === "nzbs" ? "Newznab Usenet NZBs" : "Torznab torrents and Newznab Usenet NZBs";
        return {
          ok: true,
          message: `Connected to Anime Tosho NEW (${modeLabel}).`
        };
      },
      stream(req, ctx) {
        const settings = normalizeSettings(ctx.settings);
        const mergedQuery = { ...req.extra, ...ctx.query };
        const plan = buildSearchPlan(req.type, req.id, mergedQuery, settings);
        if (!plan) {
          return { streams: [] };
        }
        const res = ctx.ether.http.fetch({
          url: plan.url,
          headers: {
            "User-Agent": USER_AGENT,
            Accept: "application/xml, text/xml"
          }
        });
        if (res.status === 429 || res.status === 503 && getHeaderCI(res.headers, "Retry-After")) {
          pendingRetryAfter = parseRetryAfter(res.headers);
          throw new PluginHttpError(
            429,
            "rate_limited",
            "Anime Tosho NEW rate limit reached. Try again later."
          );
        }
        if (res.status !== 200) {
          throw new PluginHttpError(
            502,
            "upstream_error",
            `Anime Tosho NEW search failed with HTTP ${res.status}.`
          );
        }
        const parsed = parseTorznabFeed(res.body || "");
        if (parsed.apiErrorCode) {
          const desc = parsed.apiErrorDescription || "Anime Tosho NEW returned an error.";
          if (parsed.apiErrorCode === "429" || /rate\s*limit|too many requests/i.test(desc)) {
            pendingRetryAfter = parseRetryAfter(res.headers);
            throw new PluginHttpError(429, "rate_limited", desc);
          }
          throw new PluginHttpError(502, "upstream_error", desc);
        }
        return { streams: itemsToStreams(parsed.items, plan, settings) };
      }
    }
  });
  function handle(req) {
    pendingRetryAfter = "";
    const finalize = (res) => {
      const retryAfter = pendingRetryAfter;
      pendingRetryAfter = "";
      if (res.status === 429 && retryAfter) {
        return {
          ...res,
          headers: {
            ...res.headers ?? {},
            "Retry-After": retryAfter
          }
        };
      }
      return res;
    };
    const out = animeTosho.handle(req);
    return out instanceof Promise ? out.then(finalize) : finalize(out);
  }
  if (typeof globalThis !== "undefined" && typeof globalThis.ether === "object" && globalThis.ether !== null) {
    globalThis.handle = handle;
  }
  return __toCommonJS(plugin_exports);
})();
