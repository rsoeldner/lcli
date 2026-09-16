---
name: lcli
description: Read Linear issues (including uploaded screenshots and videos) and post or fix comments with attached images/videos via the lcli CLI, across multiple Linear accounts. Use when the user mentions a Linear issue ID like ENG-123, a linear.app issue URL, or asks to report findings, progress or a fix on a Linear ticket.
---

# lcli — Linear issues and comments

`lcli` talks to the user's Linear workspaces. The account is chosen
automatically from the issue ID's team key; never guess or pass `--account`
unless lcli reports an unknown team key and the user tells you which account.

## Read an issue first

```sh
lcli issue ENG-123
```

- Output: metadata, description, attachments, all comments (oldest first,
  each under `### Comment <COMMENT_ID>`), and a **Media** list of downloaded
  files with local paths.
- Open the downloaded images with the Read tool: screenshots often carry the
  actual bug report.
- For videos, rerun with `--frames 4` and Read the extracted frame images.
- `--json` gives the same data structured; `lcli comment list ENG-123` shows
  only comments.
- linear.app issue URLs work wherever an ID does.

## Post a comment

1. Write the comment as markdown to a file (avoids shell quoting problems):
   use the Write tool, e.g. `/tmp/lcli-ENG-123.md`.
2. Preview when the content matters:
   `lcli comment add ENG-123 --body-file /tmp/lcli-ENG-123.md --attach shot.png --dry-run`
3. Post:
   `lcli comment add ENG-123 --body-file /tmp/lcli-ENG-123.md --attach before.png --attach repro.mp4`
4. Keep the printed comment ID; it is needed to fix the comment later.

- `--attach` uploads a file and appends it (images inline, videos as links).
  Repeat it for several files.
- `--reply-to <COMMENT_ID>` answers a specific comment thread.
- Only if an image must sit in the middle of the text: `lcli upload ENG-123 shot.png`
  prints `![shot.png](https://uploads.linear.app/...)`; paste that line into
  the markdown file, then `comment add` it. Uploads alone are not visible on
  the issue.

## Fix a comment (never post a duplicate)

If a posted comment is wrong or incomplete, edit it:

```sh
# Replace the whole body: pass the COMPLETE corrected text
lcli comment edit ENG-123 <COMMENT_ID> --body-file /tmp/lcli-ENG-123-fixed.md

# Add to the end, e.g. a follow-up screenshot
lcli comment edit ENG-123 <COMMENT_ID> --append -m "Update: verified on staging." --attach after.png
```

Find the comment ID in the `comment add` output or with `lcli comment list ENG-123`.
Editing someone else's comment is expected to fail (Linear normally only lets the author edit); report that to the user. There is no delete.

## When a command was interrupted or its outcome is unclear

Run `lcli comment list ENG-123` before retrying a `comment add`. If the
comment is already there, edit it instead of posting again.

## Errors (exit codes)

- **1: input error.** Examples: an unknown team key (the message lists the
  known keys), a missing file, or a comment that belongs to another issue.
  Fix the arguments; don't retry unchanged.
- **2: config, key or auth problem.** Tell the user and suggest
  `lcli accounts --check`. Don't try to work around it.
- **3: Linear API or network error.** Report Linear's message. Retry once at
  most, and only for reads, or for writes after checking with `comment list`.
