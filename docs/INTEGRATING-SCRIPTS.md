# Integrating a script with Meta Cost Center

This guide is for anyone, person or coding agent, adding Meta Cost Center reporting to a
script that calls the Meta Model API directly. It describes the contract of the
`meta-cost record` command, what a script should record after each API call, and how to
check that the integration works. `scripts/gen-image` in the
`meta-global-ai-hackathon-jolly-challenge` repository is a complete working example.

## Why a script has to report its own usage

Cost Center builds its reports from the session logs that Muse writes on this machine. A
script that calls `https://api.meta.ai/v1` itself, for example through the OpenAI SDK,
leaves no trace in those logs, so its usage appears nowhere in Cost Center, does not count
toward spending alerts, and cannot trigger Meta Keyguard to revoke keys. Using a key from
Meta Keyguard does not change this, because Keyguard hands out and expires keys but does
not observe what they are used for.

The script closes that gap by running `meta-cost record` once after each successful API
call. Cost Center stores the call alongside Muse's usage, prices it from the same rate
table, shows it on the project's page, and counts it toward any alert that covers the
project or the key.

## Requirements

- `meta-cost` on the `PATH`, in a version that has the `record` command. Check with
  `meta-cost record --help`; an older version reports an unknown command. Scripts should
  let the command be overridden with a `META_COST_COMMAND` environment variable, as
  `gen-image` does, so that a binary installed elsewhere can be used.
- The model must have a rate for the call to be priced. `meta-cost record` prints
  `"estimated_api_equivalent_usd": null` when no rate is known; the usage is still recorded
  and counted, and a rate can be added later on the dashboard's **Pricing** page, after
  which the call is priced retroactively.

## The `meta-cost record` command

```bash
meta-cost record --model MODEL [--images N] [--input-tokens N] [--output-tokens N]
  [--cached-tokens N] [--project NAME] [--tool NAME] [--session ID] [--key LABEL]
  [--billing-mode payg|subscription|unknown] [--id ID] [--at TIME]
```

| Option | Default | Meaning |
| --- | --- | --- |
| `--model` | required | The model ID sent in the request, such as `muse-image-1.0` or `muse-spark-1.3`. It must match the ID used in Cost Center's rates for the call to be priced. |
| `--images` | none | The number of images the response returned. Image models are billed per returned image, so count the images in the response rather than the number requested. |
| `--input-tokens` | 0 | Input (prompt) tokens from the response's usage object. This count **includes** any cached tokens. |
| `--output-tokens` | 0 | Output (completion) tokens, including reasoning tokens, from the response's usage object. |
| `--cached-tokens` | 0 | The part of the input tokens served from the prompt cache, which is billed at the lower cached rate. It must not exceed `--input-tokens`. |
| `--project` | the git repository, or folder, the command runs in | The project the call belongs to. Use the name of the repository's top-level folder, because that is how Muse names a workspace, so the script's usage lands on the same project page as the Muse sessions for that repository. Pass it explicitly, since a script may be run from any directory. |
| `--tool` | `script` | A short name for the script, such as `gen-image`. It labels the call on the session page and forms the default session name. |
| `--session` | `TOOL-YYYY-MM-DD` (local date) | The session the call is grouped under. The default groups each tool's calls into one session per day, which keeps the Sessions page readable; leave it unset unless the script has a natural grouping of its own, such as one batch job. |
| `--key` | `$META_KEYGUARD_KEY` | The Meta Keyguard label of the API key used. Alerts that revoke keys match on this label, so pass it whenever the key came from Keyguard, and omit it when the key came from anywhere else. |
| `--billing-mode` | `payg` | Direct API calls are paid with an API key, so leave the default. |
| `--id` | a random ID | A unique ID for the call. Recording the same ID again records nothing, so a script that retries a failed `record` cannot count a call twice. Generate one per API call, for example `TOOL-<uuid4 hex>`; it may contain letters, digits, and `._:-`, up to 128 characters. |
| `--at` | now | When the call was made, as an ISO 8601 time with a timezone, such as `2026-10-27T13:45:00-04:00`. Only needed when recording a call after the fact. |

