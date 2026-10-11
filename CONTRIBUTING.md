# Contributing

Thanks for helping. Before you start, read `ARCHITECTURE.md`: the layers and
rules there keep the project clean as it grows.

## Develop

```
go run ./cmd/gitswitch help      # run from source
go test ./...                    # all tests (unit, integration, architecture)
go vet ./... && gofmt -l .       # must be clean
./build.sh                       # all six platform builds into dist/
```

## Try changes safely

Test with real accounts without touching your own git setup. Pick a way:

| | Native | Docker |
|---|---|---|
| Needs | Go | Docker (Go optional) |
| Runs on | your own OS, in a throwaway home folder | a Linux desktop in your browser (Webtop) |
| CLI, setup, terminal app | yes | yes |
| Browser app, popups, clipboard, app menu | yes, on your own desktop | yes, on Linux |
| Best for | everyday work | Linux behavior, or no Go installed |

### Native: throwaway home folder (macOS, Linux, WSL)

Build first: Go keeps its caches under `$HOME`, so building after the switch
downloads everything again. The switch only affects that one terminal.

```
mkdir -p /tmp/gs-sandbox/bin
go build -o /tmp/gs-sandbox/bin/gitswitch ./cmd/gitswitch
export HOME=/tmp/gs-sandbox; unset XDG_CONFIG_HOME
export PATH=/tmp/gs-sandbox/bin:$PATH
gitswitch install && gitswitch setup
```

`~` now means the sandbox, so pass existing keys by full path
(`--key /Users/you/.ssh/id_ed25519`). After a code change, run the `go build`
line again. Clean up by closing the terminal and `rm -rf /tmp/gs-sandbox`.

### Docker: Linux desktop (Webtop)

`sandbox/compose.yaml` runs an Ubuntu desktop at http://localhost:3000 (only
reachable from your machine). The desktop user's home is `sandbox/config/`,
which git ignores because it holds SSH private keys. The repo's `dist/` is
mounted read-only at `/opt/gitswitch`: you build on the host and run the
result on the desktop. Run these from the repo root.

1. Start the desktop:

   ```
   docker compose -f sandbox/compose.yaml up -d
   ```

2. Build for Linux. Use `GOARCH=arm64` on Apple Silicon or ARM, `amd64` on
   Intel or AMD. With Go installed:

   ```
   CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o dist/gitswitch-linux ./cmd/gitswitch
   ```

   Without Go, build in a container:

   ```
   docker run --rm -v "$PWD":/src -w /src -e CGO_ENABLED=0 -e GOOS=linux -e GOARCH=arm64 \
     golang:1.22-bookworm go build -buildvcs=false -o dist/gitswitch-linux ./cmd/gitswitch
   ```

3. Add the tools gitswitch uses (ssh, and zenity for popups):

   ```
   docker exec gitswitch-desktop bash -c 'apt-get update && apt-get install -y openssh-client zenity'
   ```

4. Install gitswitch for the desktop user:

   ```
   docker exec -u abc -e HOME=/config gitswitch-desktop /opt/gitswitch/gitswitch-linux install
   ```

5. Open http://localhost:3000, open a terminal from the app menu and run
   `gitswitch setup`, then `gitswitch` (terminal app) or `gitswitch ui`
   (browser app, opens in Chromium on the desktop).

After a code change, repeat steps 2 and 4. Stop the desktop with
`docker compose -f sandbox/compose.yaml down`. That removes the container, so
repeat step 3 after the next `up`; accounts and keys stay in
`sandbox/config/`. Start over by deleting `sandbox/config/`.

### What a sandbox does not isolate

- **GitHub.** SSH keys you add to your GitHub accounts are real. Remove them
  when you are done.
- **`~/.ssh/known_hosts` on macOS and Linux** in the native sandbox:
  ssh finds it from your user account, not `$HOME`, so a first connection may
  add a host entry there. This is harmless.

## Pull requests

- Put new behavior in the right layer; front ends only call `internal/app`.
- Add tests: a unit test next to the code, and an integration test in
  `test/integration` for anything git or the terminal can observe.
- New packages need a layer in `test/architecture/layers_test.go` and in
  `ARCHITECTURE.md`.
- Keep messages plain and specific: say what happened and how to fix it.
- CI must pass on Linux, macOS and Windows.

## Publishing a release (maintainers)

1. Push a tag: `git tag v1.0.0 && git push origin v1.0.0`.
2. The `release` workflow runs the tests, builds all six binaries plus
   `SHA256SUMS`, and attaches them to the GitHub release.
3. The one-line installers always fetch the latest release.
