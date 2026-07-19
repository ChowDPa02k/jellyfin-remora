# 2026-07-19 external review response

This document records the final disposition of all 70 findings reviewed for
`v0.9.0-beta.6` through `v0.9.0-beta.9`. The accepted findings were delivered
as 63 independent commits; none were squashed. Seven findings were rejected or
withdrawn because they conflict with an intentional compatibility contract or
describe no actionable runtime representation.

The follow-up report reused three identifiers after withdrawing earlier items.
To remove ambiguity, rows below use the identifiers from the approved repair
plan and show the follow-up report's canonical identifier where it differs.
Thus accepted `N-H3`, `N-M5`, and `N-M17`, for example, are distinct from the
withdrawn legacy `N-H2`, `N-M4`, and `N-M16` findings.

## Accepted findings

`Gate` identifies the complete validation round described below. Every commit
also records its targeted package tests in its commit message.

| Plan ID | Follow-up ID | Commit | Disposition | Gate |
|---|---|---|---|---|
| C1 | C1 | `ba48f80` | Added non-reusable process-generation identity and PID revalidation | R1 |
| C2 | C2 | `6a4c9c2` | Bounded archive entries, individual files, and total expansion | R2 |
| H1 | H1 | `de8c70b` | Require every storage result to be healthy before recovery | R1 |
| H2 | H2 | `ed33eb1` | Removed Interactive Users from the Windows pipe ACL | R3 |
| H3 | H3 | `09d95a3` | Made loopback TCP an explicit opt-in for new configurations | R3 |
| H4 | H4 | `77cd60a` | Completed database-console parsing across EOF, chunks, long lines, and ANSI | R1 |
| H5 | H5 | `13a4fb5` | Removed access tokens from API-key revocation errors | R3 |
| H6 | H6 | `b1c5b2b` | Enforced positive intervals, thresholds, and log retention controls | R4 |
| H8 | H8 | `8a37446` | Moved leftover-probe discovery outside the I/O deadline and mutex | R1 |
| H9 | H9 | `5c99d5e` | Terminated escaped Darwin/Linux descendants reliably | R1 |
| M1 | M1 | `a8acfa3` | Exposed stop failures and added bounded retry backoff | R1 |
| M2 | M2 | `c8e3ae8` | Excluded manual stops from crash-circuit accounting | R1 |
| M3 | M3 | `018790d` | Persisted manual intent despite unrelated storage failure | R1 |
| M4 | M4 | `dd05789` | Preserved nullable `enable-https` semantics | R4 |
| M5 | M5 | `fdb3ffd` | Required absolute resolved Remora state and log paths | R4 |
| M6 | M6 | `055325f` | Rejected Darwin `run-as-user: root` and numeric `0` | R4 |
| M7 | M7 | `7453174` | Bounded custom assets and rejected symlink path components | R4 |
| M8 | M8 | `cbcee5f` | Propagated directory fsync failures and rolled back the current XML plan | R4 |
| M9 | M9 | `30822c7` | Made mount-source identity mismatch immediately fatal | R1 |
| M10 | M10 | `c472c0a` | Skipped isolated malformed Linux mountinfo records | R1 |
| M11 | M11 | `0cb186f` | Tracked and removed files left by killed probe helpers | R1 |
| M12 | M12 | `d0b1a0c` | Enforced owner-only Unix configuration permissions | R3 |
| M13 | M13 | `b4302ab` | Preserved uid/gid across atomic `edit-config` replacement | R3 |
| M14 | M14 | `466c58a` | Removed sensitive CLI temporary files on SIGINT/SIGTERM | R2 |
| M15 | M15 | `fb74b0f` | Applied the redirect guard to every loopback literal | R3 |
| M16 | M16 | `3e35b8d` | Disabled redirects in the Jellyfin HTTP client | R3 |
| N-H1 | N-H1 | `0be3813` | Bound administrator-password submission to the current process generation | R3 |
| N-H3 | N-H2 | `9e47821` | Added a local safety mirror and fail-closed remote-state recovery | R1 |
| N-M1 | N-M1 | `fc0795f` | Mapped typed persistence failures to HTTP 503 and CLI exit 3 | R3 |
| N-M2 | N-M2 | `2d06ec4` | Isolated database evidence by process generation | R1 |
| N-M3 | N-M3 | `0e8d108` | Made POSIX environment names case-sensitive and Windows names collision-safe | R4 |
| N-M5 | N-M4 | `04b1a3a` | Detached bounded single-flight immediate health checks | R3 |
| N-M6 | N-M5 | `593f75f` | Closed the active followed log descriptor after rotation | R3 |
| N-M7 | N-M6 | `13b63ca` | Detached accepted operations from request cancellation | R3 |
| N-M8 | N-M7 | `8ce96a7` | Put adopted Windows processes and descendants in the Job Object | R1 |
| N-M9 | N-M8 | `942e616` | Preferred private Unix runtime sockets and authenticated fallback peers | R3 |
| N-M10 | N-M9 | `0a23c8d` | Capped successful Jellyfin response bodies at 8 MiB | R3 |
| N-M11 | N-M10 | `43e690b` | Avoided Linux start-tick overflow with quotient/remainder conversion | R1 |
| N-M12 | N-M11 | `68edf79` | Rechecked start ticks after pidfd open and continued descendant scans | R1 |
| N-M13 | N-M12 | `53c55aa` | Enumerated Windows drive-letter and folder volume mounts | R4 |
| N-M14 | N-M13 | `2841374` | Propagated Darwin UID/GID errors and honored a different target group | R4 |
| N-M15 | N-M14 | `51310d5` | Made init preserve, back up, and explicitly replace existing configuration | R2 |
| N-M17 | N-M15 | `7869d70` | Preserved failure streaks for every non-healthy storage result | R1 |
| N-M18 | N-M16 | `3fbafc7` | Thresholded infrastructure-transient path probes separately from storage errors | R1 |
| N-M19 | N-M17 | `85f682c` | Split environment validation tests by Windows and Unix semantics | R4 |
| N-L1 | N-L1 | `06eebb9` | Journaled rejected lifecycle restoration instead of replaying silently | R1 |
| N-L2 | N-L2 | `86a41a9` | Published lifecycle changes only after persistence succeeded | R1 |
| N-L3 | N-L3 | `836d8b2` | Added start/restart persistence rollback coverage | R1 |
| N-L6 | N-L6 | `a3a2577` | Required YAML string scalars for every environment name and value | R4 |
| N-L7 | N-L7 | `0800079` | Added env fuzz seeds, invariants, and schema/sample/struct parity checks | R4 |
| N-L8 | N-L8 | `60e2478` | Replaced unreachable and tautological supervisor properties | R1 |
| N-L9 | N-L9 | `5e01689` | Pinned archive limits and cleanup postconditions in fuzz coverage | R2 |
| N-L10 | N-L10 | `2645f84` | Used Darwin kernel start time and exact argv | R1 |
| N-L11 | N-L11 | `a9f301b` | Required ESRCH, not arbitrary `kill(0)` errors, for HA process exit | R1 |
| N-L12 | N-L12 | `b80fc49` | Sanitized and bounded Jellyfin API error text | R3 |
| N-L13 | N-L13 | `28c4f12` | Prevented an old console drain from contaminating a new generation | R1 |
| N-L15 | N-L15 | `a67135c` | Made short identifiers Unicode-rune safe | R4 |
| N-L16 | N-L16 | `416c018` | Hex-encoded control bytes in generated systemd path values | R4 |
| N-L17 | N-L17 | `046acb3` | Preserved 429, 5xx, and timeout repository failures | R2 |
| N-L18 | N-L18 | `30dc88c` | Covered unstable package classes in archive fallback | R2 |
| N-L19 | N-L19 | `514b206` | Verified and extracted from one open, digest-bound file description | R2 |
| N-L20 | N-L20 | `600e1af` | Added reverse install rollback and exact incomplete-cleanup manifests | R2 |
| N-L21 | N-L21 | `7b2f672` | Removed stale wait state and cleared command handles with PID state | R1 |

