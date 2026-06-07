# Live Provider Smoke

Use this when you need to prove the ChatGPT or Claude web sync path against a real logged-in browser session. Public CI and redacted fixtures prove parser and archive behavior; this smoke proves the current web app contract and browser-auth boundary on a real account.

The smoke script builds the current checkout, uses an isolated temp `HOME`/XDG runtime, and prints only aggregate fields. It does not print message text, raw provider JSON, cookies, auth headers, browser storage, or archive rows.

## Start A Logged-In CDP Browser

Use a dedicated browser profile per provider. Log in normally in the browser window that opens.

```bash
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  --user-data-dir "$HOME/.cache/aicrawl/browser-profiles/chatgpt" \
  --remote-debugging-address=127.0.0.1 \
  --remote-debugging-port=9222 \
  https://chatgpt.com/
```

```bash
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  --user-data-dir "$HOME/.cache/aicrawl/browser-profiles/claude" \
  --remote-debugging-address=127.0.0.1 \
  --remote-debugging-port=9223 \
  https://claude.ai/
```

## Dry-Run Proof

Dry-run mode performs list-only same-origin page-context fetches. It must not fetch detail payloads or write the archive.

```bash
scripts/live-provider-smoke.sh --provider chatgpt --cdp-url http://127.0.0.1:9222 --mode dry-run
scripts/live-provider-smoke.sh --provider claude --cdp-url http://127.0.0.1:9223 --mode dry-run
```

Passing evidence is aggregate output with `source_kind live_list` and a candidate count, or a clear `live_list_unavailable` warning if the logged-in provider page is not attached. Do not paste raw JSON output into public issues or PRs.

For native ChatGPT macOS app coverage, add the app cache root. This discovers conversation IDs from `conversations-v3-*/*.data` filenames only; dry-run does not read cache bodies or fetch detail payloads.

```bash
scripts/live-provider-smoke.sh --provider chatgpt \
  --cdp-url http://127.0.0.1:9222 \
  --chatgpt-app-cache "$HOME/Library/Application Support/com.openai.chat" \
  --mode dry-run \
  --max-conversations 10
```

Passing evidence is aggregate output with `source_kind chatgpt_app_cache_ids` and a candidate count.

## Write Proof

Write mode imports at most `--max-conversations` recent conversations into the script's isolated temp archive. Use this only when it is acceptable to place a bounded copy of real recent transcript data in a temporary local archive.

```bash
scripts/live-provider-smoke.sh --provider chatgpt --cdp-url http://127.0.0.1:9222 --mode write --max-conversations 1
scripts/live-provider-smoke.sh --provider claude --cdp-url http://127.0.0.1:9223 --mode write --max-conversations 1
```

Passing evidence is aggregate output with provider/source kind plus conversation and message counts. The temporary archive is deleted automatically unless `--keep-output` is passed.

For ChatGPT app-cache-seeded write proof:

```bash
scripts/live-provider-smoke.sh --provider chatgpt \
  --cdp-url http://127.0.0.1:9222 \
  --chatgpt-app-cache "$HOME/Library/Application Support/com.openai.chat" \
  --mode write \
  --max-conversations 1
```

## Profile-Launch Boundary

The CLI can also launch/reuse a dedicated profile. A brand-new profile should report `login_required` and stop before archive writes.

```bash
scripts/live-provider-smoke.sh --provider chatgpt \
  --profile "$HOME/.cache/aicrawl/browser-profiles/chatgpt" \
  --mode write \
  --max-conversations 1
```

This path is useful for first-run UX validation, but CDP dry-run/write smoke against an already logged-in provider page is the stronger field-readiness proof.