`meta-cost record` also accepts the global `--db` and `--config` options, which a script
should not normally pass; the defaults are the database the dashboard reads.

On success the command exits with status 0 and prints one JSON object on stdout:

```json
{
  "recorded": true,
  "id": "gen-image-3f2a…",
  "tool": "gen-image",
  "project": "meta-global-ai-hackathon-jolly-challenge",
  "session": "gen-image-2026-10-27",
  "model": "muse-image-1.0",
  "billing_mode": "payg",
  "key_label": "jolly-challenge",
  "recorded_at": "2026-10-27T18:22:53.693725+00:00",
  "input": 120,
  "output": 8000,
  "cached": 0,
  "images": 2,
  "estimated_api_equivalent_usd": "0.02"
}
```

`recorded` is `false` when a call with the same `--id` was already recorded.
`estimated_api_equivalent_usd` is a decimal string, or `null` when the model has no rate.
On invalid input, such as a missing `--model` or a negative count, the command exits with
status 1 and writes a message beginning `meta-cost: ` to stderr.

## What a script should do

1. **Make the API call as usual.** Recording happens only after a call succeeds, because
   Meta does not bill failed requests, and for image models it does not bill images that
   were filtered out or not returned.
2. **Read the billed quantities from the response.** For image models that is the number
   of images in `response.data`. For text models it is the token counts in
   `response.usage`. The field names depend on which endpoint was called:

   | Endpoint | Input | Output | Cached |
   | --- | --- | --- | --- |
   | Chat Completions | `prompt_tokens` | `completion_tokens` | `prompt_tokens_details.cached_tokens` |
   | Responses, and image responses | `input_tokens` | `output_tokens` | `input_tokens_details.cached_tokens` |

   Image responses also report token counts. Record them for reference; they cost nothing,
   because image models are priced per image and their token rates are zero.
3. **Run `meta-cost record` once for that call**, with `--tool`, `--project`, `--model`, a
   new `--id`, the quantities from step 2, and `--key` if the key came from Keyguard.
   Batch scripts that make many calls record each one separately, so that every call is
   priced at the rate for its own model.
4. **Never let recording break the script.** By the time the script records, the API call
   has been made and paid for, and its output may already be saved. If `meta-cost` is
   missing, fails, or takes more than a few seconds, print a warning to stderr and carry
   on with exit status unchanged.
5. **Do not pass prompts or responses.** `meta-cost record` has no option for them, and
   nothing else about the call is needed.
6. **Offer a way to skip recording**, such as a `--no-record` flag, for test runs whose
   cost should not be counted.

## A helper to copy

This Python function implements the steps above for both image and text responses from
the OpenAI SDK. It uses only the standard library.

