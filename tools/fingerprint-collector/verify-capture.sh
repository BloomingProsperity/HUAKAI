#!/usr/bin/env bash

# 校验 fingerprint-collector 的净化输出是否满足提交前最低条件。
# 用法：./verify-capture.sh [output-dir]

set -u

output_dir="${1:-./output}"
metadata="${output_dir}/metadata.json"
ja3_file="${output_dir}/ja3-hashes.txt"
failures=()

if [[ ! -d "${output_dir}" ]]; then
  failures+=("输出目录不存在: ${output_dir}")
fi

sample_count=""
mitm_result=""

if [[ ! -f "${metadata}" ]]; then
  failures+=("缺少 metadata.json: ${metadata}")
else
  sample_count="$(sed -nE 's/.*"sample_count"[[:space:]]*:[[:space:]]*([0-9]+).*/\1/p' "${metadata}" | head -n 1)"
  mitm_result="$(sed -nE 's/.*"mitm_check_result"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' "${metadata}" | head -n 1)"

  if [[ -z "${sample_count}" ]]; then
    failures+=("metadata.json 缺少 sample_count")
  elif ! [[ "${sample_count}" =~ ^[0-9]+$ ]] || (( sample_count <= 0 )); then
    failures+=("metadata.json sample_count 必须大于 0，实际为: ${sample_count}")
  fi

  if [[ "${mitm_result}" != "ok" ]]; then
    failures+=("metadata.json mitm_check_result 必须为 ok，实际为: ${mitm_result:-<missing>}")
  fi
fi

if [[ ! -f "${ja3_file}" ]]; then
  failures+=("缺少 ja3-hashes.txt: ${ja3_file}")
elif [[ -n "${sample_count}" && "${sample_count}" =~ ^[0-9]+$ ]]; then
  ja3_count="$(grep -E '^(hash:[[:space:]]*)?[[:xdigit:]]{32}[[:space:]]*$' "${ja3_file}" 2>/dev/null | wc -l | tr -d ' ')"
  if [[ "${ja3_count}" != "${sample_count}" ]]; then
    failures+=("ja3-hashes.txt 有效 hash 行数应等于 sample_count=${sample_count}，实际为: ${ja3_count}")
  fi
fi

if (( ${#failures[@]} == 0 )); then
  echo "OK: ${output_dir} capture verified"
  exit 0
fi

echo "FAIL: ${output_dir} capture verify failed"
for item in "${failures[@]}"; do
  echo "- ${item}"
done
exit 1

