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

Try changes safely with a throwaway home folder (macOS, Linux, WSL):

```
export HOME=/tmp/gs-sandbox; mkdir -p "$HOME"; unset XDG_CONFIG_HOME
go run ./cmd/gitswitch install
```

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
