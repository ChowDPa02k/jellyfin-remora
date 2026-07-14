# Response to the 2026-07-14 external code review

This document records the disposition of every finding in the supplied external
review. Severity labels and identifiers follow that report.

| Finding | Disposition | Result |
|---|---|---|
| M1 initialization retry storm | Accepted | First-start failures use 1/2/4/8-second backoff. The fifth consecutive failure stops the incomplete server, enters `PROCESS_FAILED`, and requires `remoractl start` to reset. |
| M2 duplicate health request/dead branch | Accepted | Readiness and failure accounting share one health result. The timeout/bootstrap condition uses the previous sample timestamp with explicit grouping. |
| L1 substring argument adoption | Accepted | Darwin discovery reads exact NUL-delimited argv through `kern.procargs2`; prefixes and paths containing spaces have deterministic tests. |
| L2 adopted uptime starts at adoption | Accepted | Darwin reads `ps etime`; `ProcessInfo.StartedAt` is retained by the process manager, with current time only as a fail-safe fallback. |
| L3 implicit operator precedence | Accepted | The surviving multi-clause health condition is explicitly grouped and named. |
| L4 ignored `http.NewRequest` error | Accepted | Request construction errors are returned and tested. |
| L5 `localhost` resolution TOCTOU | Accepted | Every resolved address must be loopback and the chosen IPv4-preferred result is pinned in the transport; redirects cannot change the host. |
| L6 one probe child per check | Not changed | This is a deliberate HA boundary. A context cannot reliably cancel a kernel-blocked filesystem syscall; a disposable child can be killed without wedging the supervisor. The cost is controlled by disk intervals and is now explicit in the architecture document. |
| N1 patched Go toolchain guidance | Accepted | `CONTRIBUTING.md` explains the exact patched-version policy and `GOTOOLCHAIN=auto`. A separate `toolchain` directive would duplicate the minimum version already declared by `go.mod`. |
| N2 duplicate optional decoding | Accepted | `Optional[T].UnmarshalYAML` delegates to the shared helper. |
| N3 three atomic writers | Not consolidated | Their ownership policies intentionally differ. Jellyfin XML must preserve Jellyfin ownership; Remora-owned state and secrets must not inherit destination ownership. The distinction is documented before any future mechanical consolidation. |
| N5 duplicate watchdog-ready assignment | Accepted | The caller-side duplicate assignment was removed; `ensureWatchdog` owns the state transition. |
| N6 API key/session-token semantics | Clarified | The fallback API key authorizes administrator API operations only. `/Users/Me` uses the cached access token returned by the watchdog user's own authentication; a code comment now makes this boundary explicit. |
| N7 crash-slice reuse readability | Accepted | The safe in-place filtering invariant is documented beside the operation. |

The review contains no N4 item; no disposition was inferred for an absent finding.
