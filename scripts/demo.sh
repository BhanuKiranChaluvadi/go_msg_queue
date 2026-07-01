#!/usr/bin/env bash
#
# demo.sh — end-to-end walkthrough of the medconnect appointment lifecycle.
#
# It exercises the running HTTP API the way a doctor, a patient, and a pharmacist
# would, printing what each step does and whether the result matched expectation.
# Every step is a PASS/FAIL check, and the script exits non-zero if any check
# fails — so it doubles as a smoke test.
#
# What it demonstrates, in order:
#   1. Doctor publishes an availability slot.
#   2. Patient can see the doctor's open slot.
#   3. Patient books the slot (the appointment is created).
#   4. The slot is now booked — it disappears from the open-slots list.
#   5. The doctor can see the appointment in their upcoming list.
#   6. The patient can see the appointment they booked.
#   7. A non-participant (a pharmacist) is forbidden from seeing it.
#   8. Nobody can "update" an appointment — there is no update endpoint by
#      design, so PATCH/PUT return 405 Method Not Allowed. (Appointments are
#      immutable once booked; the lifecycle moves via dedicated actions such as
#      dispatch, not field edits.)
#   9. The "one appointment per patient-doctor pair" rule holds — a second
#      booking is rejected with 409 Conflict.
#
# Prerequisites: bash, curl, and jq. Start the server first (in another
# terminal):   make run
#
# Usage:
#   ./scripts/demo.sh                 # against http://localhost:8080
#   BASE=http://host:port ./scripts/demo.sh
#
# Note: run this against a freshly started server. The demo tenant seeds a single
# doctor and a single patient, and a patient may hold only one appointment per
# doctor, so the booking step only succeeds on a clean store. If it has already
# run, restart the server (make run) before running again.

set -uo pipefail

BASE="${BASE:-http://localhost:8080}"

# Demo identities (tenant "demo", seeded by the dev server). Passed to curl as
# unquoted strings so they word-split into repeated -H flags.
DOC='-H X-Tenant-ID:demo -H X-User-ID:doctor'
PAT='-H X-Tenant-ID:demo -H X-User-ID:patient'
PHARM='-H X-Tenant-ID:demo -H X-User-ID:pharmacist'

# ---- presentation helpers ---------------------------------------------------
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  RED=$'\033[31m'; GREEN=$'\033[32m'; YELLOW=$'\033[33m'; BOLD=$'\033[1m'; DIM=$'\033[2m'; RESET=$'\033[0m'
else
  RED=; GREEN=; YELLOW=; BOLD=; DIM=; RESET=
fi

PASS=0
FAIL=0

section() { printf '\n%s══ %s ══%s\n' "$BOLD" "$1" "$RESET"; }
note()    { printf '%s%s%s\n' "$DIM" "$1" "$RESET"; }
pass()    { printf '  %s✓ PASS%s %s\n' "$GREEN" "$RESET" "$1"; PASS=$((PASS + 1)); }
fail()    { printf '  %s✗ FAIL%s %s\n' "$RED" "$RESET" "$1"; FAIL=$((FAIL + 1)); }

# http METHOD URL HEADERS [BODY] -> sets global CODE and BODY.
http() {
  local method="$1" url="$2" headers="$3" body="${4:-}" raw
  if [ -n "$body" ]; then
    raw=$(curl -sS -m 10 -w $'\n%{http_code}' -X "$method" $headers -d "$body" "$url")
  else
    raw=$(curl -sS -m 10 -w $'\n%{http_code}' -X "$method" $headers "$url")
  fi
  CODE="${raw##*$'\n'}"
  BODY="${raw%$'\n'*}"
}

show() { printf '  %s→ %s%s\n' "$DIM" "$(echo "$BODY" | jq -c . 2>/dev/null || echo "$BODY")" "$RESET"; }

# check DESCRIPTION EXPECTED_CODE -> PASS if CODE matches, else FAIL.
check() {
  if [ "$CODE" = "$2" ]; then
    pass "$1 (HTTP $CODE)"
  else
    fail "$1 — expected HTTP $2, got $CODE"
    printf '         response: %s\n' "$BODY"
  fi
}

# ---- preflight --------------------------------------------------------------
command -v jq >/dev/null 2>&1 || { echo "${RED}error:${RESET} jq is required (apt-get install jq)"; exit 2; }

printf '%smedconnect demo — %s%s\n' "$BOLD" "$BASE" "$RESET"
if ! curl -sf -m 5 "$BASE/healthz" >/dev/null; then
  echo "${RED}error:${RESET} server not reachable at $BASE"
  echo "start it in another terminal with:  make run"
  exit 1
fi
note "server is up (GET /healthz ok)"

# ---- 1. doctor publishes a slot --------------------------------------------
section "1. Doctor publishes an availability slot"
note "POST /v1/timeslots as the doctor. The slot starts life 'open'."
http POST "$BASE/v1/timeslots" "$DOC" '{"start":"2027-03-01T09:00:00Z","end":"2027-03-01T09:30:00Z"}'
show
check "slot created" 201
SLOT=$(echo "$BODY" | jq -r .id)
if [ "$(echo "$BODY" | jq -r .status)" = "open" ]; then pass "new slot status is 'open'"; else fail "new slot is not open"; fi
note "slot id = $SLOT"

