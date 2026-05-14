# Decisions

A running log of design choices we made during the first session, so future
sessions can revisit them with full context.

## 2026-05-14 — v0.1.0 foundation

### Module path

`github.com/jiripisa/kitchen`.

### Self-update implementation

We rolled a small custom updater (`internal/updater`) instead of pulling in
`creativeprojects/go-selfupdate`. Reasons:

- Only two release surfaces to support (`linux`, `darwin` × `amd64`, `arm64`),
  so the matching logic is trivial.
- Fewer transitive dependencies and a tighter binary.
- We can verify checksums against GoReleaser's `checksums.txt` directly.
- We have full control over the atomic-replace flow on macOS/Linux.

If we ever need to support Windows, signed releases, delta updates, or in-place
rollbacks, revisit and consider the library.

### Bubble Tea state model

Single root `tea.Model` that holds the current screen (namespace picker,
deployment picker, log viewer) plus shared state (cluster context, selected
namespace/deployment). Each screen is its own sub-model implementing
`Init/Update/View`. The root delegates routing.

### K8s wrapper

Thin wrapper around `client-go`. Returns plain Go types (strings, structs from
this package) — no `*v1.Namespace` leaks into the TUI layer. Streaming logs
use `corev1.PodLogOptions{Follow: true}` and return a `<-chan LogLine` plus a
cancel function.

### Log streaming

Per-pod goroutine reads the stream and fans into a single buffered channel.
The TUI reads from the channel inside a `tea.Cmd` and emits a `logLineMsg`
per line. On `f` toggle we stop sending new lines to the viewport without
killing the streams.

### Versioning

`version`, `commit`, `date` are package-level `var`s in `internal/version`
overridden via `-ldflags`. Default values (`dev`, `none`, `unknown`) make the
`upgrade` command short-circuit on dev builds.
