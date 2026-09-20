#!/usr/bin/env sh
set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_root"
set -a
if [ -f .env ]; then . ./.env; else . ./.env.example; fi
set +a

(command -v jq >/dev/null 2>&1) || { echo "jq is required for API validation" >&2; exit 1; }

(cd backend && go test ./... && go test -race ./... && go vet ./... && go build ./...)
(cd frontend && npm install --no-audit --no-fund && npm run typecheck && npm run build)
docker compose config --quiet
docker compose down -v --remove-orphans
docker compose up -d --build

cleanup() { docker compose down -v --remove-orphans; }
if [ "${KEEP_RUNNING:-0}" = "1" ]; then
  trap cleanup INT TERM
else
  trap cleanup EXIT INT TERM
fi

i=0
until curl -fsS "http://127.0.0.1:${BACKEND_PORT:-19519}/healthz" | jq -e '.data.status == "ok" and .data.database == "ready" and .data.redis == "ready"' >/dev/null; do
  i=$((i+1))
  [ "$i" -lt 60 ] || { docker compose logs; exit 1; }
  sleep 2
done
i=0
until curl -fsS "http://127.0.0.1:${FRONTEND_PORT:-18519}/" >/dev/null; do
  i=$((i+1))
  [ "$i" -lt 30 ] || { docker compose logs frontend; exit 1; }
  sleep 1
done

login_token() {
  curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"$1\",\"password\":\"Admin123!\"}" | jq -er '.data.token'
}

admin_token=$(login_token admin)
reviewer_token=$(login_token reviewer)
operator_token=$(login_token operator)
viewer_token=$(login_token viewer)

curl -fsS "http://127.0.0.1:${BACKEND_PORT:-19519}/api/session" -H "Authorization: Bearer $viewer_token" \
  | jq -e '.data.role == "viewer" and (.data.requestId | length > 0)' >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT:-19519}/api/cases?page=1&pageSize=20" -H "Authorization: Bearer $viewer_token" \
  | jq -e '.data | length >= 3' >/dev/null

viewer_write_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/cases" \
  -H "Authorization: Bearer $viewer_token" -H 'Content-Type: application/json' -d '{}')
[ "$viewer_write_status" = "403" ]
viewer_audit_status=$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:${BACKEND_PORT:-19519}/api/audits" \
  -H "Authorization: Bearer $viewer_token")
[ "$viewer_audit_status" = "403" ]

now=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
suffix=$(date +%s)
signoff_payload=$(printf '{"code":"SIGNOFF-SMOKE-%s","name":"Validated PCR result","description":"Dual-control Compose validation","facility":"Validation Veterinary Lab","owner":"Result Desk","category":"PCR","riskLevel":"high","metricValue":99.8,"metricUnit":"percent","effectiveAt":"%s","evidence":"PCR run sheet revision 1","relatedCode":"ASSAY-SMOKE"}' "$suffix" "$now")
signoff=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb519-signoff-create' \
  -d "$signoff_payload")
signoff_id=$(printf '%s' "$signoff" | jq -er '.data.id')
signoff_version=$(printf '%s' "$signoff" | jq -er '.data.version')
printf '%s' "$signoff" | jq -e '.data.status == "draft" and .data.preparedBy == "operator" and .data.version == 1 and .data.revisions[0].actor == "operator" and .data.revisions[0].requestId == "gb519-signoff-create"' >/dev/null

