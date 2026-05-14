# kitchen

A developer toolbox with a modern Bubble Tea TUI. The first feature, `kitchen
log`, streams live Kubernetes logs from every pod of a deployment into a
single colour-coded viewer. JIRA and GitHub integrations will be added later.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/jiripisa/kitchen/main/scripts/install.sh | sh
```

The script detects your OS/arch (linux & darwin, amd64 & arm64), verifies the
release checksum, and installs to `~/.local/bin` (no sudo needed). This also
means `kitchen upgrade` always works without sudo. Make sure `~/.local/bin`
is on your `$PATH` — the installer prints a hint if it isn't.

Want the binary somewhere else (e.g. `/usr/local/bin`)?

```sh
curl -fsSL .../install.sh | KITCHEN_INSTALL_DIR=/usr/local/bin sh
```

## Usage

```sh
kitchen log       # pick namespace → pick deployment → stream logs
kitchen webtop    # list webtop deployments (mafin-coreo-app-*) across all namespaces
kitchen upgrade   # check for a newer release and replace the running binary
kitchen version   # print version, commit, build date
```

### `kitchen log`

A three-screen TUI:

1. **Namespace picker** — filterable list of all namespaces in the current
   context. Type to filter, `↑`/`↓` to move, `Enter` to pick, `q`/`Esc` to quit.
2. **Deployment picker** — deployments in the selected namespace, with ready
   counts. `Esc` goes back.
3. **Log viewer** — live logs from every pod, each line prefixed with a
   per-pod colour. `f` toggles follow on/off, `g`/`G` jumps to top/bottom,
   `Esc` goes back, `q` quits.

The last 5 selected namespaces and deployments are pinned to the top of the
picker on subsequent runs (marked with `★`, with a dim rule separating them
from the rest). Recents are stored per kubeconfig context in
`~/.local/state/kitchen/recents.json`.

### Prerequisites

`kitchen log` reads `KUBECONFIG`, falling back to `~/.kube/config`. The
current context is what gets used — switch with `kubectl config use-context`
beforehand.

## Development

```sh
git clone https://github.com/jiripisa/kitchen
cd kitchen
go build ./cmd/kitchen
./kitchen log
```

Run the test suite:

```sh
go test ./...
```

## Screenshot

_TODO — add a recording of the log viewer in action._

## License

[MIT](./LICENSE)
