# Backlog

These entries track the GitHub issues created in the fork for the macOS arm64 port.

| Ticket | Title | Priority | Status |
|---|---|---|---|
| [#1](https://github.com/kizouker/meta-costcenter/issues/1) | Verify macOS arm64 build and startup | P1 | Open |
| [#2](https://github.com/kizouker/meta-costcenter/issues/2) | Make terminal dashboard portable | P1 | Open |
| [#3](https://github.com/kizouker/meta-costcenter/issues/3) | Document Linux-only limitations | P1 | Open |
| [#4](https://github.com/kizouker/meta-costcenter/issues/4) | Add macOS arm64 distribution support | P2 | Open |
| [#5](https://github.com/kizouker/meta-costcenter/issues/5) | Verify tests and builds | P1 | Open |
| [#6](https://github.com/kizouker/meta-costcenter/issues/6) | Announce the port early | P1 | Open |
| [#7](https://github.com/kizouker/meta-costcenter/issues/7) | Ask ExamPro/Andrew for help | P2 | Open |
| [#8](https://github.com/kizouker/meta-costcenter/issues/8) | Ask hackathon Discord for help | P2 | Open |

## Session save — 2026-10-27 (update)

Done, not committed: Linux-only build tag on `cmd/meta-cost/main_test.go` (#5), `TestFileIdentity` (#5), `open` first on darwin (#1), darwin/amd64 removed from GoReleaser plus a clear `install.sh` error (#4), `docs/INSTALL-MACOS.md` and README checks section (#3, #4). `go vet ./...`, `gofmt -l .` and `go test ./...` pass on macOS arm64.

Not done: `ProcessInspector` and explicit `unknown` billing result (#3), `tui --once` run in a real terminal (#2), install from a published release (#4), announcement and help requests (#6, #7, #8).

Last thought: The small, safe fixes are in the working tree; the billing boundary needs a design decision.

Immediate next micro-step: Run `meta-cost tui --once --period all` in Terminal.app to verify MCC-REQ-002-S01, then commit in small commits.
