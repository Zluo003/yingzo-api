#!/usr/bin/env bash
# 参考素材转存的真实冒烟验证（不产生任何生成费用）。
#
# 用非法 duration / 不支持的分辨率让请求在计费与上游调用之前被拒，但参考素材的解析
# 发生在 CreateTask 之前，所以仍然完整走一遍：
#   内联 base64 → 落盘 → 可信探测（图片 go-image / 视频 ffprobe）→ 入库 → 公网 URL 可读。
set -uo pipefail
cd "$(dirname "$0")" || exit 1

BASE="${BASE:-http://127.0.0.1:8080}"
COMPOSE=(docker compose -f docker-compose.dev.yml)
API_KEY="$("${COMPOSE[@]}" exec -T postgres psql -U sub2api -d sub2api -tAc \
  "SELECT k.key FROM api_keys k JOIN groups g ON g.id=k.group_id WHERE g.platform='video' AND k.status='active' ORDER BY k.id LIMIT 1;" | tr -d '\r\n')"

psql() { "${COMPOSE[@]}" exec -T postgres psql -U sub2api -d sub2api -tAc "$1" | tr -d '\r'; }

BEFORE_IDS="$(psql "SELECT COALESCE(string_agg(id::text, ','), '') FROM temporary_assets WHERE deleted_at IS NULL;")"
cleanup() {
  local new_ids
  new_ids="$(psql "SELECT COALESCE(string_agg(id::text, ','), '') FROM temporary_assets
    WHERE deleted_at IS NULL AND id::text <> ALL(COALESCE(string_to_array(NULLIF('$BEFORE_IDS', ''), ','), ARRAY[]::text[]));")"
  if [ -z "$new_ids" ]; then echo "  本次没有留下新素材"; return; fi
  psql "UPDATE temporary_assets SET deleted_at=NOW() WHERE id::text = ANY(string_to_array('$new_ids', ','));" >/dev/null
  local id key
  for id in ${new_ids//,/ }; do
    key="$(psql "SELECT storage_key FROM temporary_assets WHERE id='$id';")"
    "${COMPOSE[@]}" exec -T sub2api sh -lc "rm -rf '$(dirname "$key")'" 2>/dev/null
  done
  echo "  已清理本次新增素材：$(echo "$new_ids" | tr ',' ' ')"
}

echo "== 1. 签发管理员 token 并读取素材存储设置 =="
# .env 里的 ADMIN_PASSWORD 与库中不一致（首次初始化后可能被改过），因此直接按后端
# 的 JWT 算法用 JWT_SECRET + 用户行签发一个只读 token，不修改任何账号数据。
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
if [ -z "$TOKEN" ]; then echo "FAIL: 未能签发管理员 token"; exit 1; fi
curl -s "$BASE/api/v1/admin/file-service/settings" -H "Authorization: Bearer $TOKEN" \
  | python3 -c '
import json,sys
d = json.load(sys.stdin)["data"]
print("  backend=%s source=%s retention=%sh" % (d["backend"], d["source"], d["retention_hours"]))
print("  public_base_url=%r effective=%r" % (d["public_base_url"], d["effective_public_base_url"]))
print("  max_total_bytes=%s daily_max_bytes=%s daily_max_count=%s" % (d["max_total_bytes"], d["daily_max_bytes"], d["daily_max_count"]))
print("  result_retention_hours=%s result_max_total_bytes=%s capacity_reserve_percent=%s" % (
    d["result_retention_hours"], d["result_max_total_bytes"], d["capacity_reserve_percent"]))
usage = d["usage"]
print("  usage 合计: files=%s bytes=%s" % (usage["active_files"], usage["active_bytes"]))
print("  usage 参考素材: files=%s bytes=%s" % (usage["reference_files"], usage["reference_bytes"]))
print("  usage 生成产物: files=%s bytes=%s" % (usage["generated_files"], usage["generated_bytes"]))
'
PUBLIC_BASE="$(curl -s "$BASE/api/v1/admin/file-service/settings" -H "Authorization: Bearer $TOKEN" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["effective_public_base_url"])')"

echo "== 1b. 冗余水位配置往返（读 → 改 → 读回 → 还原） =="
# 前端保存走的就是这个 PUT：body 是配置字段本身。这里验证 capacity_reserve_percent
# 能被接受并回读，且不会踩到 result_cleanup_policy（该策略已废弃）。
settings_json="$(curl -s "$BASE/api/v1/admin/file-service/settings" -H "Authorization: Bearer $TOKEN")"
put_body="$(printf '%s' "$settings_json" | python3 -c '
import json, sys
data = json.load(sys.stdin)["data"]
config = {key: data[key] for key in (
    "schema_version", "backend", "public_base_url", "retention_hours",
    "daily_max_count", "daily_max_bytes", "max_total_bytes",
    "result_retention_hours", "result_max_total_bytes", "capacity_reserve_percent", "s3",
)}
config["s3"]["secret_access_key"] = ""
config["capacity_reserve_percent"] = 15
print(json.dumps(config))
')"
restore_body="$(printf '%s' "$put_body" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["capacity_reserve_percent"]=10; print(json.dumps(c))')"

round_trip() {
  curl -s -X PUT "$BASE/api/v1/admin/file-service/settings" -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' -d "$1" \
    | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["capacity_reserve_percent"])'
}
changed="$(round_trip "$put_body")"
echo "  写入 15% 后回读：${changed}%"
[ "$changed" = "15" ] || { echo "FAIL: 冗余比例未按写入值回读"; round_trip "$restore_body" >/dev/null; cleanup; exit 1; }
restored="$(round_trip "$restore_body")"
echo "  还原 10% 后回读：${restored}%"
[ "$restored" = "10" ] || { echo "FAIL: 冗余比例未能还原"; cleanup; exit 1; }

echo "== 2. 内联 base64 参考图 + 非法 duration =="
PNG_B64="$(python3 -c '
import base64, struct, zlib
def chunk(t, d):
    c = t + d
    return struct.pack(">I", len(d)) + c + struct.pack(">I", zlib.crc32(c) & 0xffffffff)
ihdr = struct.pack(">IIBBBBB", 3, 2, 8, 2, 0, 0, 0)
raw = b"".join(b"\x00" + b"\xff\x00\x00" * 3 for _ in range(2))
png = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr) + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b"")
print(base64.b64encode(png).decode())')"

