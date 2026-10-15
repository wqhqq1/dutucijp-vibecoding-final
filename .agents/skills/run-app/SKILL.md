---
name: run-app
description: Build, start, test and diagnose this local returns reconciliation app while preserving official data, private credentials and mandatory account security. Use when asked to run or verify this repository.
---

# Run the reconciliation app

Read AGENTS.md, the latest actual-date 执行交接.md and 运行说明.md before commands. Work from the repository root. Preserve existing changes and business histories.

On 10-22 or 11-17 in Asia/Shanghai, use only the previously built and rehearsed binary/runtime. Do not install, rebuild, initialize accounts, prepare data, fix code or change materials. A normal login and preset business action remain allowed.

For a normal execution day:

1. Check `git status --short`, actual installed dependencies and the current handover. Use `./build.ps1` only when the binary needs building. It locks frontend dependencies and embeds static resources.
2. Reuse the private demo runtime identified by the latest handover. Start with `./start.ps1 -Database <existing database> -Binary <verified binary>`. Do not clear a database. Creating a new demo copy requires an explicit request or the user's requested independent reproduction.
3. Use `http://localhost:8080`. Every account must configure a strong password, then actually verify Passkey or TOTP and confirm recovery-code saving. Only the user configures real passwords, devices and recovery material. Never read or log the initialization links, master key, OTP, cookies or recovery codes.
4. Run `./test.ps1` for actual events, and read its real summary. Use verification scripts only in their marked isolated directories. Do not create login bypasses or substitute fixtures for official business data.
5. If a failure appears, report its actual command, safe error and evidence path. Correct it within authorized scope, rerun the relevant check and preserve the original failure. Distinguish protocol/fixture tests from real-device and human actions.

At handoff, record FR/TC scope, actual date, HEAD, output paths, server/binary/runtime status and pending user actions. Stop editing before the tutoring chat archives or backs up. A skill file being present is not evidence that it was invoked or survived a restart; record those only after actual user execution.
