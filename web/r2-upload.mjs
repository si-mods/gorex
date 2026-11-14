#!/usr/bin/env node
// Uploads web/dist to a Cloudflare R2 bucket via its S3-compatible API
// (AWS SigV4, zero external dependencies).
//
// Reads R2_ENDPOINT, R2_BUCKET, R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY,
// and optional R2_PREFIX from the environment. Uploads hashed assets
// first and index.html / files.json last so clients never see a partial
// index.

import { createHash, createHmac } from "node:crypto";
import { readdirSync, readFileSync } from "node:fs";
import { extname, join, relative } from "node:path";

const endpoint = (process.env.R2_ENDPOINT || "").replace(/\/+$/, "");
const bucket = process.env.R2_BUCKET || "";
const accessKey = process.env.R2_ACCESS_KEY_ID || "";
const secretKey = process.env.R2_SECRET_ACCESS_KEY || "";
const prefix = (process.env.R2_PREFIX || "").replace(/^\/+|\/+$/g, "");

if (!endpoint || !bucket || !accessKey || !secretKey) {
  console.error("Set R2_ENDPOINT, R2_BUCKET, R2_ACCESS_KEY_ID, and R2_SECRET_ACCESS_KEY");
  process.exit(1);
}

const dist = process.env.GOREX_WEB_DIST || join(import.meta.dirname, "dist");

const mimeTypes = {
  ".html": "text/html; charset=utf-8",
  ".js": "application/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".webmanifest": "application/manifest+json; charset=utf-8",
  ".wasm": "application/wasm",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".ico": "image/x-icon",
  ".woff2": "font/woff2",
  ".ttf": "font/ttf",
  ".txt": "text/plain; charset=utf-8",
  ".gz": "application/gzip",
};

const sha256Hex = (buf) => createHash("sha256").update(buf).digest("hex");
const hmac = (key, data) => createHmac("sha256", key).update(data).digest();

function collect(dir) {
  const out = [];
  const walk = (cur) => {
    for (const ent of readdirSync(cur, { withFileTypes: true })) {
      const full = join(cur, ent.name);
      if (ent.isDirectory()) {
        walk(full);
        continue;
      }
      const rel = relative(dir, full).split("\\").join("/");
      if (rel === "_headers") continue;
      out.push({ rel, full });
    }
  };
  walk(dir);
  // Upload index.html and files.json last.
  const isLast = (r) => r === "index.html" || r === "files.json";
  out.sort((a, b) => Number(isLast(a.rel)) - Number(isLast(b.rel)) || a.rel.localeCompare(b.rel));
  return out;
}

async function putObject(key, body, contentType, cacheControl) {
  const u = new URL(`${endpoint}/${bucket}/${key.split("/").map(encodeURIComponent).join("/")}`);
  const now = new Date();
  const amzDate = now.toISOString().replace(/[:-]|\.\d{3}/g, "");
  const dateStamp = amzDate.slice(0, 8);
  const payloadHash = sha256Hex(body);

  const headers = {
    "cache-control": cacheControl,
    "content-type": contentType,
    host: u.host,
    "x-amz-content-sha256": payloadHash,
    "x-amz-date": amzDate,
  };
  const signedHeaders = Object.keys(headers).sort().join(";");
  const canonicalHeaders = Object.keys(headers)
    .sort()
    .map((k) => `${k}:${headers[k]}\n`)
    .join("");
  const canonicalRequest = ["PUT", u.pathname, "", canonicalHeaders, signedHeaders, payloadHash].join("\n");
  const scope = `${dateStamp}/auto/s3/aws4_request`;
  const stringToSign = ["AWS4-HMAC-SHA256", amzDate, scope, sha256Hex(canonicalRequest)].join("\n");

  const kDate = hmac(`AWS4${secretKey}`, dateStamp);
  const kRegion = hmac(kDate, "auto");
  const kService = hmac(kRegion, "s3");
  const kSigning = hmac(kService, "aws4_request");
  const signature = createHmac("sha256", kSigning).update(stringToSign).digest("hex");

  const authorization = `AWS4-HMAC-SHA256 Credential=${accessKey}/${scope}, SignedHeaders=${signedHeaders}, Signature=${signature}`;
  const res = await fetch(u, {
    method: "PUT",
    headers: { ...headers, authorization },
    body,
  });
  if (!res.ok) {
    throw new Error(`PUT ${key} failed: ${res.status} ${await res.text()}`);
  }
}

const files = collect(dist);
for (const f of files) {
  const body = readFileSync(f.full);
  const key = prefix ? `${prefix}/${f.rel}` : f.rel;
  const contentType = mimeTypes[extname(f.rel)] || "application/octet-stream";
  const cacheControl = f.rel.startsWith("assets/") ? "public, max-age=31536000, immutable" : "no-store";
  await putObject(key, body, contentType, cacheControl);
  console.log(`uploaded ${key} (${body.length} bytes)`);
}
console.log(`uploaded ${files.length} files to R2 bucket ${bucket}`);
