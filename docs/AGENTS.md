# KyDrive operator documentation

## Purpose
Own restore instructions, local acceptance evidence and captured product UI assets.

## Ownership
`RESTORE.md` is the recovery runbook. `ACCEPTANCE.md` distinguishes local evidence from production gates. `PILOT.md` records live deployment evidence and remaining gates. Root README owns configuration; root STATUS owns handoff.

## Local Contracts
- Document exact verified behavior; local fixtures do not establish live suite integration or production readiness.
- Keep credentials, opened capsules and custodian shares out of documentation and screenshots.
- Recovery instructions must restore the sealed integration configuration and the exact bound bulk snapshot; never treat metadata alone as a complete restore.

## Work Guidance
- Update acceptance evidence when a gate is actually exercised. Use actual product screenshots with capture conditions.

## Verification
- Check referenced paths exist. Restore checks are in `internal/backup`; native browser captures are described in root `UI-VERIFICATION.md`.

## Child DOX Index