# ---- 2. patient sees the open slot -----------------------------------------
section "2. Patient can see the doctor's open slot"
note "GET /v1/doctors/doctor/timeslots as the patient — lists the doctor's open slots."
http GET "$BASE/v1/doctors/doctor/timeslots" "$PAT"
show
check "list open slots" 200
if echo "$BODY" | jq -e --arg s "$SLOT" '[.data[].id] | index($s) != null' >/dev/null; then
  pass "the patient sees the doctor's slot"
else
  fail "the slot is not visible to the patient"
fi

# ---- 3. patient books the slot ---------------------------------------------
section "3. Patient books the slot"
note "POST /v1/appointments {doctorId, timeslotId}. This atomically books the slot and creates the appointment."
http POST "$BASE/v1/appointments" "$PAT" "{\"doctorId\":\"doctor\",\"timeslotId\":\"$SLOT\"}"
show
if [ "$CODE" = "409" ]; then
  fail "booking returned 409 Conflict"
  echo
  echo "${YELLOW}This patient already has an appointment with this doctor on this server.${RESET}"
  echo "${YELLOW}Restart the server for a clean run:  make run  (then re-run this script).${RESET}"
  exit 1
fi
check "appointment booked" 201
APPT=$(echo "$BODY" | jq -r .id)
note "appointment id = $APPT"

# ---- 4. the slot is now booked ---------------------------------------------
section "4. Check back: the slot is now booked"
note "Re-list the doctor's OPEN slots. The booked slot must no longer appear (it flipped from open to booked)."
http GET "$BASE/v1/doctors/doctor/timeslots" "$PAT"
show
if echo "$BODY" | jq -e --arg s "$SLOT" '[.data[].id] | index($s) == null' >/dev/null; then
  pass "the booked slot is gone from the open list"
else
  fail "the booked slot is still listed as open"
fi

# ---- 5. the doctor can see the appointment ---------------------------------
section "5. The doctor can see the appointment"
note "GET /v1/appointments/next as the doctor — their upcoming appointments."
http GET "$BASE/v1/appointments/next" "$DOC"
show
check "doctor's upcoming list" 200
if echo "$BODY" | jq -e --arg a "$APPT" '[.data[].id] | index($a) != null' >/dev/null; then
  pass "the doctor sees the new appointment"
else
  fail "the appointment is missing from the doctor's list"
fi

# ---- 6. the patient can see the appointment --------------------------------
section "6. The patient can see the appointment"
note "GET /v1/appointments/{id} as the patient — the full overview (appointment + notes + prescriptions)."
http GET "$BASE/v1/appointments/$APPT" "$PAT"
show
check "patient reads the appointment" 200
if [ "$(echo "$BODY" | jq -r .appointment.id)" = "$APPT" ]; then
  pass "the returned appointment matches the one booked"
else
  fail "unexpected appointment payload"
fi

# ---- 7. a non-participant is forbidden -------------------------------------
section "7. A non-participant cannot see it"
note "GET /v1/appointments/{id} as the pharmacist (neither the booking patient nor the appointment's doctor)."
http GET "$BASE/v1/appointments/$APPT" "$PHARM"
show
check "pharmacist is forbidden" 403

# ---- 8. can the appointment be updated? ------------------------------------
section "8. Can the patient or doctor update the appointment?"
note "There is no update endpoint by design — a booked appointment is immutable."
note "So PATCH and PUT on the appointment return 405 Method Not Allowed."
http PATCH "$BASE/v1/appointments/$APPT" "$DOC" '{"start":"2027-03-02T09:00:00Z"}'
check "doctor PATCH is rejected" 405
http PUT "$BASE/v1/appointments/$APPT" "$PAT" '{"start":"2027-03-02T09:00:00Z"}'
check "patient PUT is rejected" 405
ALLOW=$(curl -s -o /dev/null -D - -X PATCH "$BASE/v1/appointments/$APPT" $DOC -d '{}' | awk 'tolower($1) == "allow:" { $1=""; sub(/^ /, ""); print }' | tr -d '\r')
note "supported methods on this route: ${ALLOW:-GET} (read-only)"

# ---- 9. one-appointment-per-pair invariant ---------------------------------
section "9. Booking invariant: at most one appointment per patient-doctor pair"
note "The same patient tries to book the same doctor again — the service rejects it with 409 Conflict."
http POST "$BASE/v1/appointments" "$PAT" "{\"doctorId\":\"doctor\",\"timeslotId\":\"$SLOT\"}"
show
check "duplicate booking is rejected" 409

# ---- summary ----------------------------------------------------------------
echo
if [ "$FAIL" -eq 0 ]; then
  printf '%sAll %d checks passed.%s\n' "$GREEN" "$PASS" "$RESET"
  exit 0
else
  printf '%s%d passed, %d failed.%s\n' "$RED" "$PASS" "$FAIL" "$RESET"
  exit 1
fi
