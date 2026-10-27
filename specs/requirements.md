# macOS arm64 Port Requirements

These draft requirements describe the approved vision and the macOS arm64 porting brief. They are written before design or implementation. The scenarios are intentionally concrete so each one can later become a falsifiable test.

## User story: Mac user runs Meta Cost Center

As a hackathon participant using an Apple Silicon Mac, I want to run Meta Cost Center locally, so that I can experiment with Meta API usage without cost surprises.

### Feature: macOS arm64 build and supported startup

MCC-REQ-001

@MCC-REQ-001-S01
Scenario: Build the macOS arm64 binary
  Given the repository and its Go dependencies are available
  When the contributor runs `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o meta-cost-darwin-arm64 ./cmd/meta-cost`
  Then the command exits with status 0 and creates an executable `meta-cost-darwin-arm64`

@MCC-REQ-001-S02
Scenario: Start the macOS arm64 CLI
  Given `meta-cost-darwin-arm64` was built successfully
  When the contributor runs `./meta-cost-darwin-arm64 --help`
  Then the command exits with status 0 and does not fail because of an unhandled Linux-only dependency

### Feature: Portable terminal dashboard

MCC-REQ-002

@MCC-REQ-002-S01
Scenario: Open and exit the terminal dashboard on macOS
  Given a macOS arm64 terminal and a valid local usage source
  When the contributor runs `meta-cost tui --once --period all`
  Then the command renders the terminal report and exits without a Linux-specific terminal ioctl error

@MCC-REQ-002-S02
Scenario: Preserve the Linux terminal build
  Given the repository is built for Linux amd64
  When the contributor runs `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o meta-cost-linux-amd64 ./cmd/meta-cost`
  Then the command exits with status 0 and creates an executable `meta-cost-linux-amd64`

## User story: Contributor understands platform limits

As a contributor, I want macOS behavior and limitations to be explicit, so that I know what to test and where to ask for help.

### Feature: Explicit Linux-only limitations

MCC-REQ-003

@MCC-REQ-003-S01
Scenario: Report unavailable process-based billing detection
  Given the billing-mode hook runs on macOS where Linux `/proc` process data is unavailable
  When the hook attempts to detect the billing mode
  Then it records or reports `unknown` or `unsupported` and includes an explanation that the required process data is unavailable

@MCC-REQ-003-S02
Scenario: Document the process-data limitation
  Given a contributor reads the macOS usage and billing-mode documentation
  When they search for the platform limitation
  Then the documentation names the `/proc` dependency and the affected billing-mode behavior

### Feature: macOS arm64 distribution

MCC-REQ-004

@MCC-REQ-004-S01
Scenario: Select the macOS arm64 release artifact
  Given `uname -s` returns `Darwin` and `uname -m` returns `arm64`
  When the supported installer runs
  Then it requests `meta-cost_darwin_arm64.tar.gz` and installs the `meta-cost` executable

@MCC-REQ-004-S02
Scenario: Describe macOS arm64 support consistently
  Given a contributor reads the README, installer comments, and release configuration
  When they compare the platform information
  Then all three identify macOS arm64 support and the README states the known Linux-only limitation

### Feature: Repeatable port verification

MCC-REQ-005

@MCC-REQ-005-S01
Scenario: Run the native verification commands
  Given the contributor is on the development Mac
  When they run `go test ./...` and `go build -o meta-cost ./cmd/meta-cost`
  Then both commands exit with status 0

@MCC-REQ-005-S02
Scenario: Run the macOS arm64 cross-build
  Given the repository and its Go dependencies are available
  When the contributor runs `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o meta-cost-darwin-arm64 ./cmd/meta-cost`
  Then the command exits with status 0 and the output is a macOS arm64 executable

## Dry-test result

All ten scenarios have a concrete Given/When/Then shape and a falsifiable result. S01 and S02 of MCC-REQ-003 require a platform fixture or a controlled macOS run before implementation acceptance; this is a test setup dependency, not an ambiguity in the expected result.
