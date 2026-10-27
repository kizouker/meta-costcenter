# Handover: Meta Cost Center macOS arm64 port

Read `state.json` first. Take **one task at a time** from the list below. Respond to the human in Swedish; code and comments in English. Max 10 lines per response.

## State (2026-10-27)

- Branch `macos-arm64-port`, seven local commits, **not pushed**. Working tree clean.
- Verified on macOS arm64: `go vet ./...`, `gofmt -l .`, `go test ./...`, and cross-builds for linux/arm64 and darwin/arm64 (`CGO_ENABLED=0`).
- Not verified: anything in a real terminal, a published release, Linux CI, Keyguard on macOS.

## Done

- Terminal ioctls split into `tui_terminal_{linux,darwin}.go` (`x/sys/unix`).
- `cmd/meta-cost/main_test.go` is `//go:build linux` (Linux PTY constants).
- `TestFileIdentity` added; the `Stat_t.Dev` concern is unproven, not a defect.
- Browser opener: `open` only on darwin.
- GoReleaser ignores darwin/amd64; `install.sh` fails clearly on Intel Macs.
- `docs/INSTALL-MACOS.md`, README "Checks before a change", requirements, SAD and tickets.

## Tasks (one at a time, in order)

1. **Verify TUI (#2, mechanical).** In Terminal.app run `go build -o meta-cost ./cmd/meta-cost && ./meta-cost tui --once --period all`. Report the output and whether it errors on ioctl. Do not change code unless it fails.
2. **Push (#6).** Ask the human first, then `git push origin macos-arm64-port` to `kizouker/meta-costcenter`.
3. **CI matrix (#5).** Add a GitHub Actions workflow on `ubuntu-latest` and `macos-latest` running `go vet ./...`, `gofmt -l .` (fail on output) and `go test ./...`.
4. **Billing boundary (#3, needs design decision from the human).** Add a `ProcessInspector` interface with the Linux `/proc` implementation and a darwin stub returning `unknown` with an explanation. Do not emulate `/proc`. Tests use a fake inspector.
5. **Announcements (#6, #7, #8).** Draft only; the human sends them.

## Do not

- Do not install a `/proc` emulator or add cgo.
- Do not commit or push without the human's yes. Commits end with the attribution line given by the session.
- Do not change the design (`design/SAD.md`) without a decision; Structurizr (`design/workspace.dsl`) is the single diagram source.

## Open questions

- Which billing-mode behavior is acceptable on macOS beyond `unknown`?
- Is a portable PTY test needed upstream, or is the Linux-only tag enough?
- ExamPro/Andrew guidance and the Discord channel for the announcement.
