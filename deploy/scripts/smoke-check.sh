#!/bin/sh
set -eu

OPERATOR_BASE_URL="${1:-https://router.vectra-pro.net}"
ROUTER_API_BASE_URL="${2:-https://api.vectra-pro.net}"
OPERATOR_USER="${3:-${VECTRA_OPERATOR_USER:-}}"
OPERATOR_PASSWORD="${4:-${VECTRA_OPERATOR_PASSWORD:-}}"

check_status() {
  URL="$1"
  METHOD="${2:-GET}"
  BODY="${3:-}"
  AUTH_USER="${4:-}"
  AUTH_PASSWORD="${5:-}"

  if [ "${METHOD}" = "POST" ]; then
    if [ -n "${AUTH_USER}" ]; then
      curl -sS -o /dev/null -w "%{http_code}" \
        -X POST \
        -H "content-type: application/json" \
        -u "${AUTH_USER}:${AUTH_PASSWORD}" \
        -d "${BODY}" \
        "${URL}"
    else
      curl -sS -o /dev/null -w "%{http_code}" \
        -X POST \
        -H "content-type: application/json" \
        -d "${BODY}" \
        "${URL}"
    fi
  else
    if [ -n "${AUTH_USER}" ]; then
      curl -sS -o /dev/null -w "%{http_code}" \
        -u "${AUTH_USER}:${AUTH_PASSWORD}" \
        "${URL}"
    else
      curl -sS -o /dev/null -w "%{http_code}" \
        "${URL}"
    fi
  fi
}

ROOT_STATUS="$(check_status "${OPERATOR_BASE_URL}/" "GET" "" "${OPERATOR_USER}" "${OPERATOR_PASSWORD}")"
OPERATOR_HEALTH_STATUS="$(check_status "${OPERATOR_BASE_URL}/healthz")"
APP_HEALTH_STATUS="$(check_status "${OPERATOR_BASE_URL}/api/health")"
API_HEALTH_STATUS="$(check_status "${ROUTER_API_BASE_URL}/healthz")"
ROUTER_STATUS="$(check_status "${ROUTER_API_BASE_URL}/api/router/register" "POST" "{}")"

echo "GET operator / => ${ROOT_STATUS}"
echo "GET operator /healthz => ${OPERATOR_HEALTH_STATUS}"
echo "GET operator /api/health => ${APP_HEALTH_STATUS}"
echo "GET router API /healthz => ${API_HEALTH_STATUS}"
echo "POST router API /api/router/register => ${ROUTER_STATUS}"

case "${ROOT_STATUS}" in
  200|302|307|401) ;;
  *)
    echo "Unexpected root status: ${ROOT_STATUS}" >&2
    exit 1
    ;;
esac

case "${OPERATOR_HEALTH_STATUS}" in
  200) ;;
  *)
    echo "Unexpected operator health status: ${OPERATOR_HEALTH_STATUS}" >&2
    exit 1
    ;;
esac

case "${API_HEALTH_STATUS}" in
  200) ;;
  *)
    echo "Unexpected router API health status: ${API_HEALTH_STATUS}" >&2
    exit 1
    ;;
esac

case "${APP_HEALTH_STATUS}" in
  200) ;;
  *)
    echo "Unexpected app health status: ${APP_HEALTH_STATUS}" >&2
    exit 1
    ;;
esac

case "${ROUTER_STATUS}" in
  200|201|202|400|401|403|404|405|422)
    ;;
  *)
    echo "Unexpected router API status: ${ROUTER_STATUS}" >&2
    exit 1
    ;;
esac
