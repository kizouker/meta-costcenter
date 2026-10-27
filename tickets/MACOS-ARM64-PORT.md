# macOS arm64 Port Tickets

These files mirror the GitHub issues created in the fork. They remain the local traceability record for the requirements.

- [MCC-REQ-001](https://github.com/kizouker/meta-costcenter/issues/1)
- [MCC-REQ-002](https://github.com/kizouker/meta-costcenter/issues/2)
- [MCC-REQ-003](https://github.com/kizouker/meta-costcenter/issues/3)
- [MCC-REQ-004](https://github.com/kizouker/meta-costcenter/issues/4)
- [MCC-REQ-005](https://github.com/kizouker/meta-costcenter/issues/5)
- [MCC-TKT-006](https://github.com/kizouker/meta-costcenter/issues/6)
- [MCC-TKT-007](https://github.com/kizouker/meta-costcenter/issues/7)
- [MCC-TKT-008](https://github.com/kizouker/meta-costcenter/issues/8)

## MCC-TKT-001 — Verify a macOS arm64 build and supported startup

- **REQ IDs:** MCC-REQ-001
- **Description:** Determine how far the existing CLI and dashboard start successfully in a native macOS arm64 build. Record any clear limitation.
- **Acceptance:** `@MCC-REQ-001-S01` and `@MCC-REQ-001-S02` pass.
- **Priority:** P1
- **Depends on:** None

## MCC-TKT-002 — Make the terminal dashboard portable

- **REQ IDs:** MCC-REQ-002
- **Description:** Ensure the terminal dashboard has observable macOS arm64 behavior while preserving a Linux build.
- **Acceptance:** `@MCC-REQ-002-S01` and `@MCC-REQ-002-S02` pass.
- **Priority:** P1
- **Depends on:** MCC-TKT-001

## MCC-TKT-003 — Document and preserve Linux-only limitations

- **REQ IDs:** MCC-REQ-003
- **Description:** Audit `/proc`-dependent behavior and make macOS results explicit instead of silently changing their meaning.
- **Acceptance:** `@MCC-REQ-003-S01` and `@MCC-REQ-003-S02` pass.
- **Priority:** P1
- **Depends on:** MCC-TKT-001

## MCC-TKT-004 — Add macOS arm64 distribution support

- **REQ IDs:** MCC-REQ-004
- **Description:** Update release artifacts, the installation path, and platform documentation for macOS arm64.
- **Acceptance:** `@MCC-REQ-004-S01` and `@MCC-REQ-004-S02` pass.
- **Priority:** P2
- **Depends on:** MCC-TKT-001, MCC-TKT-003

## MCC-TKT-005 — Verify tests and builds

- **REQ IDs:** MCC-REQ-005
- **Description:** Run and, where necessary, adapt native tests, the native build, and the macOS arm64 cross-build.
- **Acceptance:** `@MCC-REQ-005-S01` and `@MCC-REQ-005-S02` pass.
- **Priority:** P1
- **Depends on:** MCC-TKT-001, MCC-TKT-002, MCC-TKT-003

## Coordination tickets

These implement approved vision goals but are not software requirements.

### MCC-TKT-006 — Announce the port early

- **Vision goal:** 4
- **Description:** Publish a short announcement that the macOS arm64 port is being investigated, so other participants do not duplicate the work.
- **Acceptance:** The announcement states the project, scope, current status, and how others can coordinate.
- **Priority:** P1
- **Depends on:** None

### MCC-TKT-007 — Ask ExamPro/Andrew for help

- **Vision goal:** 2
- **Description:** Ask ExamPro/Andrew for project-owner guidance where the port reaches an unclear limitation or decision.
- **Acceptance:** The question includes observed behavior, checks already completed, and the specific help requested.
- **Priority:** P2
- **Depends on:** MCC-TKT-001, MCC-TKT-003

### MCC-TKT-008 — Ask the hackathon Discord group for help

- **Vision goal:** 3
- **Description:** Ask other participants for help with remaining macOS arm64 portability questions after the initial audit.
- **Acceptance:** The request includes reproducible evidence and avoids asking others to repeat completed work.
- **Priority:** P2
- **Depends on:** MCC-TKT-006, MCC-TKT-001

## Change log — 2026-10-27

- **PTY test build tag (MCC-REQ-005-S01):** `cmd/meta-cost/main_test.go` now has `//go:build linux`. It uses Linux-only PTY constants (`TIOCSPTLCK`, `TIOCGPTN`, `/dev/pts`), so `go test ./...` did not compile on macOS. On macOS the test is skipped; Linux still runs it. A portable PTY test remains open work.
- **Review correction:** `fileIdentity` using `syscall.Stat_t.Dev` (int32 on darwin, uint64 on Linux) compiles and is stable on both; it is unproven rather than a defect. A cross-platform test covers it.
