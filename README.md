# azd — a k9s-style terminal UI for Azure

`azd` lets you browse and operate **Azure Container Apps** from your terminal, with the
same look, keys and workflow as [k9s](https://k9scli.io): live tables, drill-down,
`:` commands, `/` filters, describe/YAML views, streaming logs, interactive shells, edit
in `$EDITOR`, scale, restart, delete. Navigation is vim-style throughout.

```
Subscription: my-subscription                  <0> all            <x> Shell                     az-deck
Res Group:    all                              <enter> Revisions  <b> Browse
Environment:  all                              <d> Describe       <s> Scale
┌───────────────────────────── ContainerApps(all)[3] ─────────────────────────────┐
│ NAME↑         RG        ENV         STATUS   REVISION        SCALE  IMAGE   AGE │
│ api           rg-prod   cae-prod    Running  api--0000026    1-10   …       45d │
└─────────────────────────────────────────────────────────────────────────────────┘
 <containerapps>
```

## Install

Works on Linux, macOS and Windows via WSL. It installs one binary into `~/.local/bin`
and needs no sudo:

```sh
curl -fsSL https://raw.githubusercontent.com/macciocu/az-deck/main/install.sh | sh
```

Options (flags after `sh -s --`, or environment variables):

| Flag | Env | Default | |
|---|---|---|---|
| `--version v1.2.3` | `AZD_VERSION` | latest | release to install |
| `--dir PATH` | `AZD_INSTALL_DIR` | `~/.local/bin` | install location |
| `--name NAME` | `AZD_BIN_NAME` | `azd` | binary name (see note below) |
| `--from-source` | `AZD_FROM_SOURCE=1` | | build with Docker instead of downloading |
| `--uninstall` | | | remove the binary |

If no release has been published yet, the installer builds from source inside a
`golang` Docker container. Nothing else gets installed on your system.

> **Name clash:** Microsoft's *Azure Developer CLI* also uses the command `azd`. If you
> have it installed, the installer warns you, and the first `azd` on your `PATH` is
> the one that runs. Install under a different name with
> `curl … | AZD_BIN_NAME=azdeck sh`.

### Authentication

`azd` uses the standard Azure credential chain: environment variables
(`AZURE_CLIENT_ID`/`AZURE_TENANT_ID`/`AZURE_CLIENT_SECRET`), workload or managed
identity, the Azure CLI (`az login`), and the Azure Developer CLI login. Usually you
just run `az login` first.

### Run in Docker

```sh
make run-docker   # builds the image and mounts your ~/.azure login
```

## Usage

```
azd [-s subscription] [-g resource-group] [-c apps|jobs|env|rg|sub] [-readonly] [-refresh 5s] [-shell sh]
```

When neither `-s` nor `$AZURE_SUBSCRIPTION_ID` is set, azd starts in the subscription you
used last, or else your `az` default. It remembers the subscription and resource group you
used last in `~/.config/azd/state.json`.

### Views and commands

| Command (aliases) | View | k9s equivalent |
|---|---|---|
| `:apps` (`ca`, `containerapps`, `dp`) `[rg]` | Container apps | deployments |
| `:rev <app>` (`revisions`, `rs`) | Revisions of an app | replicasets |
| `:rep <app>` (`replicas`, `po`, `pods`) | Replicas of active revisions | pods |
| `:env` (`cae`, `managedenvironments`) `[rg]` | Managed environments | nodes |
| `:jobs` (`job`, `cj`) `[rg]` | Container app jobs → executions | cronjobs → jobs |
| `:rg [name]` (`ns`) | Resource groups / set scope | namespaces |
| `:sub [name]` (`ctx`, `context`) | Subscriptions / switch | contexts |
| `:help`, `:q` | Help, quit | |

Drill down with `enter`: environment → apps → revisions → replicas → containers → logs.
Jobs drill down to their executions.

### Keys

**Global:** `:` command · `/` filter (regex, prefix `!` to invert) · `esc` back or clear
filter · `?` help · `ctrl-r` refresh · `ctrl-c`/`:q` quit

**Navigation:** `j`/`k` down/up · `h`/`l` left/right · `g`/`G` top/bottom ·
`ctrl-f`/`ctrl-b` page down/up · `enter` drill down

**Tables:** `d` describe · `y` YAML · `c` copy name · `shift-n` / `shift-a` /
`shift-s` sort by name / age / status (press again to reverse) · `0` all resource groups,
`1`–`9` recently used resource groups

| View | Keys |
|---|---|
| Apps | `l` logs (all replicas) · `v` system events · `x` shell · `b` open in browser · `s` scale · `r` restart · `ctrl-t` start/stop · `e` edit · `ctrl-d` delete |
| Revisions | `l` logs · `r` restart · `a` activate/deactivate |
| Replicas | `l` logs · `s` shell · `ctrl-k` restart revision |
| Containers | `enter`/`l` logs · `s` shell |
| Environments | `l` system logs · `ctrl-d` delete |
| Jobs | `t` trigger · `e` edit · `ctrl-d` delete |
| Executions | `ctrl-k` stop |
| Logs | `s` autoscroll · `w` wrap · `p` source prefix · `c` clear · `0`–`3` tail none/50/100/300 · `/` filter · `ctrl-s` save |
| Describe/YAML | `/` search · `n`/`N` next/prev match · `w` wrap · `c` copy · `ctrl-s` save |

Every destructive action asks for confirmation first. Start with `-readonly` to turn off
all mutating actions.

**Edit** (`e`) opens the editable parts of the resource (template, ingress, revision
mode, Dapr, tags) as YAML in `$AZD_EDITOR`, `$KUBE_EDITOR`, `$VISUAL` or `$EDITOR`.
Saving applies the change as a merge PATCH. Secrets and registry credentials are not
shown and stay untouched. If the YAML is invalid, the editor reopens with the error on
top, the same way `kubectl edit` does.

**Shell** (`s`/`x`) uses the same websocket exec endpoint as `az containerapp exec`. The
UI suspends while the session runs and comes back when you type `exit`.

## Development

All tooling runs in Docker, so you don't need Go installed:

```sh
make build      # bin/azd for this machine
make test       # unit tests
make vet        # go vet (linux + darwin)
make cross      # dist/azd_{linux,darwin}_{amd64,arm64}
make snapshot   # release archives via GoReleaser
make install    # copy bin/azd to ~/.local/bin
```

To release, push a tag (`git tag v0.1.0 && git push --tags`). GitHub Actions then runs
GoReleaser and publishes the archives and `checksums.txt` that `install.sh` downloads.

Layout: `cmd/azd` (entry point) · `internal/azure` (SDK wrapper, log streaming, exec
protocol) · `internal/ui` (tview views: table, logs, detail, help, prompt, header) ·
`internal/config` (persisted state).

Built with [tview](https://github.com/rivo/tview)/[tcell](https://github.com/gdamore/tcell),
which k9s also uses, and the
[Azure SDK for Go](https://github.com/Azure/azure-sdk-for-go).
