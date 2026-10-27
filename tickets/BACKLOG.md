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

## Session save — 2026-10-27 (evening)

Done: The TUI snapshot was built and run in Terminal.app (#2); it exited 0 without an ioctl error. No Muse session logs were found, so usage coverage is unavailable, not confirmed zero. Earlier checks passed on macOS arm64: `go vet ./...`, `gofmt -l .`, `go test ./...`, and Linux/arm64 and Darwin/arm64 cross-builds. The handover records seven local commits on `macos-arm64-port` as not pushed.

Not done: Push the branch (#6); add the CI matrix (#5); decide the billing boundary (#3); verify a published release (#4); draft announcements and help requests (#6, #7, #8). Issues remain open.

Last thought: The real-terminal snapshot succeeds, but no session logs are available to establish usage coverage.

Immediate next micro-step: Verify the approved push completed, then take the CI matrix task (#5), subject to the project process gate. Continue with only one handover task at a time.
