# lcli

A small CLI for reading and commenting on Linear issues across several Linear
accounts, built to be easy for LLM agents to use. The account is picked from
the issue's team key (`ENG-123` → team `ENG`).

## Install

```sh
go install github.com/rsoeldner/lcli@latest
```

## Configure

`~/.config/lcli/config.toml` (one personal API key per workspace, e.g. in the macOS Keychain):

```toml
[accounts.work]
teams = ["ENG"]
key_cmd = "security find-generic-password -s lcli-work -w"   # or: key_env = "LINEAR_WORK_KEY"
```

Check with `lcli accounts --check`.

## Usage

```sh
lcli issue ENG-123                                   # issue + comments; downloads uploaded images/videos
lcli comment add ENG-123 --body-file note.md --attach shot.png
lcli comment edit ENG-123 <COMMENT_ID> --append -m "Update"
lcli upload ENG-123 shot.png                          # markdown snippet only
```

Write commands support `--dry-run`. See `lcli --help` for details, and
`skills/lcli/SKILL.md` for a Claude Code skill.
