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

Running `ai` without arguments opens an interactive Bubble Tea TUI. Use the
arrow keys or `j`/`k` to select a profile:

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
