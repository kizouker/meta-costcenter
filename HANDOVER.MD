# Handover: Meta Cost Center macOS arm64 port

## Context

The goal is to determine how far `github.com/omenking/meta-costcenter` can be ported to macOS arm64, while preserving explicit limitations for Linux-only behavior. The work is being done in the fork `kizouker/meta-costcenter`, branch `macos-arm64-port`.

## Current state

- The branch has been pushed to the fork.
- The local working tree contains uncommitted porting and documentation changes; these must be preserved.
- The requirements are in `specs/requirements.md` as Gherkin-style scenarios.
- The architecture draft is in `design/SAD.md`.
- The first System Context view exists as `design/system-context.puml` and `design/workspace.dsl`.
- Local tickets and GitHub issues exist for the macOS port requirements.
- Full macOS verification is not complete yet.

## Changes already present

- GoReleaser includes Linux and Darwin.
- The installer and README mention macOS.
- TUI terminal handling has Linux and Darwin files using OS-specific `x/sys/unix` constants.
- The main TUI code no longer directly uses Linux-only `syscall.TCGETS`/`TCSETS`.

## Important technical findings

- Billing detection in `internal/costcenter/billing.go` reads Linux `/proc/<pid>/{stat,environ,cmdline}`.
- macOS has no standard `/proc` equivalent that should be assumed by the application.
- `cmd/meta-cost/main_test.go` contains Linux-specific PTY constants and needs platform-aware tests or build tags.
- Web tests may fail in a restricted environment because they bind localhost sockets; this is an environment constraint, not necessarily an application failure.
- The application is a user-space Go program reading Muse JSONL logs, storing normalized data in SQLite, and exposing CLI/TUI/web views.

## Questions for critical review

1. Is the OS boundary in the design at the right abstraction level, or should process inspection and terminal handling be separate interfaces?
2. Is it acceptable to mark billing detection as unavailable on macOS, or should there be a documented fallback strategy?
3. Are the Darwin terminal ioctl choices safe for the supported macOS terminals and testable without brittle PTY assumptions?
4. Should the standalone PlantUML/Structurizr files become the single source of truth, with `SAD.md` linking to them?
5. Which claims in the README, installer, and GoReleaser configuration still need evidence from macOS arm64?
6. What is the smallest useful cross-platform test matrix for Linux baseline plus native macOS verification?

## Review request

Please act as a skeptical architecture reviewer. Identify incorrect assumptions, hidden Linux coupling, unsafe behavior changes, missing tests, and scope that is too large for an incremental contribution. Suggest concrete, low-risk next steps, but do not assume that Linux `/proc` functionality can be emulated on macOS.