## Rejected or withdrawn findings

| ID | Disposition | Rationale |
|---|---|---|
| H7 | Reject | The documented macOS inline SMB credential mode remains a compatibility option; Keychain migration stays on the security roadmap. |
| legacy N-H2 | Withdrawn/reject | Official-repository validation plus the documented archive fallback remains the intended package trust contract; redirect policy was not broadened. |
| legacy N-M4 | Reject | The active proxy block in the Linux sample is an explicit distribution-compatibility default and remains enabled. |
| legacy N-M16 | Reject | Jellyfin console output, including ANSI color, remains byte-preserving by design. |
| N-L4 | Reject | Raw NUL cannot be represented in a JSON string, so no schema change is actionable. |
| N-L5 | Reject | Windows environment construction retains explicit `NAME=` entries; the documented contract remains correct for the supported boundary. |
| N-L14 | Reject | SIGHUP/SIGQUIT intentionally terminate Remora while leaving Jellyfin available for subsequent generation-safe adoption. |

## Validation results

| Gate | Version | Findings | Static and deterministic result | Live result |
|---|---|---:|---|---|
| R1 | `v0.9.0-beta.6` | 26 | Race, vet, govulncheck, five-target build, Linux/Windows cross-test pass | macOS, Rocky, and Debian process identity/adoption, storage loss/recovery, manual-stop durability, and database-generation fencing pass |
| R2 | `v0.9.0-beta.7` | 8 | Same full gate plus archive fuzz/limit/rollback tests pass | Isolated tar/zip install, capacity rejection, successful deployment, and stepwise rollback pass; no service or share modified |
| R3 | `v0.9.0-beta.8` | 15 | Same full gate plus control concurrency and log-follow tests pass | macOS and Debian socket-only, forged-socket rejection, explicit TCP, API-key recovery, generation-bound provisioning, and healthcheck concurrency pass |
| R4 | `v0.9.0-beta.9` | 14 | Same full gate plus env fuzz/schema parity and Windows platform seams pass | macOS, Rocky, and Debian init, owner-only configuration, backup, environment, launchd/systemd artifact, and ownership-preserving edit pass |

The four round totals are 26 + 8 + 15 + 14 = **63 accepted findings**.
Together with the seven rejected or withdrawn findings above, this accounts for
all **70 findings**. Detailed live steps and resource-cleanup evidence are kept
in `test/HA_TEST_MATRIX.md`.
