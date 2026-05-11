#!/usr/bin/env bash
set -u

BASE_URL="${BASE_URL:-http://localhost:4000}"
CUSTOMER_ID="${CUSTOMER_ID:-acme-1}"
CURL_CONNECT_TIMEOUT="${CURL_CONNECT_TIMEOUT:-2}"
CURL_MAX_TIME="${CURL_MAX_TIME:-15}"

PASS=0
FAIL=0

green='\033[0;32m'
red='\033[0;31m'
yellow='\033[1;33m'
reset='\033[0m'

tmp_body=""
tmp_code=""

cleanup() {
  [[ -n "${tmp_body:-}" && -f "$tmp_body" ]] && rm -f "$tmp_body"
  [[ -n "${tmp_code:-}" && -f "$tmp_code" ]] && rm -f "$tmp_code"
}
trap cleanup EXIT

log_pass() {
  PASS=$((PASS + 1))
  printf "%bPASS%b %s\n" "$green" "$reset" "$1"
}

log_fail() {
  FAIL=$((FAIL + 1))
  printf "%bFAIL%b %s\n" "$red" "$reset" "$1"
  if [[ $# -gt 1 ]]; then
    printf "  %s\n" "$2"
  fi
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf "%bMissing dependency:%b %s\n" "$red" "$reset" "$1"
    exit 1
  fi
}

request() {
	request_as "$CUSTOMER_ID" "$@"
}

request_as() {
	local customer_id="$1"
	shift
	local method="$1"
	local path="$2"
	local data="${3:-}"

  [[ -n "${tmp_body:-}" && -f "$tmp_body" ]] && rm -f "$tmp_body"
  [[ -n "${tmp_code:-}" && -f "$tmp_code" ]] && rm -f "$tmp_code"

		tmp_body="$(mktemp)"
	tmp_code="$(mktemp)"

	if [[ -n "$data" ]]; then
		curl -sS --connect-timeout "$CURL_CONNECT_TIMEOUT" --max-time "$CURL_MAX_TIME" \
			-o "$tmp_body" -w "%{http_code}" \
			-X "$method" \
			-H "Content-Type: application/json" \
			-H "customer-id: $customer_id" \
			-d "$data" \
			"$BASE_URL$path" >"$tmp_code" || printf "000" >"$tmp_code"
	else
		curl -sS --connect-timeout "$CURL_CONNECT_TIMEOUT" --max-time "$CURL_MAX_TIME" \
			-o "$tmp_body" -w "%{http_code}" \
			-X "$method" \
			-H "customer-id: $customer_id" \
			"$BASE_URL$path" >"$tmp_code" || printf "000" >"$tmp_code"
	fi
}

request_no_customer() {
	local method="$1"
	local path="$2"
	local data="${3:-}"

	[[ -n "${tmp_body:-}" && -f "$tmp_body" ]] && rm -f "$tmp_body"
	[[ -n "${tmp_code:-}" && -f "$tmp_code" ]] && rm -f "$tmp_code"

	tmp_body="$(mktemp)"
	tmp_code="$(mktemp)"

	if [[ -n "$data" ]]; then
		curl -sS --connect-timeout "$CURL_CONNECT_TIMEOUT" --max-time "$CURL_MAX_TIME" \
			-o "$tmp_body" -w "%{http_code}" \
			-X "$method" \
			-H "Content-Type: application/json" \
			-d "$data" \
			"$BASE_URL$path" >"$tmp_code" || printf "000" >"$tmp_code"
	else
		curl -sS --connect-timeout "$CURL_CONNECT_TIMEOUT" --max-time "$CURL_MAX_TIME" \
			-o "$tmp_body" -w "%{http_code}" \
			-X "$method" \
			"$BASE_URL$path" >"$tmp_code" || printf "000" >"$tmp_code"
	fi
}

status_code() {
  cat "$tmp_code"
}

body() {
  cat "$tmp_body"
}

json_field() {
  jq -r "$1" "$tmp_body"
}

assert_status() {
  local expected="$1"
  local label="$2"
  local actual
  actual="$(status_code)"

  if [[ "$actual" == "$expected" ]]; then
    log_pass "$label"
  else
    log_fail "$label" "expected status $expected, got $actual; body: $(body)"
  fi
}

assert_json_eq() {
  local filter="$1"
  local expected="$2"
  local label="$3"
  local actual
  actual="$(json_field "$filter")"

  if [[ "$actual" == "$expected" ]]; then
    log_pass "$label"
  else
    log_fail "$label" "expected $filter == $expected, got $actual; body: $(body)"
  fi
}

assert_json_nonempty() {
  local filter="$1"
  local label="$2"
  local actual
  actual="$(json_field "$filter")"

  if [[ -n "$actual" && "$actual" != "null" ]]; then
    log_pass "$label"
  else
    log_fail "$label" "expected $filter to be non-empty; body: $(body)"
  fi
}

assert_json_contains_id() {
  local id="$1"
  local label="$2"
  local found
  found="$(jq -r --arg id "$id" 'any(.bills[]?; .id == $id)' "$tmp_body")"

  if [[ "$found" == "true" ]]; then
    log_pass "$label"
  else
    log_fail "$label" "expected bills list to contain $id; body: $(body)"
  fi
}

assert_json_missing_id() {
	local id="$1"
	local label="$2"
	local found
	found="$(jq -r --arg id "$id" 'any(.bills[]?; .id == $id)' "$tmp_body")"

	if [[ "$found" == "false" ]]; then
		log_pass "$label"
	else
		log_fail "$label" "expected bills list to exclude $id; body: $(body)"
	fi
}

wait_for_bill() {
  local id="$1"
  local label="$2"
  local attempts=30
  local delay=0.2

  for _ in $(seq 1 "$attempts"); do
    request GET "/bills/$id"
    if [[ "$(status_code)" == "200" ]]; then
      log_pass "$label"
      return 0
    fi
    sleep "$delay"
  done

  log_fail "$label" "bill $id was not readable after ${attempts} attempts; last status $(status_code); body: $(body)"
  return 1
}

wait_for_line_items() {
  local id="$1"
  local expected="$2"
  local label="$3"
  local attempts=30
  local delay=0.2

  for _ in $(seq 1 "$attempts"); do
    request GET "/bills/$id"
    if [[ "$(status_code)" == "200" && "$(json_field '.line_items | length')" == "$expected" ]]; then
      log_pass "$label"
      return 0
    fi
    sleep "$delay"
  done

  log_fail "$label" "bill $id did not reach $expected line items after ${attempts} attempts; last status $(status_code); body: $(body)"
  return 1
}

print_section() {
  printf "\n%b%s%b\n" "$yellow" "$1" "$reset"
}

require_cmd curl
require_cmd jq

print_section "Phase 0: Health"
request GET "/health"
assert_status 200 "health returns 200"
assert_json_eq '.status' 'ok' "health status ok"
assert_json_eq '.temporal_reachable' 'true' "temporal reachable"

print_section "Phase 1: USD Happy Path"
request POST "/bills" '{"currency":"USD"}'
assert_status 200 "create USD bill returns 200"
assert_json_eq '.status' 'open' "USD bill starts open"
assert_json_eq '.currency' 'USD' "USD bill currency set"
assert_json_nonempty '.id' "USD bill id present"
usd_id="$(json_field '.id')"
usd_workflow_id="$(json_field '.workflow_id')"
if [[ "$usd_id" == "$usd_workflow_id" ]]; then
  log_pass "workflow_id matches bill id"
else
  log_fail "workflow_id matches bill id" "id=$usd_id workflow_id=$usd_workflow_id"
fi
wait_for_bill "$usd_id" "USD bill persisted" || exit 1

request POST "/bills/$usd_id/line-items" '{"description":"widget","quantity":2,"unit_price":"3.50"}'
assert_status 200 "add USD line item 1 returns 200"
assert_json_eq '.amount' '7.00' "USD line item 1 amount"

request POST "/bills/$usd_id/line-items" '{"description":"gadget","quantity":1,"unit_price":"10.00"}'
assert_status 200 "add USD line item 2 returns 200"
assert_json_eq '.amount' '10.00' "USD line item 2 amount"

request POST "/bills/$usd_id/line-items" '{"description":"service","quantity":3,"unit_price":"5.00"}'
assert_status 200 "add USD line item 3 returns 200"
assert_json_eq '.amount' '15.00' "USD line item 3 amount"

wait_for_line_items "$usd_id" 3 "USD line items persisted" || exit 1

request POST "/bills/$usd_id/close"
assert_status 200 "close USD bill returns 200"
assert_json_eq '.status' 'closed' "USD bill closed"
assert_json_eq '.currency' 'USD' "closed USD bill currency"
assert_json_eq '.total' '32.00' "USD total uses integer math"
assert_json_eq '.line_items | length' '3' "closed USD bill has 3 line items"

request GET "/bills/$usd_id"
assert_status 200 "get USD bill returns 200"
assert_json_eq '.status' 'closed' "get USD bill status closed"
assert_json_eq '.total' '32.00' "get USD bill total"
assert_json_eq '.line_items | length' '3' "get USD bill line item count"

request GET "/bills"
assert_status 200 "list bills returns 200"
assert_json_contains_id "$usd_id" "list bills contains USD bill"

print_section "Phase 1a: Tenant Isolation"
other_customer_id="other-customer"

request_as "$other_customer_id" GET "/bills/$usd_id"
assert_status 404 "cross-tenant get bill returns 404"

request_as "$other_customer_id" POST "/bills/$usd_id/line-items" '{"description":"intruder","quantity":1,"unit_price":"1.00"}'
assert_status 404 "cross-tenant add line item returns 404"

request_as "$other_customer_id" POST "/bills/$usd_id/close"
assert_status 404 "cross-tenant close bill returns 404"

request_as "$other_customer_id" GET "/bills"
assert_status 200 "cross-tenant list bills returns 200"
assert_json_missing_id "$usd_id" "cross-tenant list excludes USD bill"

request_no_customer POST "/bills" '{"currency":"USD"}'
assert_status 401 "create bill without customer-id returns 401"

request_no_customer GET "/bills"
assert_status 401 "list bills without customer-id returns 401"

request_no_customer GET "/bills/$usd_id"
assert_status 401 "get bill without customer-id returns 401"

print_section "Phase 2: GEL Happy Path"
request POST "/bills" '{"currency":"GEL"}'
assert_status 200 "create GEL bill returns 200"
assert_json_eq '.currency' 'GEL' "GEL bill currency set"
gel_id="$(json_field '.id')"
wait_for_bill "$gel_id" "GEL bill persisted" || exit 1

request POST "/bills/$gel_id/line-items" '{"description":"khachapuri","quantity":1,"unit_price":"1.00"}'
assert_status 200 "add GEL line item 1 returns 200"
assert_json_eq '.amount' '1.00' "GEL line item 1 amount"

request POST "/bills/$gel_id/line-items" '{"description":"lobiani","quantity":1,"unit_price":"2.00"}'
assert_status 200 "add GEL line item 2 returns 200"
assert_json_eq '.amount' '2.00' "GEL line item 2 amount"

wait_for_line_items "$gel_id" 2 "GEL line items persisted" || exit 1

request POST "/bills/$gel_id/close"
assert_status 200 "close GEL bill returns 200"
assert_json_eq '.currency' 'GEL' "closed GEL bill currency"
assert_json_eq '.total' '3.00' "GEL total uses integer math"

print_section "Phase 3: Input Validation"
request POST "/bills" '{"currency":"EUR"}'
assert_status 400 "invalid currency returns 400"

request POST "/bills" '{"currency":""}'
assert_status 400 "empty currency returns 400"

request POST "/bills" '{"currency":"USD"}'
assert_status 200 "create validation target bill returns 200"
validation_id="$(json_field '.id')"
wait_for_bill "$validation_id" "validation target bill persisted" || exit 1

request POST "/bills/$validation_id/line-items" '{"description":"bad","quantity":0,"unit_price":"1.00"}'
assert_status 400 "zero quantity returns 400"

request POST "/bills/$validation_id/line-items" '{"description":"bad","quantity":-1,"unit_price":"1.00"}'
assert_status 400 "negative quantity returns 400"

request POST "/bills/$validation_id/line-items" '{"description":"bad","quantity":1,"unit_price":"0"}'
assert_status 400 "zero unit_price returns 400"

request POST "/bills/$validation_id/line-items" '{"description":"bad","quantity":1,"unit_price":"-1.00"}'
assert_status 400 "negative unit_price returns 400"

print_section "Phase 4: State Integrity"
missing_id="00000000-0000-0000-0000-000000000000"

request POST "/bills/$missing_id/line-items" '{"description":"missing","quantity":1,"unit_price":"1.00"}'
assert_status 404 "add to missing bill returns 404"

request POST "/bills/$missing_id/close"
assert_status 404 "close missing bill returns 404"

request GET "/bills/$missing_id"
assert_status 404 "get missing bill returns 404"

request POST "/bills/$usd_id/line-items" '{"description":"too late","quantity":1,"unit_price":"1.00"}'
assert_status 409 "add to closed bill returns 409"

request POST "/bills/$usd_id/close"
assert_status 409 "double close returns 409"

print_section "Phase 5: Edge Cases"
request POST "/bills" '{"currency":"USD"}'
assert_status 200 "create zero-item bill returns 200"
zero_id="$(json_field '.id')"
wait_for_bill "$zero_id" "zero-item bill persisted" || exit 1

request POST "/bills/$zero_id/close"
assert_status 200 "close zero-item bill returns 200"
assert_json_eq '.total' '0.00' "zero-item bill total is 0"
assert_json_eq '.line_items | length' '0' "zero-item bill has no line items"

request POST "/bills" '{"currency":"USD"}'
assert_status 200 "create large-value bill returns 200"
large_id="$(json_field '.id')"
wait_for_bill "$large_id" "large-value bill persisted" || exit 1

request POST "/bills/$large_id/line-items" '{"description":"large","quantity":999,"unit_price":"9999999.99"}'
assert_status 200 "add large-value line item returns 200"
assert_json_eq '.amount' '9989999990.01' "large-value item amount"

wait_for_line_items "$large_id" 1 "large-value line item persisted" || exit 1

request POST "/bills/$large_id/close"
assert_status 200 "close large-value bill returns 200"
assert_json_eq '.total' '9989999990.01' "large-value bill total"

request POST "/bills" '{"currency":"USD"}'
assert_status 200 "create open-bill inspection target returns 200"
open_id="$(json_field '.id')"
wait_for_bill "$open_id" "open-bill inspection target persisted" || exit 1

request POST "/bills/$open_id/line-items" '{"description":"open-item","quantity":1,"unit_price":"1.23"}'
assert_status 200 "add item to open-bill inspection target returns 200"

wait_for_line_items "$open_id" 1 "open-bill inspection item persisted" || exit 1

request GET "/bills/$open_id"
assert_status 200 "get open bill returns 200"
assert_json_eq '.status' 'open' "open bill status remains open"
assert_json_eq '.total' 'null' "open bill total is null"
assert_json_eq '.line_items | length' '1' "open bill line item visible"

printf "\n%bSummary%b %d passed, %d failed\n" "$yellow" "$reset" "$PASS" "$FAIL"

if [[ "$FAIL" -gt 0 ]]; then
  exit 1
fi