update_payload=$(printf '%s' "$signoff_payload" | jq --argjson version "$signoff_version" '. + {expectedVersion: $version, evidence: "PCR run sheet and control chart revision 2"} | del(.code)')
updated=$(curl -fsS -X PUT "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff/$signoff_id" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb519-signoff-update' \
  -d "$update_payload")
updated_version=$(printf '%s' "$updated" | jq -er '.data.version')
printf '%s' "$updated" | jq -e '.data.version == 2 and (.data.revisions | length) == 2 and .data.revisions[0].evidence == "PCR run sheet revision 1" and .data.revisions[1].evidence == "PCR run sheet and control chart revision 2"' >/dev/null

peer_review=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff/$signoff_id/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb519-signoff-submit' \
  -d "{\"status\":\"peer_review\",\"expectedVersion\":$updated_version,\"reason\":\"PCR controls and evidence are complete\"}")
peer_review_version=$(printf '%s' "$peer_review" | jq -er '.data.version')
printf '%s' "$peer_review" | jq -e '.data.status == "peer_review" and .data.version == 3 and (.data.revisions | length) == 3' >/dev/null

operator_sign_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff/$signoff_id/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb519-operator-sign-denied' \
  -d "{\"status\":\"signed\",\"expectedVersion\":$peer_review_version,\"reason\":\"operator must not sign\"}")
[ "$operator_sign_status" = "422" ]

signed=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff/$signoff_id/transition" \
  -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb519-signoff-signed' \
  -d "{\"status\":\"signed\",\"expectedVersion\":$peer_review_version,\"reason\":\"independent veterinary result review passed\"}")
printf '%s' "$signed" | jq -e '
  .data.status == "signed" and .data.version == 4 and .data.preparedBy == "operator" and .data.reviewedBy == "reviewer"
  and (.data.revisions | length) == 4
  and ([.data.revisions[] | select((.evidence | length) > 0 and (.actor | length) > 0 and (.requestId | length) > 0)] | length) == 4
  and [.data.revisions[].requestId] == ["gb519-signoff-create","gb519-signoff-update","gb519-signoff-submit","gb519-signoff-signed"]' >/dev/null

admin_payload=$(printf '{"code":"SIGNOFF-SELF-%s","name":"Self review guard","description":"Separation of duty validation","facility":"Validation Veterinary Lab","owner":"Admin Desk","category":"PCR","riskLevel":"medium","metricValue":98,"metricUnit":"percent","effectiveAt":"%s","evidence":"self review guard evidence","relatedCode":"ASSAY-SELF"}' "$suffix" "$now")
admin_signoff=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff" \
  -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb519-self-create' -d "$admin_payload")
admin_id=$(printf '%s' "$admin_signoff" | jq -er '.data.id')
admin_version=$(printf '%s' "$admin_signoff" | jq -er '.data.version')
admin_review=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff/$admin_id/transition" \
  -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb519-self-submit' \
  -d "{\"status\":\"peer_review\",\"expectedVersion\":$admin_version,\"reason\":\"submit admin draft for review\"}")
admin_review_version=$(printf '%s' "$admin_review" | jq -er '.data.version')
same_actor_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff/$admin_id/transition" \
  -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb519-self-sign-denied' \
  -d "{\"status\":\"signed\",\"expectedVersion\":$admin_review_version,\"reason\":\"same actor must be rejected\"}")
[ "$same_actor_status" = "422" ]

curl -fsS "http://127.0.0.1:${BACKEND_PORT:-19519}/api/audits/ResultSignoff/$signoff_id?limit=10" \
  -H "Authorization: Bearer $reviewer_token" \
  | jq -e '[.data[].requestId] | index("gb519-signoff-create") != null and index("gb519-signoff-update") != null and index("gb519-signoff-submit") != null and index("gb519-signoff-signed") != null' >/dev/null

# 复核更正：异人 reviewer/admin 才能对 signed 记录发起复核，须填原因与证据。
review_open_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff/$signoff_id/reviews" \
  -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' \
  -d '{"reason":"missing evidence body"}')
[ "$review_open_status" = "400" ]
review_self_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:19519/api/signoff/$signoff_id/reviews" \
  -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' \
  -d '{"reason":"original signer must not review own signoff","evidence":"self review forbidden by separation of duty"}')
[ "$review_self_status" = "422" ]

review=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff/$signoff_id/reviews" \
  -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb519-review-open' \
  -d '{"reason":"post-release metric discrepancy found","evidence":"re-run QC sheet contradicts signed value"}')
review_id=$(printf '%s' "$review" | jq -er '.data.reviews[0].id')
printf '%s' "$review" | jq -e '.data.status == "signed" and .data.reviews[0].status == "open" and .data.reviews[0].openedBy == "admin"' >/dev/null

# 重复/并发发起只允许一条在办复核。
review_dup_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff/$signoff_id/reviews" \
  -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json' \
  -d '{"reason":"duplicate correction request","evidence":"duplicate evidence body attached"}')
[ "$review_dup_status" = "409" ]

# 原签发人不能裁决自己签发的结果。
review_self_decide=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff/$signoff_id/reviews/decision" \
  -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' \
  -d "{\"reviewId\":$review_id,\"decision\":\"upheld\",\"reason\":\"original signer decision must be rejected\",\"expectedVersion\":$review_id}")
[ "$review_self_decide" = "422" ]

# 同意保留原 signed 版本并创建关联 draft，随后重提并经异人签发生效。
upheld=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff/$signoff_id/reviews/decision" \
  -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb519-review-uphold' \
  -d "{\"reviewId\":$review_id,\"decision\":\"upheld\",\"reason\":\"deviation confirmed, create correction draft\",\"expectedVersion\":$review_id}")
correction_id=$(printf '%s' "$upheld" | jq -er '.data.reviews[0].draftId')
printf '%s' "$upheld" | jq -e --argjson cid "$correction_id" '
  .data.status == "signed" and .data.version == 4
  and .data.reviews[0].status == "upheld" and .data.reviews[0].draftId == $cid
  and (.data.correctionDrafts | length) == 1
  and .data.correctionDrafts[0].id == $cid and .data.correctionDrafts[0].status == "draft"
  and .data.correctionDrafts[0].preparedBy == "admin"
  and .data.correctionDrafts[0].correctionOfId == (.data.id)' >/dev/null

correction_submit=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff/$correction_id/transition" \
  -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb519-correction-submit' \
  -d '{"status":"peer_review","expectedVersion":1,"reason":"corrected evidence ready for review"}')
correction_submit_version=$(printf '%s' "$correction_submit" | jq -er '.data.version')
correction_signed=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff/$correction_id/transition" \
  -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb519-correction-signed' \
  -d "{\"status\":\"signed\",\"expectedVersion\":$correction_submit_version,\"reason\":\"corrected result independently verified\"}")
printf '%s' "$correction_signed" | jq -e --argjson oid "$signoff_id" '
  .data.status == "signed" and .data.preparedBy == "admin" and .data.reviewedBy == "reviewer"
  and .data.correctionOfId == $oid and .data.correctionSource.status == "signed"' >/dev/null
# 旧 signed 版本原样保留、仍可查询，复核已关闭且只生成一份更正草稿。
curl -fsS "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff/$signoff_id" -H "Authorization: Bearer $admin_token" \
  | jq -e '.data.status == "signed" and .data.version == 4 and (.data.revisions | length) == 4
      and (.data.reviews | map(select(.status == "open")) | length) == 0
      and (.data.correctionDrafts | length) == 1' >/dev/null

# 驳回只关复核、原结果不变；关闭后可发起新一轮复核。
seed_review=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff?page=1&pageSize=20&search=RS-003" \
  -H "Authorization: Bearer $admin_token")
seed_id=$(printf '%s' "$seed_review" | jq -er '.data[0].id')
seed_version=$(printf '%s' "$seed_review" | jq -er '.data[0].version')
seed_open=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff/$seed_id/reviews" \
  -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json' \
  -d '{"reason":"seed result complaint under verification","evidence":"external complaint document attached"}')
seed_review_id=$(printf '%s' "$seed_open" | jq -er '.data.reviews[0].id')
seed_rejected=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT:-19519}/api/signoff/$seed_id/reviews/decision" \
  -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json' \
  -d "{\"reviewId\":$seed_review_id,\"decision\":\"rejected\",\"reason\":\"re-check confirms the signed value is correct\",\"expectedVersion\":$seed_review_id}")
printf '%s' "$seed_rejected" | jq -e --argjson v "$seed_version" '.data.status == "signed" and .data.version == $v
  and .data.reviews[0].status == "rejected" and (.data.correctionDrafts | length) == 0' >/dev/null

curl -fsS "http://127.0.0.1:${BACKEND_PORT:-19519}/api/audit-summary?windowHours=24" -H "Authorization: Bearer $admin_token" \
  | jq -e '.data.total >= 6 and .data.transitions >= 3 and .data.uniqueActors >= 2' >/dev/null

docker compose ps
if [ "${KEEP_RUNNING:-0}" = "1" ]; then
  echo "KEEP_RUNNING=1: containers left running for built-in Browser validation"
fi
