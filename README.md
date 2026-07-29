# ai-session

Small local launcher for AI CLI accounts that need isolated authentication state.

The first version keeps account state separate instead of copying or rewriting
tokens. Each launched process receives only its profile-specific state directory:

- Codex: `CODEX_HOME`
- Claude Code: `CLAUDE_CONFIG_DIR`
- OpenCode: `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, and `XDG_STATE_HOME`

This prevents two Codex accounts from racing over one `auth.json` and lets each
official CLI own its own refresh-token lifecycle. `ai-session` never reads,
prints, or stores token contents.

## Install

```sh
go install ./...
```

## Create profiles

```sh
ai profile add codex-personal codex
ai profile add codex-work codex
ai profile add claude-personal claude
ai profile add opencode-go opencode
ai profile add deepseek opencode
```

Log in through the isolated profile:

```sh
ai login codex-personal
ai login codex-work
ai login claude-personal
ai login opencode-go
```

`ai run` uses the credentials already stored in that profile. A new profile has
no credentials until you run `ai login <profile>`; the login flow is performed
by the official CLI and may open a browser or ask for a device code. The
launcher intentionally does not copy the account from your normal Codex or
Claude configuration.

Run a profile:

```sh
ai run codex-personal
ai run codex-work exec -- "review this repository"
ai run claude-personal
ai run opencode-go
```

The `run` command is optional when the first argument is a profile name. These
are equivalent, and any following arguments are passed to the profile's
configured command:

```sh
ai claude-personal
ai run claude-personal
ai codex-work exec -- "review this repository"
```

If the bare argument is not an existing profile, `ai` returns an error instead
of guessing a provider or creating a profile.

## Move a profile to another computer

Install [`age`](https://age-encryption.org/) on both computers, then export the
profile as an encrypted bundle:

```sh
ai profile export codex-work codex-work.ai-profile.age
ai profile import codex-work.ai-profile.age
```

The export includes only that profile's metadata and isolated CLI state. `age`
prompts for the encryption passphrase; the launcher never prints or stores it.
Imports refuse to replace an existing profile. Treat the bundle like a password
backup and delete it after transferring it if it is no longer needed. API keys
provided through environment variables, such as `DEEPSEEK_API_KEY`, are not
included and must be transferred separately through a secret manager.

Do not actively use the same imported profile on multiple computers: provider
refresh tokens may rotate when used.

Running `ai` without arguments opens an interactive Bubble Tea TUI. Providers
are colour-coded, a profile that currently holds its lock is marked
`▶ running`, and the `AUTH` column shows whether the profile has logged in:

- `● yes` — the provider's credential file exists in the isolated state
  directory, so `ai run` can use it.
- `● key` — a `deepseek` profile with `DEEPSEEK_API_KEY` exported. DeepSeek runs
  through OpenCode with an API key from the environment rather than a stored
  credential file, so there is nothing to log in to.
- `○ no` — no credentials yet; press `l` to log in.
- `· ?` — the provider has no known credential location, so the launcher does
  not guess.

The `MODEL` column shows the model the profile will start with, read from that
CLI's own settings inside the isolated directory. `—` means the provider has no
discoverable answer yet:

| Provider | Source |
| -------- | ------ |
| Codex | `codex/config.toml`, top-level `model` |
| Claude Code | `claude/settings.json`, `model` |
| OpenCode | `state/opencode/model.json`, most recent entry; falls back to `model` in `config/opencode/opencode.json[c]` |

OpenCode's state file is preferred because it holds the model last chosen in the
TUI, which is what OpenCode restores on the next start; the config default only
applies before anything has been picked.

The check only tests whether the file exists, and reads the API key variable
only to see whether it is empty; `ai-session` still never opens or prints
credentials. A profile whose token has expired therefore keeps showing `● yes`
until the official CLI asks you to log in again.

Use the arrow keys or `j`/`k` to select a profile:

- Enter runs the selected profile.
- `l` logs in to the selected profile.
- `a` adds a profile.
- `e` edits its name, provider, or command.
- `x` deletes it and its isolated state after confirmation.
- `q` or Escape quits.

In the profile editor, Enter or Tab advances through the fields; on the final
field it saves. Escape cancels and Ctrl-U clears the current field.

DeepSeek should be configured in the OpenCode profile using OpenCode's normal
provider setup or an environment variable. The launcher preserves ordinary
environment variables, including `DEEPSEEK_API_KEY`, but removes shared
`CODEX_HOME`, `CLAUDE_CONFIG_DIR`, and XDG paths before adding the selected
profile's paths.

## OpenUsage integration

OpenUsage remains the usage/limits layer. Install its official integrations
inside each isolated profile:

```sh
ai integrate openusage claude-personal
ai integrate openusage codex-personal
ai integrate openusage codex-work
ai integrate openusage opencode-go
ai integrate openusage deepseek
```

The launcher deliberately does not modify OpenUsage's database or copy tokens.
It runs OpenUsage's supported installer with the selected profile environment,
so Codex and Claude hooks are installed beside that profile's own state. The
profile lock also refuses a second process for the same account, preventing
concurrent refresh-token rotation. Different profiles can still run at once.
