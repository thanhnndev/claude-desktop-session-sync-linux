# Evidence and compatibility report

Investigation date: 2026-10-25 (Asia/Ho_Chi_Minh).

## Primary sources

- Upstream examined at commit [`4a19d0b0ceefdcd571605d902754689fc41deb54`](https://github.com/realperest/claude-desktop-session-sync/tree/4a19d0b0ceefdcd571605d902754689fc41deb54), including `README.md`, `docs/FINDINGS.md`, `hesap-oturum-esitle.ps1` and `tests/Test-Sync.ps1`.
- [Issue #74662 and comments](https://github.com/anthropics/claude-code/issues/74662): the initial server-sidebar diagnosis was refined by later pointer-file/account/org observations. These are reports, not a public storage contract.
- [October cross-account Desktop versus terminal reproduction](https://github.com/anthropics/claude-code/issues/74662#issuecomment-6202310196): the author reports that the resume deep link imported a terminal-created session but did not import a Desktop-created session under another account. This prevents treating CLI resume as the requested fix.
- [Official Desktop reference](https://code.claude.com/docs/en/desktop) describes `claude --desktop --resume` for existing CLI sessions; it does not document this metadata synchronization protocol. Current documentation was retrieved with Context7 CLI, resolving `/websites/code_claude` first, then fetching Desktop resume documentation.
- [Official Linux documentation](https://code.claude.com/docs/en/desktop-linux) describes Linux beta for Debian/Ubuntu and recommends CLI on other distributions. The CachyOS installation below is a directly observed working package/layout, not evidence of official Arch support.

## Direct local observations before implementation

Environment: CachyOS (`ID=cachyos`, `ID_LIKE=arch`), x86_64. `pacman -Qo` and `pacman -Qi` identified `claude-desktop 3.6556.2-1`, installed from the `cachyos` repository. The installed desktop entry executes `claude-desktop %U`; the executable resolves to `/usr/lib/claude-desktop/claude-desktop`.

A bounded filesystem inventory found actual files under `~/.config/Claude/claude-code-sessions`, not an assumed XDG path. There were four account/org buckets, spanning two distinct account IDs and two organization IDs. One bucket held 89 `local_*.json` records; the other three were empty. No deletion markers were present in these four buckets. Account UUIDs, conversation titles, transcript contents and credentials are intentionally absent from this repository.

94 top-level project JSONL transcripts were present under `~/.claude/projects/*/*.jsonl`. Each of the 89 pointers had a matching transcript **filename**, and a Desktop entrypoint was found in that file. The initial 0.1.0 check saw two files whose first entrypoint records used a different `sessionId`, and skipped them because that bounded check did not prove lineage. The complete-file and parent-pointer investigation below corrects that limitation in 0.1.1. Filename presence alone is not enough to assert a resumable transcript.

App `logs/main*.log` independently contained `Loaded N persisted sessions from .../claude-code-sessions/<account>/<org>` lines for the observed folders, including account/org switches. This supports the real Linux metadata location and account/org scoping. It does not prove a newly copied record will successfully resume under a different login.

The real records contain, among other fields: `sessionId`, `cliSessionId`, `cwd`, `originCwd`, `gitAnchors`, timestamps, `isArchived`, title, `permissionMode`, `enabledMcpTools`, `remoteMcpServersConfig`, `sessionSettings`, `spawnSeed`, `promptAppendSnapshot`, and runtime/status fields. Treating all of those as mere history would lose workflow state. `--copy all` therefore copies the entire source file exactly, without automatic redaction; `--copy chat` is a separate minimal projection.

Global files `ccd-session-presets.json`, `mcp-user-tool-toggles.json` and `git-worktrees.json` also existed. Their top-level schemas were inspected without exporting values. They are not modified: this version does not claim account-wide saved-environment/configuration migration.

## Shipped artifact inspection

Installed `/usr/lib/claude-desktop/resources/app.asar`:

```text
package.json version: 3.6556.2
ASAR SHA-256: 5884c585791dd21c1aa6d933fc5fb634e3846f7020d0f12bf4771b8833109d8e
```

REA static inventory evidence: `ev_77f923c5addd64611a10980ef71d53898c2978eb8e2ecfd8434a7c122a87750d`.

Focused analysis parent evidence: `ev_309e0c8deda3a1f8d9ec5c3e0ca952d5981e20d350d34cbd721e8750e761af60`; summary evidence: `ev_7325b6c2ba52f664f05a7eb8620c7edbd7d5894075af085d2686470639606b6f`.

Exact selected packaged modules: `.vite/build/index.chunk-Bd0pIhWA.js` (persisted record hydration) and `.vite/build/index.chunk-M-nBbhoO.js` (local session manager). Manual inert-source inspection found `loadSessionRecords()` enumerating `local_*.json`, parsing JSON, hydrating the record, using its stored `sessionId`, and loading local filesystem/worktree state. The hydration helper (`RM`, exported as `Mt` in this build) reads permission, MCP, session settings, worktree and runtime fields. `writeSessionToDisk()` serializes session state and can overwrite its on-disk copy from memory, supporting the offline-only design.

This was static inspection, not execution. Analysis selected two modules, left 561 inventoried files outside scope, and hit graph/unknown/primitive-byte retention limits. It does not establish a complete control-flow or authentication model. No proprietary app code is redistributed here.

## Implementation verification

- Unit/integration fixtures exercise both copy modes; exact-byte preservation of all-mode configuration including embedded test credentials; Desktop versus CLI transcript provenance; alias collisions; corrupt JSON; conflicting records; filename/ID validation; tombstones for all alias kinds; release sidecars; orphan reporting; filesystem path/symlink/FIFO refusal; exclusive locking; stale plans; process refusal; backup permission/checksum; additive publication; idempotence; undo; retention of user-modified records.
- Apply/undo integration tests run in temporary directories with a stubbed process guard. Production retains process inspection. The actual machine's `doctor` did independently detect a running Claude process.
- Real-data inspection was **read-only**: `doctor` and `plan`. Both modes produced 261 proposed additions (87 proven pointers × 3 missing buckets); two pointers were skipped due to the lineage mismatch above. `doctor` additionally reported Claude still running.
- `--copy all` keeps original bytes. Existing records are never updated, even if another account has a newer copy; conflicts are reported. Transcripts are read for provenance but never edited or copied. This is additive sidebar recovery, not a continuously reconciled metadata database.
- Final test/build results and artifact checksums are in `BUILD-REPORT.txt` and `SHA256SUMS` in the release bundle.

## Kiem chung UI con thieu

**Not performed:** real-user account logout/login automation, mutation of live user metadata, reopening a migrated session in the UI, sending a new turn under the second account, worktree execution across accounts, MCP authentication under the target account, saved environment resolution, or physical ARM64 execution. Desktop was running; it was not terminated or signed out to conduct a destructive live experiment.

To finish UI validation on the installed version:

1. Create a disposable session **from Desktop Code**, give it a recognizable message and a non-sensitive session setting. Record the app version and original account/org bucket.
2. Sign in to the target account/org once, then quit the whole app and its workers.
3. Run `plan --copy all --from ... --to ... --summary`; verify one intended session's IDs/configuration. Use a disposable data set for the experiment, or review the complete plan before applying; `--from/--to` selects buckets, not a single record.
4. Apply offline; record the backup ID; launch Desktop under the target login. Verify sidebar listing, full history, cwd/branch/worktree, session settings and MCP availability.
5. Send a harmless follow-up, quit again, and inspect the resulting pointer/transcript. Check for changed CLI IDs or conflicting metadata before switching back.
6. Test `chat` separately, expecting default permissions and no copied session configuration. Test undo before any target session edit; after an edit, expect undo to refuse that record.

Do not mark cross-account resume verified merely because a pointer copy succeeded. Different app versions, account policies, account-scoped environments, pruned transcripts, missing worktrees or app-side import rules can still prevent continuation.

## Differences from upstream in the initial 0.1.0 release

The Windows upstream chooses newest valid versions, propagates tombstones, quarantines stale records, repairs transcript-only entries/worktree leases, merges some global account settings, and installs hooks/watchers. The initial 0.1.0 release started with an offline additive core. It has no watcher, hooks, scheduled services, automatic token swapping, newest-wins replacement, tombstone cleanup, fabricated orphan pointers, global settings merge or lease repair. Those features are not claimed complete.

## Follow-up fix: complete transcripts and Desktop forks (0.1.1)

The 0.1.0 provenance check read only the first 100 lines / 1 MiB and ignored scanner errors. This wrongly excluded two real Desktop forks with inherited parent history. Their names, IDs and contents remain local and are not included in this report.

Direct observation of the two full JSONL files and their existing Desktop pointer records established:

- One child has 1,514 transcript lines. Its own ID first appears at line 1,419, and its own matching `entrypoint: claude-desktop` record at line 1,426. The earlier prefix belongs to its parent; this is direct Desktop provenance beyond the old limit.
- The other child has 1,367 lines, with its own ID in metadata rows from line 1,362 onward and no child conversation turns yet. Its `forkedFromSessionId` and `spawnedFrom.sessionId` agree and name an existing Desktop parent pointer. That parent's `cliSessionId` exactly matches the Desktop identity in the inherited child transcript. The parent also has its own proven Desktop transcript. This verifies the relationship without guessing IDs or modifying either file.

0.1.1 streams each complete JSONL file with memory bounded by the largest line, rather than the entire transcript. Large lines and final lines without a newline are supported. Invalid JSON or read errors fail explicitly instead of silently leaving a partial provenance result. Symlinks and non-regular transcripts are refused before opening.

A child without a direct Desktop entrypoint needs its own ID in the file, an explicit parent reference, a proven parent transcript, and matching inherited Desktop identity for a known parent ID/alias. Conflicting parent identities, disagreement between parent references, CLI entrypoints, unrelated inherited history, missing parents and cyclic unproven ancestry are rejected. `lineage_verified_sessions` reports accepted inherited forks. The minimal chat projection retains the parent reference so subsequent runs do not lose that evidence. Both 0.1.0 and 0.1.1 backup manifests remain supported by undo.

New regression tests cover both real failure patterns using synthetic data, markers after 1,500 inherited lines, lines over 1 MiB, malformed tails, FIFO transcripts, invalid fork relationships, and compatibility with older backups.

**Updated real-data dry-run:** 89 records, four account/org buckets, **267 proposed additions**, zero missing/unproven transcripts, zero warnings, one fork verified through parent lineage. No live Claude metadata or transcripts were written. UI resume/new-turn validation remains outstanding.

## English interactive interface and explicit process control (0.2.0)

The terminal menu, prompts, errors and results are English. Running without arguments in a terminal opens the menu; `interactive` opens it explicitly. Direct commands accept `--interactive` for Force quit / Retry / Cancel prompts and concise results. `--force-quit` is an explicit non-interactive apply option and cannot run on a dry run.

Process discovery is shared between the write guard and force-quit path. It checks the current UID, exact Claude executable names, and Claude app paths for Electron. It recognizes upgraded executables marked `(deleted)` and excludes this tool and unrelated Electron apps. The force-quit operation uses a fixed process snapshot and checks process start identity before signalling; on supported Linux kernels Go pins the process with a pidfd. TERM is followed by a bounded wait and KILL only for survivors from that snapshot. Newly started processes are handled by the existing guard, not killed automatically.

Tests exercise Retry, Force quit, Cancel, EOF, invalid choices, failures, English menu routing, snapshot TERM/KILL escalation, non-interactive flag validation, classification, current process identity and refusal to signal a changed identity. A signal integration test only starts and terminates its own harmless `/bin/sleep` child; no real user Claude process is stopped by the automated tests. Undo remains compatible with both 0.1.0 and 0.1.1 manifests. Force quitting live Claude can interrupt work and is only performed after explicit selection or an explicit flag.

## Bidirectional metadata and deletion reconciliation (0.3.0)

The additive-only limitation of earlier releases is removed. All existing account/org directories are both sources and destinations by default. The newest valid record is selected by source file modification time, with `lastActivityAt` as a tie-breaker; the original source modification time is preserved on every copy/update. Equally recent differing records remain explicit conflicts. A user-selected `--from` is authoritative for one-way transfers.

Create, update, quarantine and tombstone actions carry a complete write-ahead manifest. Existing bytes and timestamps, planned new bytes and checksums are all stored before any metadata changes. Updates are staged/fsynced and atomically renamed after checking the expected target again. Deletion markers are copied across destinations, and matching stale records are preserved in backup/quarantine before removal from the active list. Undo restores original versions and timestamps, retains applied versions separately, skips unapplied/already-restored actions after interruption, and refuses targets modified by the user. Old 0.1.0, 0.1.1 and 0.2.0 manifests remain supported.

Both old and newly referenced transcript files stay untouched. These accounts share the same projects directory on this machine: messages do not need to be copied between account buckets. Updating the metadata's current/prior transcript IDs brings accounts onto the current conversation. This does not claim cross-machine transcript transfer or merge divergent message histories.

Regression coverage includes A-to-B-to-A round trips, new messages in the shared transcript, configuration changes, advancing transcript IDs without losing previous files, parent pointers advancing while forks retain old history, preserved modification times/idempotence, deletion propagation, marker updates, original/replacement backup retention, undo, partial apply recovery, explicit source resolution, tied conflicts, stale plans and target-owned configuration in chat mode.

These are executable filesystem tests with synthetic records and read-only checks of the actual machine. They do not replace the user's validation of listing and resuming sessions in Claude Desktop under different logins. No automated account login or UI send was performed.

## Protected unrelated processes and first live apply (0.3.1)

The first real apply was initially blocked because the user systemd process denied `/proc/<pid>/exe` inspection. The guard now checks an accessible process name when executable inspection returns a permission error. Protected unrelated processes such as systemd do not block synchronization; processes named Claude/Electron, unknown identities and unexpected errors still fail closed. No unrelated process is signalled. Regression tests cover both categories.

After that fix, an explicitly requested force-quit/apply completed on the real CachyOS dataset: 89 source records, 267 additions, zero warnings/unproven transcripts. All four account/org buckets contained 89 records. Every added file was compared byte-for-byte with its write-ahead backup payload. The backup manifest is mode 0600. No transcripts were rewritten. Claude Desktop was then launched for the user to validate account switching and continuation; no automated UI resume/send is claimed. Undo of 0.3.0 backups remains supported in 0.3.1.

## Worktree metadata copies sharing a transcript ID (0.3.2)

On 2026-10-27, a real Desktop session had the same transcript filename in two project directories: an approximately 8.9 MB Desktop history and a 1.8 KB file in a worktree project directory. The latter contained only last-prompt/title/agent/latch/cost/queue metadata, with no conversation UUIDs or Desktop entrypoint. The stored Desktop record referenced the original project. Version 0.3.1 incorrectly rejected the filename collision globally before provenance checks. No transcript contents, private project names or user IDs are published here.

The index now retains all copies and validates Desktop provenance independently for each file. A metadata-only stub cannot replace or contaminate a proven history. Byte-identical proven copies are accepted using a streaming SHA-256 of the complete raw bytes; distinct proven histories cause a warning and skip only that session. The resolver does not select by size or timestamp and does not assume project-directory encodings. Fork lineage also uses independent per-file proofs; ambiguous parents cannot prove a child. Release sidecars beside any copy, including a stub, prevent synchronization of that session. Existing malformed-file, read-error, symlink and non-regular-file protections remain in place.

Regression tests cover stubs sorted before/after the history, both copy modes, backup/apply/undo with all transcripts unchanged, identical copies and unique orphan reporting, conflicting histories while unrelated sessions continue, refusal to combine fork evidence from separate files, fork/parent stubs, release sidecars beside stubs/duplicates, a conflicting copy appearing after planning, CLI-only rejection, and undo compatibility with 0.3.1 backups.

A real-data dry run completed successfully: 475 records, 213 proposed additions, 165 proposed updates, zero missing/unproven transcripts and zero warnings. The previously blocked session was planned for all three missing buckets. Both files reported in the collision retained their exact SHA-256 values across the dry run. No live apply was performed for this 0.3.2 fix; the live apply recorded above belongs to 0.3.1. Physical ARM64 execution and complete UI/configuration validation remain outside the automated tests.
