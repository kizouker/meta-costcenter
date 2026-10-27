# Installing Meta Cost Center on macOS

Status: macOS support is new. Items marked **not verified** have not been tested on a
release build and may change.

## Requirements

- A Mac with Apple Silicon (arm64). Intel Macs have no release archive.
- A web browser.
- Go, only if you build from source.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/omenking/meta-costcenter/main/install.sh | sh
```

`install.sh` requests `meta-cost_darwin_arm64.tar.gz`, checks it against the release
checksums, and installs `meta-cost` into `~/.local/bin`. Set `META_COST_INSTALL_DIR` to use
another directory. Make sure that directory is on your `PATH`.

To build from source instead:

```bash
go build -o meta-cost ./cmd/meta-cost
```

## First run

```bash
meta-cost show
```

This starts the dashboard on `http://127.0.0.1:8765/` and opens it with the macOS `open`
command. Muse session logs are read from `~/.local/share/muse/sessions` unless
`XDG_DATA_HOME` is set.

## Known limitations on macOS

- **Billing-mode detection is unavailable.** The billing hook reads Linux `/proc` process
  data (`/proc/<pid>/stat`, `environ`, `cmdline`), which macOS does not have. Treat the
  billing mode as `unknown` on macOS. **Not verified:** the hook's exact output on macOS,
  since the code does not yet report an explicit "unsupported" result.
- **Terminal dashboard** (`meta-cost tui`) uses the macOS terminal ioctls
  (`TIOCGETA`/`TIOCSETA`). **Not verified** in Terminal.app and iTerm2 beyond a build.
- **Meta Keyguard integration** has not been tested on macOS.
- **Gatekeeper:** a binary downloaded by a browser may be blocked as unsigned. **Not
  verified** for the `curl` installer.

## Checking your build

```bash
go vet ./... && gofmt -l . && go test ./...
```

See "Checks before a change" in the README for what each command means.
