---
status: observed-manual
date: 2026-07-31
evidence_type:
  - runtime
source:
  - project-owner
---

# H5 browser manual smoke and CSRF correction

## Claim boundary

The project owner manually started the four Go backend processes and Vue H5,
opened `http://localhost:5173`, registered an account and confirmed that the
authenticated snapshot flow completed.

This is owner-reported manual runtime evidence. It is not an automated browser
test, screenshot evidence, persistence evidence or a capacity measurement.

## Defect found during the run

The first browser attempt registered the account but failed before Ticket
issuance with `CSRF_REJECTED`.

Root cause:

- registration established an authenticated Session;
- H5 fetched a fresh CSRF proof after registration;
- `GET /v1/auth/csrf` incorrectly created that proof as anonymous even when a
  valid Session cookie was present;
- Ticket issuance correctly required a Session-bound proof and rejected it.

Correction:

- `GET /v1/auth/csrf` now binds a newly issued proof to the current Session
  when one is present;
- an HTTP handler regression test reproduces
  `anonymous CSRF -> register -> authenticated CSRF -> issue Ticket`;
- targeted auth tests, the complete Go suite and `go vet ./...` passed after
  the correction.

The project owner restarted the services, registered again and confirmed the
browser flow succeeded.

## Current limitation

Login and Zone still use explicit in-memory development adapters. Successful
browser registration and snapshot display do not prove durable registration,
MySQL checkpoints, Dirty writeback, restart recovery or production capacity.
