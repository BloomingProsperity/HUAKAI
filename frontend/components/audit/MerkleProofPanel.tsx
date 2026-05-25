import { FileKey2 } from 'lucide-react';
import type { AuditChainProof, AuditMerkleTree } from '@/lib/audit-api';

function ProofRow({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="grid gap-1 rounded-lg border border-accent-200 bg-accent-50 p-3 dark:border-accent-800 dark:bg-accent-950/70 md:grid-cols-[150px_minmax(0,1fr)] md:items-center">
      <dt className="text-xs font-medium text-accent-500 dark:text-accent-400">{label}</dt>
      <dd className="min-w-0 break-all font-mono text-xs text-accent-900 dark:text-accent-100">{value || '未记录'}</dd>
    </div>
  );
}

export function MerkleProofPanel({
  proof,
  tree,
}: {
  proof: AuditChainProof;
  tree: AuditMerkleTree;
}) {
  const total = tree.total_entries ?? tree.size;
  const isLatest = proof.merkle_root === tree.latest_merkle_root;

  return (
    <section className="rounded-lg border border-accent-200 bg-white p-5 shadow-card dark:border-accent-800 dark:bg-accent-900/70" aria-label="Merkle 链 proof">
      <div className="flex flex-col justify-between gap-3 sm:flex-row sm:items-center">
        <div>
          <h2 className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <FileKey2 className="size-4 text-primary-600 dark:text-primary-300" />
            Merkle 链 proof
          </h2>
          <p className="mt-1 text-sm text-accent-500 dark:text-accent-400">PrevRoot、当前 Root、公开 LatestRoot 与签名材料。</p>
        </div>
        <span className="inline-flex min-h-8 items-center rounded-lg border border-accent-200 bg-accent-50 px-3 text-sm text-accent-700 dark:border-accent-800 dark:bg-accent-950/70 dark:text-accent-200">
          {isLatest ? '当前最新 root' : '历史 root'} · 共 {total} 条
        </span>
      </div>

      <dl className="mt-4 grid gap-3">
        <ProofRow label="PrevMerkleRoot" value={proof.prev_merkle_root} />
        <ProofRow label="MerkleRoot" value={proof.merkle_root} />
        <ProofRow label="LatestRoot" value={tree.latest_merkle_root} />
        <ProofRow label="Signature" value={proof.signature} />
        <ProofRow label="PubkeyFingerprint" value={proof.pubkey_fingerprint} />
      </dl>
    </section>
  );
}
