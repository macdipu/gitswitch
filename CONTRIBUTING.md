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

Two sandboxes. Use the throwaway home folder for everyday work and the Linux
desktop to check Linux behavior before a release.

| Sandbox | CLI, setup, terminal app | Browser app, popups, clipboard, app menu |
|---|---|---|
| Throwaway home folder | yes | your own desktop |
| Docker Linux desktop (Webtop) | yes | yes |

### Throwaway home folder (macOS, Linux, WSL)

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
(`--key /Users/you/.ssh/id_ed25519`). Clean up with `rm -rf /tmp/gs-sandbox`.

### Docker Linux desktop (Webtop)

`sandbox/compose.yaml` runs an Ubuntu desktop at http://localhost:3000 (only
reachable from your machine). The desktop user's home is `sandbox/config/`,
which git ignores because it holds SSH private keys. The repo's `dist/` is
mounted read-only at `/opt/gitswitch`, so you build on the host and run the
result on the desktop.

```
docker compose -f sandbox/compose.yaml up -d
GOOS=linux GOARCH=arm64 go build -o dist/gitswitch-linux ./cmd/gitswitch   # amd64 on Intel
```

In a terminal on the desktop, once:

```
sudo apt-get update && sudo apt-get install -y git openssh-client zenity xclip
```

After each build:

```
/opt/gitswitch/gitswitch-linux install
```

Stop it with `docker compose -f sandbox/compose.yaml down`; start over by
deleting `sandbox/config/`.

### What a sandbox does not isolate

- **GitHub.** SSH keys you add to your GitHub accounts are real. Remove them
  when you are done.
- **`~/.ssh/known_hosts` on macOS and Linux** in the throwaway-home sandbox:
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
