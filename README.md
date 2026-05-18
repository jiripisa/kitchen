# kitchen

A small developer toolbox for working with the mafin Kubernetes cluster:
stream logs, browse running webtops, and roll out (or tear down) a webtop
against any coreo backend — all from a fullscreen TUI.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/jiripisa/kitchen/main/scripts/install.sh | sh
```

Detects your OS / arch, verifies the release checksum, and installs to
`~/.local/bin` (no sudo). Make sure `~/.local/bin` is on your `$PATH` — the
installer prints a hint if it isn't. Run `kitchen upgrade` later to update.

Want the binary somewhere else?

```sh
curl -fsSL .../install.sh | KITCHEN_INSTALL_DIR=/usr/local/bin sh
```

## Commands

```sh
kitchen log                 # pick namespace → deployment → stream live logs
kitchen webtop              # menu: list / deploy / undeploy
kitchen webtop list         # browse running webtops + their coreo backends
kitchen webtop deploy       # wizard: pick build → pick coreo → name → deploy
kitchen webtop undeploy     # remove a webtop you deployed with kitchen
kitchen coreo list          # browse running coreos + how many webtops point at each
kitchen upgrade             # self-update to the latest release
kitchen version             # show version
```

### `kitchen log`

Three screens: namespace → deployment → live logs from every pod with
per-pod colours. `f` toggles follow, `g`/`G` jumps to top/bottom, `Esc`
goes back, `q` quits.

The last few namespaces & deployments are pinned to the top of the picker
on subsequent runs (marked with `★`).

### `kitchen webtop list`

Live list of every webtop deployment in the cluster. Each row shows the
webtop URL, the coreo it's pointing at, the PR + branch behind it, and how
long ago the pod last logged something. URLs, PR numbers and branch names
are clickable. Press `Enter` to view the deployment's full manifest.

### `kitchen webtop deploy`

Three steps:

1. **Pick a build** — `main` plus every open PR on the webtop repo. Your
   own PRs float to the top.
2. **Pick a coreo backend** — the running coreos in the cluster, with the
   canonical staging first and the most recently active ones next.
3. **Name the deployment** — kitchen suggests one and validates as you
   type. The URL preview updates live.

After confirming, kitchen rolls out the new webtop and prints the URL.
Give the pod ~30 s to come up before opening it.

### `kitchen webtop undeploy`

Lists only the webtops you deployed with kitchen — upstream PR review-apps
are never shown. Pick one, confirm, and kitchen removes the deployment.

### `kitchen coreo list`

Mirror of `webtop list` but for the coreo backend. Each row shows the
coreo URL, the PR + branch behind it, time since the last log line, and
how many webtops are currently pointing at it via `MAFIN_URL`. Press
`Enter` to view the deployment's manifest.

## Prerequisites

- `KUBECONFIG` pointing at the mafin cluster (or `~/.kube/config`). Switch
  contexts with `kubectl config use-context` beforehand.
- `gh` CLI authenticated (`gh auth login`) for PR / branch listings and
  for the deploy wizard.

## License

[MIT](./LICENSE)
