#!/usr/bin/env bash
# 容量冗余水位的真实验证（零生成费用）。
#
# 把参考素材配额临时压到 1 MiB、冗余 10%（水位 900 KiB），连续提交 4 份约 300 KiB 的
# 参考素材：前 3 份把占用顶到水位线，第 4 份必须先删掉最早的素材才能写入。
# 请求本身用非法 duration 挡住，不会产生任何生成费用。
set -uo pipefail
cd "$(dirname "$0")" || exit 1

BASE="${BASE:-http://127.0.0.1:8080}"
COMPOSE=(docker compose -f docker-compose.dev.yml)
LIMIT_BYTES=1048576 # 1 MiB
WATERLINE_BYTES=943718 # 1 MiB × 90%
MATERIAL_BYTES=300000 # 约 300 KiB

psql() { "${COMPOSE[@]}" exec -T postgres psql -U sub2api -d sub2api -tAc "$1" | tr -d '\r'; }
API_KEY="$(psql "SELECT k.key FROM api_keys k JOIN groups g ON g.id=k.group_id WHERE g.platform='video' AND k.status='active' ORDER BY k.id LIMIT 1;")"

BEFORE_IDS="$(psql "SELECT COALESCE(string_agg(id::text, ','), '') FROM temporary_assets WHERE deleted_at IS NULL AND purpose='reference';")"
cleanup() {
  local new_ids
  new_ids="$(psql "SELECT COALESCE(string_agg(id::text, ','), '') FROM temporary_assets
    WHERE purpose='reference' AND id::text <> ALL(COALESCE(string_to_array(NULLIF('$BEFORE_IDS', ''), ','), ARRAY[]::text[]));")"
  if [ -n "$new_ids" ]; then
    psql "UPDATE temporary_assets SET deleted_at=NOW() WHERE id::text = ANY(string_to_array('$new_ids', ','));" >/dev/null
    local id key
    for id in ${new_ids//,/ }; do
      key="$(psql "SELECT storage_key FROM temporary_assets WHERE id='$id';")"
      "${COMPOSE[@]}" exec -T sub2api sh -lc "rm -rf '$(dirname "$key")'" 2>/dev/null
    done
  fi
  # 还原配额设置。
  curl -s -X PUT "$BASE/api/v1/admin/file-service/settings" -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' -d "$RESTORE_BODY" >/dev/null
  echo "  已清理本次素材并还原配额"
}

echo "== 准备：签发管理员 token，临时压低参考素材配额 =="
HASH="$(psql "SELECT password_hash FROM users WHERE role='admin' ORDER BY id LIMIT 1;")"
SECRET="$(grep -E '^JWT_SECRET=' .env | cut -d= -f2-)"
EMAIL="$(psql "SELECT email FROM users WHERE role='admin' ORDER BY id LIMIT 1;")"
TOKEN="$(python3 - "$HASH" "$SECRET" "$EMAIL" <<'PYEOF'
import base64, hashlib, hmac, json, struct, sys, time
password_hash, secret, email = sys.argv[1], sys.argv[2], sys.argv[3]
material = (email.lower() + "\n" + password_hash).encode()
fingerprint = struct.unpack(">Q", hashlib.sha256(material).digest()[:8])[0] & 0x7FFFFFFFFFFFFFFF
now = int(time.time())
b64 = lambda d: base64.urlsafe_b64encode(d).rstrip(b"=")
header = b64(json.dumps({"alg": "HS256", "typ": "JWT"}, separators=(",", ":")).encode())
payload = b64(json.dumps({"user_id": 1, "email": email, "role": "admin", "token_version": fingerprint,
                          "exp": now + 3600, "iat": now, "nbf": now}, separators=(",", ":")).encode())
signing = header + b"." + payload
print((signing + b"." + b64(hmac.new(secret.encode(), signing, hashlib.sha256).digest())).decode())
PYEOF
)"
[ -n "$TOKEN" ] || { echo "FAIL: 未能签发管理员 token"; exit 1; }

SETTINGS="$(curl -s "$BASE/api/v1/admin/file-service/settings" -H "Authorization: Bearer $TOKEN")"
build_put() {
  printf '%s' "$SETTINGS" | python3 -c '
import json, sys
data = json.load(sys.stdin)["data"]
config = {key: data[key] for key in (
    "schema_version", "backend", "public_base_url", "retention_hours",
    "daily_max_count", "daily_max_bytes", "max_total_bytes",
    "result_retention_hours", "result_max_total_bytes", "capacity_reserve_percent", "s3",
)}
config["s3"]["secret_access_key"] = ""
config["max_total_bytes"] = int(sys.argv[1])
config["capacity_reserve_percent"] = int(sys.argv[2])
print(json.dumps(config))' "$1" "$2"
}
RESTORE_BODY="$(build_put 0 10)"

