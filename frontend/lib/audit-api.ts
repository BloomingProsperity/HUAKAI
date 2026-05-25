export type VerifyStatus = 'verified' | 'partial' | 'tampered';
export type HopName = 'ingress' | 'router' | 'pool' | 'account' | 'provider' | 'response';

export interface HopAttestation {
  hop: HopName | string;
  ts: string;
  request_id?: string;
  account_id_hash?: string;
  pool_id?: string;
  route_id?: string;
  provider?: string;
  endpoint?: string;
  duration_ms?: number;
  detail?: unknown;
}
export interface ModelChain {
  requested: string;
  route_decided: string;
  upstream_reported?: string;
}
export interface AuditLedgerEntry {
  ledger_id: string;
  timestamp: string;
  request_id: string;
  tenant_id: number;
  hop_chain: HopAttestation[];
  model_chain?: ModelChain | null;
}
export interface AuditChainProof {
  prev_merkle_root: string;
  merkle_root: string;
  signature: string;
  pubkey_fingerprint: string;
}
export interface AuditVerifyResponse {
  ledger_entry: AuditLedgerEntry;
  chain_proof: AuditChainProof;
}
export interface AuditMerkleTree {
  latest_merkle_root: string;
  size: number;
  total_entries?: number;
}
export interface AuditBundle {
  verify: AuditVerifyResponse;
  tree: AuditMerkleTree;
  source: 'backend' | 'mock';
}
export interface AuditVerifyResult {
  status: VerifyStatus;
  details: string[];
}
export async function fetchAuditVerify(requestId: string, signal?: AbortSignal): Promise<AuditVerifyResponse> {
  return fetchJSON<AuditVerifyResponse>(`/v1/audit/verify?request_id=${encodeURIComponent(requestId)}`, signal);
}
export async function fetchAuditMerkleTree(signal?: AbortSignal): Promise<AuditMerkleTree> {
  return fetchJSON<AuditMerkleTree>('/v1/audit/merkle-tree.json', signal);
}
export async function fetchAuditBundle(requestId: string, signal?: AbortSignal): Promise<AuditBundle> {
  const [verify, tree] = await Promise.all([
    fetchAuditVerify(requestId, signal),
    fetchAuditMerkleTree(signal),
  ]);
  return { verify, tree, source: 'backend' };
}
export async function verifyAuditProofInBrowser(bundle: AuditBundle): Promise<AuditVerifyResult> {
  if (bundle.source === 'mock') {
    return { status: 'partial', details: ['当前为本地 mock 数据，未执行真实 ed25519 验签。'] };
  }
  const structural = validateProofShape(bundle);
  if (structural.length > 0) return { status: 'tampered', details: structural };

  try {
    const entryHash = await computeEntryHash(bundle.verify);
    const expectedRoot = await sha256Hex(concatBytes(hexToBytes(bundle.verify.chain_proof.prev_merkle_root), entryHash));
    if (expectedRoot !== bundle.verify.chain_proof.merkle_root) {
      return { status: 'tampered', details: ['MerkleRoot 与 PrevMerkleRoot + EntryHash 不匹配。'] };
    }
    const publicKey = await fetchPublicKey(bundle.verify.chain_proof.pubkey_fingerprint);
    if (!publicKey) {
      return { status: 'partial', details: ['Merkle 链本地校验通过，但未取得公开 ed25519 公钥。'] };
    }
    const fp = (await sha256Hex(publicKey)).slice(0, 16);
    if (fp !== bundle.verify.chain_proof.pubkey_fingerprint) {
      return { status: 'tampered', details: ['公开公钥指纹与 ledger 记录不匹配。'] };
    }
    const ok = await verifyEd25519(publicKey, base64ToBytes(bundle.verify.chain_proof.signature), entryHash);
    if (!ok) return { status: 'tampered', details: ['ed25519 签名校验失败。'] };

    const latest = bundle.tree.latest_merkle_root;
    const note = latest === bundle.verify.chain_proof.merkle_root
      ? '该条记录是公开树当前最新 root。'
      : '签名与本条 Merkle 链通过；LatestRoot 已前进，当前记录属于历史条目。';
    return { status: 'verified', details: [note] };
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    if (message.includes('Ed25519')) {
      return { status: 'partial', details: [`浏览器不支持 ed25519 WebCrypto：${message}`] };
    }
    return { status: 'tampered', details: [message] };
  }
}
async function fetchJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const resp = await fetch(path, { cache: 'no-store', signal });
  if (resp.ok) return resp.json() as Promise<T>;
  let message = `HTTP ${resp.status}`;
  try {
    const payload = await resp.json() as { error?: { message?: string } };
    message = payload.error?.message ?? message;
  } catch {
    // 保持 HTTP 状态作为错误信息。
  }
  throw new Error(message);
}
function validateProofShape(bundle: AuditBundle): string[] {
  const { ledger_entry: entry, chain_proof: proof } = bundle.verify;
  const errors: string[] = [];
  if (!entry.request_id || !entry.ledger_id || !entry.timestamp) errors.push('LedgerEntry 缺少基础标识字段。');
  if (entry.hop_chain.length !== 6) errors.push(`HopChain 跳数为 ${entry.hop_chain.length}，期望 6。`);
  for (const field of ['prev_merkle_root', 'merkle_root'] as const) {
    if (!/^[0-9a-f]{64}$/i.test(proof[field])) errors.push(`${field} 不是 32-byte hex。`);
  }
  if (!proof.signature) errors.push('签名字段为空。');
  if (!/^[0-9a-f]{16}$/i.test(proof.pubkey_fingerprint)) errors.push('公钥指纹不是 16 位 hex。');
  return errors;
}
async function computeEntryHash(resp: AuditVerifyResponse): Promise<Uint8Array> {
  const { ledger_entry: entry, chain_proof: proof } = resp;
  const parts = [
    utf8(entry.ledger_id),
    unitSep(),
    utf8(entry.timestamp),
    unitSep(),
    utf8(entry.request_id),
    unitSep(),
    int64BE(entry.tenant_id),
    unitSep(),
    utf8(JSON.stringify(entry.hop_chain)),
    unitSep(),
    utf8(JSON.stringify(entry.model_chain ?? null)),
    unitSep(),
    utf8(proof.pubkey_fingerprint),
  ];
  return sha256Bytes(concatBytes(...parts));
}
async function fetchPublicKey(fingerprint: string): Promise<Uint8Array | null> {
  const resp = await fetch('/.well-known/huakai-pubkey.json', { cache: 'no-store' });
  if (!resp.ok) return null;
  const text = await resp.text();
  const material = findPubkeyMaterial(text, fingerprint);
  return material ? decodePubkeyMaterial(material) : null;
}
function findPubkeyMaterial(text: string, fingerprint: string): string | null {
  try {
    const parsed = JSON.parse(text) as unknown;
    const fromDoc = materialFromDoc(parsed, fingerprint);
    if (fromDoc) return fromDoc;
    if (isRecord(parsed) && typeof parsed[fingerprint] === 'string') return parsed[fingerprint];
    return null;
  } catch {
    return text.trim();
  }
}
function materialFromDoc(value: unknown, fingerprint: string): string | null {
  if (!isRecord(value)) return null;
  const fp = stringOf(value.fingerprint) || stringOf(value.pubkey_fingerprint);
  const material = stringOf(value.public_key) || stringOf(value.public_key_ed25519) || stringOf(value.pubkey);
  if (material && (!fp || fp === fingerprint)) return material;
  const keys = Array.isArray(value.keys) ? value.keys : [];
  for (const key of keys) {
    const found = materialFromDoc(key, fingerprint);
    if (found) return found;
  }
  return null;
}
async function verifyEd25519(publicKey: Uint8Array, signature: Uint8Array, message: Uint8Array): Promise<boolean> {
  const cryptoKey = await crypto.subtle.importKey('raw', publicKey as unknown as BufferSource, { name: 'Ed25519' }, false, ['verify']);
  return crypto.subtle.verify({ name: 'Ed25519' }, cryptoKey, signature as unknown as BufferSource, message as unknown as BufferSource);
}
function decodePubkeyMaterial(material: string): Uint8Array {
  const cleaned = material.trim().replace(/^ed25519:/, '');
  if (/^[0-9a-f]{64}$/i.test(cleaned)) return hexToBytes(cleaned);
  const raw = cleaned.replace(/-/g, '+').replace(/_/g, '/');
  const padded = raw.padEnd(raw.length + ((4 - (raw.length % 4)) % 4), '=');
  const bytes = base64ToBytes(padded);
  if (bytes.length !== 32) throw new Error('invalid ed25519 public key material');
  return bytes;
}
function base64ToBytes(value: string): Uint8Array {
  const binary = atob(value);
  return Uint8Array.from(binary, (ch) => ch.charCodeAt(0));
}
function hexToBytes(value: string): Uint8Array {
  const out = new Uint8Array(value.length / 2);
  for (let i = 0; i < out.length; i += 1) out[i] = Number.parseInt(value.slice(i * 2, i * 2 + 2), 16);
  return out;
}
async function sha256Bytes(bytes: Uint8Array): Promise<Uint8Array> {
  return new Uint8Array(await crypto.subtle.digest('SHA-256', bytes as unknown as BufferSource));
}
async function sha256Hex(bytes: Uint8Array): Promise<string> {
  return bytesToHex(await sha256Bytes(bytes));
}
function bytesToHex(bytes: Uint8Array): string {
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}
function concatBytes(...chunks: Uint8Array[]): Uint8Array {
  const out = new Uint8Array(chunks.reduce((sum, chunk) => sum + chunk.length, 0));
  let offset = 0;
  for (const chunk of chunks) {
    out.set(chunk, offset);
    offset += chunk.length;
  }
  return out;
}
function int64BE(value: number): Uint8Array {
  const out = new Uint8Array(8);
  new DataView(out.buffer).setBigInt64(0, BigInt(value), false);
  return out;
}
function utf8(value: string): Uint8Array {
  return new TextEncoder().encode(value);
}
function unitSep(): Uint8Array {
  return Uint8Array.of(0x1f);
}
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}
function stringOf(value: unknown): string {
  return typeof value === 'string' ? value : '';
}
