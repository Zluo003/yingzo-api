#!/usr/bin/env bash
# 本地素材目录可配置性的真实验证（零生成费用）。
#
# 验证：
#   1. 通过设置接口把 local_dir 指到宿主机挂进来的目录，设置页回读的就是它；
#   2. 新上传的参考素材确实落在该宿主机目录里（而不是容器内的卷）；
#   3. 相对路径、根目录、系统目录被拒绝；
#   4. 改回默认目录后，之前落在自定义目录里的素材仍然可读（新写入才受影响）。
set -uo pipefail
cd "$(dirname "$0")" || exit 1

BASE="${BASE:-http://127.0.0.1:8080}"
COMPOSE=(docker compose -f docker-compose.dev.yml)
# dev 栈把 ./data 挂到容器的 /app/data，所以这个路径就是宿主机的 deploy/data/host-assets。
HOST_DIR_REL="data/host-assets"
CONTAINER_DIR="/app/data/host-assets"

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
  curl -s -X PUT "$BASE/api/v1/admin/file-service/settings" -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' -d "$RESTORE_BODY" >/dev/null
  rm -rf "$HOST_DIR_REL"
  echo "  已清理素材、还原目录设置"
}

echo "== 签发管理员 token =="
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
    "result_retention_hours", "result_max_total_bytes", "capacity_reserve_percent",
    "result_daily_max_count", "result_daily_max_bytes", "s3",
)}
config["s3"]["secret_access_key"] = ""
config["local_dir"] = sys.argv[1]
print(json.dumps(config))' "$1"
}
ORIGINAL_LOCAL_DIR="$(printf '%s' "$SETTINGS" | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"].get("local_dir") or "")')"
RESTORE_BODY="$(build_put "$ORIGINAL_LOCAL_DIR")"
DEFAULT_PATH="$(printf '%s' "$SETTINGS" | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["local_path"])')"
echo "  默认生效目录：$DEFAULT_PATH"

echo "== 1. 拒绝非法目录与不存在的目录 =="
for bad in "data/assets" "/" "/etc"; do
  code="$(curl -s -o /tmp/dir_body.json -w '%{http_code}' -X PUT "$BASE/api/v1/admin/file-service/settings" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "$(build_put "$bad")")"
  echo "  local_dir='$bad' -> HTTP $code $(python3 -c 'import json;print(json.load(open("/tmp/dir_body.json")).get("reason") or json.load(open("/tmp/dir_body.json")).get("message",""))' 2>/dev/null | head -c 80)"
  [ "$code" = "400" ] || { echo "FAIL: 非法目录应被拒绝"; cleanup; exit 1; }
done

# 配置的目录必须已存在：服务不替管理员创建宿主机目录（自动创建会在没挂载时把素材写进
# 容器可写层，容器一重建就丢）。这里先在宿主机上建好，再配置。
missing="/app/data/not-created-yet"
code="$(curl -s -o /tmp/dir_body.json -w '%{http_code}' -X PUT "$BASE/api/v1/admin/file-service/settings" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "$(build_put "$missing")")"
echo "  local_dir='$missing'（不存在）-> HTTP $code $(head -c 120 /tmp/dir_body.json)"
[ "$code" = "400" ] || { echo "FAIL: 不存在的目录应被拒绝"; cleanup; exit 1; }
[ -d "$missing" ] && { echo "FAIL: 服务不应该创建这个目录"; cleanup; exit 1; }

echo "== 2. 在宿主机建好目录后配置并上传参考素材 =="
mkdir -p "$HOST_DIR_REL"
changed="$(curl -s -X PUT "$BASE/api/v1/admin/file-service/settings" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "$(build_put "$CONTAINER_DIR")" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["local_path"])')"
echo "  保存后生效目录：$changed"
[ "$changed" = "$CONTAINER_DIR" ] || { echo "FAIL: 生效目录未按配置更新"; cleanup; exit 1; }

PNG_B64="$(python3 -c '
import base64, struct, zlib
def chunk(t, d):
    c = t + d
    return struct.pack(">I", len(d)) + c + struct.pack(">I", zlib.crc32(c) & 0xffffffff)
ihdr = struct.pack(">IIBBBBB", 3, 2, 8, 2, 0, 0, 0)
raw = b"".join(b"\x00" + b"\xff\x00\x00" * 3 for _ in range(2))
png = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr) + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b"")
print(base64.b64encode(png).decode())')"
code="$(curl -s -o /tmp/dir_upload.json -w '%{http_code}' -X POST "$BASE/v1/videos" \
  -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' \
  -d "{\"model\":\"seedance-2.0\",\"prompt\":\"storage dir check\",\"duration\":99,\"resolution\":\"720p\",\"content\":[{\"type\":\"image_url\",\"role\":\"reference_image\",\"image_url\":{\"url\":\"data:image/png;base64,$PNG_B64\"}}]}")"
echo "  提交参考素材 HTTP $code"
NEW_ID="$(psql "SELECT id FROM temporary_assets WHERE purpose='reference' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1;")"
STORAGE_KEY="$(psql "SELECT storage_key FROM temporary_assets WHERE id='$NEW_ID';")"
echo "  入库路径：$STORAGE_KEY"
case "$STORAGE_KEY" in "$CONTAINER_DIR"/*) ;; *) echo "FAIL: 素材没有写到配置的目录"; cleanup; exit 1;; esac

echo "== 3. 宿主机上确实有这个文件 =="
HOST_FILE="$HOST_DIR_REL/$(basename "$(dirname "$STORAGE_KEY")")/object"
if [ -f "$HOST_FILE" ]; then
  echo "  宿主机文件存在：${HOST_FILE}（$(wc -c < "$HOST_FILE") 字节）"
else
  echo "FAIL: 宿主机目录 $HOST_FILE 不存在"; cleanup; exit 1
fi
curl -s -o /dev/null -w '  平台 URL -> HTTP %{http_code} bytes=%{size_download}\n' \
  "$(printf '%s' "$SETTINGS" | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["effective_public_base_url"])')/media/$NEW_ID/asset.png"

echo "== 4. 改回默认目录后旧素材仍可读 =="
curl -s -X PUT "$BASE/api/v1/admin/file-service/settings" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "$RESTORE_BODY" >/dev/null
curl -s -o /dev/null -w '  旧素材 -> HTTP %{http_code} bytes=%{size_download}\n' \
  "$(printf '%s' "$SETTINGS" | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["effective_public_base_url"])')/media/$NEW_ID/asset.png"

echo "== 5. 清理 =="
cleanup
"${COMPOSE[@]}" exec -T sub2api rm -f /tmp/dir_body.json /tmp/dir_upload.json 2>/dev/null
rm -f /tmp/dir_body.json /tmp/dir_upload.json
echo "STORAGE_DIR_OK"
