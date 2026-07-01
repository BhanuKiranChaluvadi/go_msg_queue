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

# Scenario details, woven into the narration below so the run reads like a story.
APPT_DATE="2027-03-01"
APPT_FROM="09:00"
APPT_TO="10:00"
APPT_DURATION="1 hour"
SLOT_START="${APPT_DATE}T09:00:00Z"
SLOT_END="${APPT_DATE}T10:00:00Z"
WHEN="${APPT_DATE}, ${APPT_FROM}–${APPT_TO} hrs (${APPT_DURATION})"

# ---- presentation helpers ---------------------------------------------------
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  RED=$'\033[31m'; GREEN=$'\033[32m'; YELLOW=$'\033[33m'; BOLD=$'\033[1m'; DIM=$'\033[2m'; RESET=$'\033[0m'
else
  RED=; GREEN=; YELLOW=; BOLD=; DIM=; RESET=
fi

PASS=0
FAIL=0

step() { printf '\n%s▶ Step %s — %s%s\n' "$BOLD" "$1" "$2" "$RESET"; }
note() { printf '  %s%s%s\n' "$DIM" "$1" "$RESET"; }
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

echo
printf '%sScenario — one appointment, three roles%s\n' "$BOLD" "$RESET"
note "Dr. \"doctor\" has clinic time on $WHEN."
note "\"patient\" books it; afterwards \"pharmacist\" (an outsider) tries to view it."
note "Each step shows the action, the API response, and a PASS/FAIL check."

# ---- 1. doctor publishes a slot --------------------------------------------
step 1 "The doctor opens a slot for $WHEN"
note "Dr. \"doctor\" advertises availability. A brand-new slot starts life 'open' (bookable)."
http POST "$BASE/v1/timeslots" "$DOC" "{\"start\":\"$SLOT_START\",\"end\":\"$SLOT_END\"}"
show
check "the slot is created" 201
SLOT=$(echo "$BODY" | jq -r .id)
if [ "$(echo "$BODY" | jq -r .status)" = "open" ]; then pass "the new slot is 'open' (bookable)"; else fail "the new slot is not open"; fi
note "slot id: $SLOT — the patient will book against this."

# ---- 2. patient sees the open slot -----------------------------------------
step 2 "The patient looks up the doctor's open slots"
note "The patient browses Dr. \"doctor\"'s availability and should see the $APPT_FROM–$APPT_TO slot listed as open."
http GET "$BASE/v1/doctors/doctor/timeslots" "$PAT"
show
check "list open slots" 200
if echo "$BODY" | jq -e --arg s "$SLOT" '[.data[].id] | index($s) != null' >/dev/null; then
  pass "the patient sees the doctor's slot"
else
  fail "the slot is not visible to the patient"
fi

# ---- 3. patient books the slot ---------------------------------------------
step 3 "The patient books the $APPT_FROM–$APPT_TO appointment ($APPT_DURATION)"
note "The patient reserves Dr. \"doctor\"'s slot on $APPT_DATE. Booking is atomic: the slot flips to booked and the appointment is created in one step."
http POST "$BASE/v1/appointments" "$PAT" "{\"doctorId\":\"doctor\",\"timeslotId\":\"$SLOT\"}"
show
if [ "$CODE" = "409" ]; then
  fail "booking returned 409 Conflict"
  echo
  echo "${YELLOW}This patient already has an appointment with this doctor on this server.${RESET}"
  echo "${YELLOW}Restart the server for a clean run:  make run  (then re-run this script).${RESET}"
  exit 1
fi
check "the appointment is booked" 201
APPT=$(echo "$BODY" | jq -r .id)
note "appointment id: $APPT"