```python
import json
import os
import subprocess
import sys
import uuid
from pathlib import Path

COST_COMMAND = os.environ.get("META_COST_COMMAND", "meta-cost")
# The repository's folder name, which is the project name Cost Center uses for Muse sessions.
# Adjust the number of .parent steps to where the script sits in the repository.
PROJECT = Path(__file__).resolve().parent.parent.name


def _count(obj, *names):
    """The first of names that obj has as a non-negative integer attribute or key."""
    for name in names:
        value = obj.get(name) if isinstance(obj, dict) else getattr(obj, name, None)
        if isinstance(value, int) and value >= 0:
            return value
    return None


def record_usage(response, model, tool, key_label=None):
    """Report one successful API call to Meta Cost Center; returns the estimated USD or None.

    key_label is the Meta Keyguard label of the key used, or None if the key came from
    elsewhere. A failure only warns, because the call has already been made.
    """
    command = [COST_COMMAND, "record", "--tool", tool, "--project", PROJECT,
               "--model", model, "--id", f"{tool}-{uuid.uuid4().hex}"]
    data = getattr(response, "data", None)
    if isinstance(data, list):  # an image response: billed per returned image
        command += ["--images", str(len(data))]
    usage = getattr(response, "usage", None)
    if usage is not None:
        details = (getattr(usage, "input_tokens_details", None)
                   or getattr(usage, "prompt_tokens_details", None))
        counts = {
            "--input-tokens": _count(usage, "input_tokens", "prompt_tokens"),
            "--output-tokens": _count(usage, "output_tokens", "completion_tokens"),
            "--cached-tokens": _count(details, "cached_tokens") if details is not None else None,
        }
        for flag, value in counts.items():
            if value is not None:
                command += [flag, str(value)]
    if key_label:
        command += ["--key", key_label]
    try:
        proc = subprocess.run(command, capture_output=True, text=True, check=True, timeout=30)
        return json.loads(proc.stdout).get("estimated_api_equivalent_usd")
    except FileNotFoundError:
        print(f"warning: `{COST_COMMAND}` not found; this call was not recorded in Cost Center",
              file=sys.stderr)
    except subprocess.CalledProcessError as exc:
        print(f"warning: recording in Cost Center failed: {exc.stderr.strip() or exc}",
              file=sys.stderr)
    except (subprocess.TimeoutExpired, ValueError) as exc:
        print(f"warning: recording in Cost Center failed: {exc}", file=sys.stderr)
    return None
```

To know the Keyguard label, load the key with `meta-keyguard get <label>` and keep the
label alongside it, or run the script with `meta-keyguard exec <label> -- script …`, which
sets `META_KEYGUARD_KEY` to the label so that `meta-cost record` picks it up without
`--key`. A key that came from `--api-key` or a plain `META_API_KEY` has no label, so pass
`None`.

From a shell script, the equivalent is one command after each call, with the counts taken
from the response JSON, for example with `jq`:

```bash
meta-cost record --tool my-script --project "$(basename "$(git rev-parse --show-toplevel)")" \
  --model muse-spark-1.3 --id "my-script-$(uuidgen)" \
  --input-tokens "$(jq '.usage.prompt_tokens' response.json)" \
  --output-tokens "$(jq '.usage.completion_tokens' response.json)" \
  ${META_KEYGUARD_KEY:+--key "$META_KEYGUARD_KEY"} \
  || echo "warning: not recorded in Cost Center" >&2
```

## Checking the integration

Run the script once, then confirm the call reached Cost Center:

```bash
meta-cost export --period today --project <project-name>
```

The export should contain one row per call, with the `model`, `images`, `input`, `output`,
and `cached` the script passed, its `key_label`, a `billing_mode` of `payg`, and an
`estimated_api_equivalent_usd`. On the dashboard (`meta-cost show`), the project's page
lists a session named `<tool>-<date>`, and each call is a turn in it labelled "Recorded by
<tool>". If `estimated_api_equivalent_usd` is empty, the model has no rate; set one on the
**Pricing** page.

While developing, point the script at a throwaway database rather than the real one by
setting `META_COST_COMMAND` to a small wrapper that adds `--db`:

```bash
printf '#!/bin/sh\nexec meta-cost "$@" --db /tmp/cost-test.sqlite3\n' > /tmp/meta-cost-test
chmod +x /tmp/meta-cost-test
META_COST_COMMAND=/tmp/meta-cost-test ./my-script …
```

## Checklist

- [ ] Every successful API call runs `meta-cost record` exactly once, with a new `--id`.
- [ ] `--model` is the model ID sent in the request.
- [ ] Image calls pass `--images` with the number of images returned.
- [ ] Token counts come from `response.usage`, with cached tokens included in the input
      count and also passed as `--cached-tokens`.
- [ ] `--project` is the repository's folder name, passed explicitly.
- [ ] `--tool` names the script.
- [ ] `--key` is passed when, and only when, the key came from Meta Keyguard.
- [ ] A recording failure prints a warning and does not change the script's result.
- [ ] There is a way to skip recording for test runs.
- [ ] `meta-cost export --period today --project <name>` shows the call after one run.
