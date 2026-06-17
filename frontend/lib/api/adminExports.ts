// 管理 CSV 导出数据层 —— 走管理 token（downloadCsv from client.ts）。
// 后端: internal/exporthttp /v1/admin/{payments,usage,orders,refunds}/export.csv（只读，无计费副作用）。
import { downloadCsv } from './client';
import {
  buildExportUrl,
  validateExportRange,
  type ExportKind,
  type ExportRangeParams,
} from './export-csv-form';

// downloadAdminExport: 校验区间（fail-fast，避免发出后端必拒的请求）→ 构造 export.csv URL →
// 鉴权 blob 下载（文件名 {kind}-export.csv，对齐后端 setCSVHeaders）。
export function downloadAdminExport(kind: ExportKind, params: ExportRangeParams): Promise<void> {
  const invalid = validateExportRange(params.from, params.to);
  if (invalid) return Promise.reject(new Error(invalid));
  return downloadCsv(buildExportUrl(kind, params), `${kind}-export.csv`);
}