# ---- 4. the slot is now booked ---------------------------------------------
step 4 "We check back — is the slot really booked now?"
note "The patient lists the doctor's OPEN slots again. The $APPT_FROM–$APPT_TO slot should have disappeared, because it is now booked (no longer open)."
http GET "$BASE/v1/doctors/doctor/timeslots" "$PAT"
show
if echo "$BODY" | jq -e --arg s "$SLOT" '[.data[].id] | index($s) == null' >/dev/null; then
  pass "the booked slot is gone from the open list"
else
  fail "the booked slot is still listed as open"
fi

# ---- 5. the doctor can see the appointment ---------------------------------
step 5 "The doctor sees the appointment on their schedule"
note "Dr. \"doctor\" opens their upcoming list and should find the new $APPT_FROM appointment with the patient."
http GET "$BASE/v1/appointments/next" "$DOC"
show
check "doctor's upcoming list" 200
if echo "$BODY" | jq -e --arg a "$APPT" '[.data[].id] | index($a) != null' >/dev/null; then
  pass "the doctor sees the new appointment"
else
  fail "the appointment is missing from the doctor's list"
fi

# ---- 6. the patient can see the appointment --------------------------------
step 6 "The patient sees the appointment they booked"
note "The patient opens the appointment and gets the full picture: the booking plus any notes and prescriptions (none yet)."
http GET "$BASE/v1/appointments/$APPT" "$PAT"
show
check "patient reads the appointment" 200
if [ "$(echo "$BODY" | jq -r .appointment.id)" = "$APPT" ]; then
  pass "the returned appointment matches the one booked"
else
  fail "unexpected appointment payload"
fi

# ---- 7. a non-participant is forbidden -------------------------------------
step 7 "An outsider (the pharmacist) is turned away"
note "The pharmacist is neither the booking patient nor the appointment's doctor, so viewing this appointment is forbidden."
http GET "$BASE/v1/appointments/$APPT" "$PHARM"
show
check "pharmacist is forbidden" 403

# ---- 8. can the appointment be updated? ------------------------------------
step 8 "Can the appointment be changed after booking?"
note "No — a booked appointment is immutable in this design; there is no 'update appointment' endpoint."
note "So when the doctor or patient tries to edit it (PATCH/PUT), the server answers 405 Method Not Allowed."
http PATCH "$BASE/v1/appointments/$APPT" "$DOC" '{"start":"2027-03-02T09:00:00Z"}'
check "doctor PATCH is rejected" 405
http PUT "$BASE/v1/appointments/$APPT" "$PAT" '{"start":"2027-03-02T09:00:00Z"}'
check "patient PUT is rejected" 405
ALLOW=$(curl -s -o /dev/null -D - -X PATCH "$BASE/v1/appointments/$APPT" $DOC -d '{}' | awk 'tolower($1) == "allow:" { $1=""; sub(/^ /, ""); print }' | tr -d '\r')
note "supported methods on this route: ${ALLOW:-GET} (read-only)"

# ---- 9. one-appointment-per-pair invariant ---------------------------------
step 9 "The patient tries to book the same doctor a second time"
note "A patient may hold at most one appointment per doctor. The doctor opens another free slot (11:00–12:00) and the patient tries to book it too — the service rejects it with 409 Conflict."
http POST "$BASE/v1/timeslots" "$DOC" "{\"start\":\"${APPT_DATE}T11:00:00Z\",\"end\":\"${APPT_DATE}T12:00:00Z\"}"
SLOT2=$(echo "$BODY" | jq -r .id)
http POST "$BASE/v1/appointments" "$PAT" "{\"doctorId\":\"doctor\",\"timeslotId\":\"$SLOT2\"}"
show
check "a second booking with the same doctor is rejected" 409

# ---- summary ----------------------------------------------------------------
echo
if [ "$FAIL" -eq 0 ]; then
  printf '%sAll %d checks passed.%s\n' "$GREEN" "$PASS" "$RESET"
  exit 0
else
  printf '%s%d passed, %d failed.%s\n' "$RED" "$PASS" "$FAIL" "$RESET"
  exit 1
fi