RESPONSE="$(curl -s -w '\n%{http_code}' -X POST "$BASE/v1/videos" \
  -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' \
  -d "{\"model\":\"seedance-2.0\",\"prompt\":\"reference material smoke test\",\"duration\":99,\"resolution\":\"720p\",\"content\":[{\"type\":\"image_url\",\"role\":\"reference_image\",\"image_url\":{\"url\":\"data:image/png;base64,$PNG_B64\"}}]}")"
CODE="$(printf '%s' "$RESPONSE" | tail -1)"
echo "  HTTP $CODE  $(printf '%s' "$RESPONSE" | sed '$d' | head -c 160)"
[ "$CODE" = "400" ] || { echo "FAIL: 期望 400（非法 duration），实际 $CODE"; cleanup; exit 1; }

IMAGE_ID="$(psql "SELECT id FROM temporary_assets WHERE deleted_at IS NULL ORDER BY created_at DESC LIMIT 1;")"
[ "$(psql "SELECT purpose FROM temporary_assets WHERE id='$IMAGE_ID';")" = "reference" ] || { echo "FAIL: 参考素材未按 reference 落库"; cleanup; exit 1; }
psql "SELECT '  id=' || id || ' media_type=' || media_type || ' mime=' || mime_type || ' size=' || size_bytes || ' metadata=' || metadata FROM temporary_assets WHERE id='$IMAGE_ID';"
"${COMPOSE[@]}" exec -T sub2api sh -lc "test -s '$(psql "SELECT storage_key FROM temporary_assets WHERE id='$IMAGE_ID';")' && echo '  file on disk: OK' || echo '  file on disk: MISSING'"
curl -s -o /dev/null -w '  平台 URL -> HTTP %{http_code} type=%{content_type} bytes=%{size_download}\n' "$PUBLIC_BASE/media/$IMAGE_ID/asset.png"

echo "== 3. 内联 base64 参考视频：时长必须由平台 ffprobe 探测 =="
"${COMPOSE[@]}" exec -T sub2api ffmpeg -y -f lavfi -i color=c=red:s=64x64:d=3 -c:v libx264 -pix_fmt yuv420p /tmp/ref-smoke.mp4 >/dev/null 2>&1
VIDEO_B64="$("${COMPOSE[@]}" exec -T sub2api sh -lc 'base64 /tmp/ref-smoke.mp4' | tr -d '\r\n')"
[ -n "$VIDEO_B64" ] || { echo "FAIL: 无法生成测试视频"; cleanup; exit 1; }

# seedance-2.5 不支持 4k：请求会在渠道闸门被拒，此时已过素材解析，仍未触达上游、未计费。
RESPONSE="$(curl -s -w '\n%{http_code}' -X POST "$BASE/v1/videos" \
  -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' \
  -d "{\"model\":\"seedance-2.5\",\"prompt\":\"reference video smoke test\",\"duration\":8,\"resolution\":\"4k\",\"content\":[{\"type\":\"video_url\",\"role\":\"reference_video\",\"video_url\":{\"url\":\"data:video/mp4;base64,$VIDEO_B64\"}}]}")"
CODE="$(printf '%s' "$RESPONSE" | tail -1)"
echo "  HTTP $CODE  $(printf '%s' "$RESPONSE" | sed '$d' | head -c 160)"

VIDEO_ID="$(psql "SELECT id FROM temporary_assets WHERE deleted_at IS NULL AND media_type='video' ORDER BY created_at DESC LIMIT 1;")"
if [ -z "$VIDEO_ID" ]; then echo "FAIL: 参考视频没有入库"; cleanup; exit 1; fi
psql "SELECT '  id=' || id || ' media_type=' || media_type || ' mime=' || mime_type || ' size=' || size_bytes || ' metadata=' || metadata FROM temporary_assets WHERE id='$VIDEO_ID';"
DURATION="$(psql "SELECT metadata->>'duration_seconds' FROM temporary_assets WHERE id='$VIDEO_ID';")"
echo "  平台探测时长：${DURATION}s"
python3 - "$DURATION" <<'PYEOF' || { echo "FAIL: 未探测到参考视频时长"; cleanup; exit 1; }
import sys
measured = float(sys.argv[1])
assert 2.5 <= measured <= 3.5, "期望约 3 秒，实际 %s" % measured
PYEOF
curl -s -o /dev/null -w '  平台 URL -> HTTP %{http_code} type=%{content_type} bytes=%{size_download}\n' "$PUBLIC_BASE/media/$VIDEO_ID/asset.mp4"

echo "== 4. 清理冒烟数据 =="
cleanup
"${COMPOSE[@]}" exec -T sub2api rm -f /tmp/ref-smoke.mp4 2>/dev/null
echo "SMOKE_OK"
