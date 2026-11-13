---
name: asc-testflight
description: >-
  Manage Rex Go (dev.gorex.ios, App ID 6831210589) TestFlight external testing,
  Beta App Review, groups, and tester invitations via the asc CLI. Use when
  checking TestFlight release readiness, updating review details, submitting a
  build for Beta App Review, or inviting external testers.
---

# Rex Go TestFlight Distribution (`asc`)

## Environment Setup

Source [ios/scripts/asc-env](../../../ios/scripts/asc-env) on macOS (it sets `PATH`, `APP`, `ASC_KEY_ID`, `ASC_ISSUER_ID`, and `ASC_PRIVATE_KEY_PATH`):

```bash
. ios/scripts/asc-env
export ASC_KEY_ID ASC_ISSUER_ID ASC_PRIVATE_KEY_PATH
```

If running from Windows, execute `asc` over SSH on a Mac that has the `.p8` key file in `~/.appstoreconnect/private_keys/` (avoid hosts whose `asc` profile relies on System Keychain, which fails over non-interactive SSH).

## 1. Check External Testing Readiness

```bash
# Latest builds & processing state
asc builds list --app "$APP" --sort -uploadedDate --limit 5 --output json

# Distribution & review state for a build
asc testflight distribution view --build-id "<BUILD_ID>" --output json
asc testflight review submissions list --build-id "<BUILD_ID>" --output json
asc api GET "/v1/builds/<BUILD_ID>/betaBuildLocalizations"

# Groups, review contact info, localizations, agreements
asc testflight groups list --app "$APP" --output json
asc testflight review view --app "$APP" --output json
asc testflight app-localizations list --app "$APP" --output json
asc testflight agreements view --app "$APP" --output json
```

## 2. Update Beta App Review Details

App Store Connect requires `contactPhone` alongside name and email when updating `betaAppReviewDetails`:

```bash
asc testflight review edit \
  --id "$APP" \
  --contact-first-name "<FIRST_NAME>" \
  --contact-last-name "<LAST_NAME>" \
  --contact-email "<EMAIL>" \
  --contact-phone "<PHONE>" \
  --demo-account-required=false \
  --notes "Rex Go is a companion terminal client for GoRex desktop (macOS, Windows, and Linux). No account sign-in is required. Without a running desktop host, you can browse the app, add remote host configurations, and explore Tailscale, Themes, and Settings. Connecting to a live terminal requires a GoRex host reachable on your local network or tailnet."
```

## 3. External Group & Tester Invitations

- External group: `Public Beta` (`de1fcb5f-8aac-4ea6-9bd2-1865d5f7a98b`)
- Keep `publicLinkEnabled=false` and invite external testers by email.

```bash
# Attach the latest build to the external group
asc builds add-groups --app "$APP" --latest --group "de1fcb5f-8aac-4ea6-9bd2-1865d5f7a98b"

# Invite an external tester by email
asc testflight testers invite --app "$APP" --email "<TESTER_EMAIL>" --group "Public Beta"
```

## 4. Submit for Beta App Review

```bash
asc testflight review submit --build-id "<BUILD_ID>" --confirm --output json
```

Verify submission status with `asc testflight review submissions list --build-id "<BUILD_ID>"`.