curl -s -X PUT "$BASE/api/v1/admin/file-service/settings" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "$(build_put "$LIMIT_BYTES" 10)" \
  | python3 -c '
import json, sys
d = json.load(sys.stdin)["data"]
print("  配额 %s 字节，冗余 %s%% -> 水位 %s 字节" % (d["max_total_bytes"], d["capacity_reserve_percent"], d["max_total_bytes"] * 90 // 100))
'

echo "== 连续提交 4 份约 ${MATERIAL_BYTES} 字节的参考素材 =="
PNG_B64="$(python3 -c '
import base64, os, struct, sys, zlib
size = int(sys.argv[1])
def chunk(t, d):
    c = t + d
    return struct.pack(">I", len(d)) + c + struct.pack(">I", zlib.crc32(c) & 0xffffffff)
# 用随机像素让 PNG 不可压缩，从而稳定达到目标体积。
side = 320
raw = b"".join(b"\x00" + os.urandom(side * 3) for _ in range(side))
png = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", side, side, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(raw, 0)) + chunk(b"IEND", b"")
print(base64.b64encode(png).decode())' "$MATERIAL_BYTES")"

ids=()
for attempt in 1 2 3 4; do
  code="$(curl -s -o /tmp/waterline_body.json -w '%{http_code}' -X POST "$BASE/v1/videos" \
    -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' \
    -d "{\"model\":\"seedance-2.0\",\"prompt\":\"waterline check $attempt\",\"duration\":99,\"resolution\":\"720p\",\"content\":[{\"type\":\"image_url\",\"role\":\"reference_image\",\"image_url\":{\"url\":\"data:image/png;base64,$PNG_B64\"}}]}")"
  [ "$code" = "400" ] || { echo "FAIL: 第 $attempt 次提交期望 400，实际 $code"; cleanup; exit 1; }
  ids+=("$(psql "SELECT id FROM temporary_assets WHERE purpose='reference' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1;")")
  active="$(psql "SELECT COUNT(*) || ' 个 / ' || COALESCE(SUM(size_bytes),0) || ' 字节' FROM temporary_assets WHERE purpose='reference' AND deleted_at IS NULL;")"
  echo "  第 $attempt 次提交后存活素材：$active"
done

echo "== 校验：占用不超水位，且最早的那份被清掉 =="
active_bytes="$(psql "SELECT COALESCE(SUM(size_bytes),0) FROM temporary_assets WHERE purpose='reference' AND deleted_at IS NULL;")"
active_count="$(psql "SELECT COUNT(*) FROM temporary_assets WHERE purpose='reference' AND deleted_at IS NULL;")"
echo "  活跃参考素材：${active_count} 个 / ${active_bytes} 字节（水位 ${WATERLINE_BYTES}）"
python3 - "$active_bytes" "$WATERLINE_BYTES" <<'PYEOF' || { echo "FAIL: 占用超过水位线"; cleanup; exit 1; }
import sys
active, waterline = int(sys.argv[1]), int(sys.argv[2])
assert active <= waterline, "占用 %s 超过水位 %s" % (active, waterline)
PYEOF

for index in 0 1 2 3; do
  id="${ids[$index]}"
  [ -n "$id" ] || continue
  deleted="$(psql "SELECT deleted_at IS NOT NULL FROM temporary_assets WHERE id='$id';")"
  echo "  第 $((index + 1)) 份素材 deleted=${deleted}"
done
oldest="${ids[0]}"
[ "$(psql "SELECT deleted_at IS NOT NULL FROM temporary_assets WHERE id='$oldest';")" = "t" ] \
  || { echo "FAIL: 最早的素材应被提前清理"; cleanup; exit 1; }
newest="${ids[3]}"
[ "$(psql "SELECT deleted_at IS NOT NULL FROM temporary_assets WHERE id='$newest';")" = "f" ] \
  || { echo "FAIL: 最新写入的素材不应被清理"; cleanup; exit 1; }

echo "== 清理并还原 =="
cleanup
"${COMPOSE[@]}" exec -T sub2api rm -f /tmp/waterline_body.json 2>/dev/null
rm -f /tmp/waterline_body.json
echo "WATERLINE_OK"
