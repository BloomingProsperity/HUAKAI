// HUAKAI fingerprint-collector: undici h2 probe for the capture server.
//
// W11-F F-1.g attempt-2 (2026-05-26): drives the local h2 capture server
// at 127.0.0.1:18099 with undici's default h2 settings so the server can
// log undici's on-wire SETTINGS + HEADERS frames.
//
// Run:
//   node tools/fingerprint-collector/clients/undici_probe.mjs
//
// Prereq:
//   npm install undici@^7

import { Client } from "undici";

const target = process.env.HUAKAI_PROBE_TARGET ?? "https://127.0.0.1:18099";

const client = new Client(target, {
  allowH2: true,
  connect: { rejectUnauthorized: false },
});

try {
  const res = await client.request({
    method: "POST",
    path: "/v1/messages",
    headers: {
      "user-agent": "huakai-fingerprint-probe/1.0 (undici)",
      "content-type": "application/json",
      "accept": "application/json",
    },
    body: JSON.stringify({ probe: true }),
  });
  const body = await res.body.text();
  console.log(`[probe] status=${res.statusCode} body=${body}`);
} catch (err) {
  console.error(`[probe] error: ${err.message}`);
  process.exitCode = 1;
} finally {
  await client.close();
}
