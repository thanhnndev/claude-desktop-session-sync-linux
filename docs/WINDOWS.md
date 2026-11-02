# Windows support (0.4.0)

Runs natively on Windows 10/11 amd64; WSL is not required. This synchronizes local
Claude Desktop Code session records between existing accounts/organizations in
the same Windows profile. It does not move sessions between Windows and WSL,
translate paths, or import SSH/WSL sessions.

## Install and run

Build with Go 1.25+:

```powershell
go build -trimpath -o claude-session-sync.exe .
.\claude-session-sync.exe version
.\claude-session-sync.exe doctor --summary
.\claude-session-sync.exe plan --copy all --summary
```

Run the executable without arguments in a terminal for the English menu. For a
per-user installation, put it in a folder on your user PATH. No administrator
privileges are required. After signing into each desired account and organization
in Claude Desktop, fully quit Desktop and its workers before applying:

```powershell
claude-session-sync sync --copy all --apply --offline --summary
claude-session-sync undo --backup BACKUP_ID --apply --offline
```

Use `--copy chat` to copy history pointers while keeping target runtime settings.
`all` preserves the complete session record, including any embedded secrets.
Account login credentials are not copied. Preview the same mode and account scope
before applying. Existing account directories are required; identities are never
created by the tool.

## Discovery

Only existing roots containing `claude-code-sessions` qualify:

- `%APPDATA%\Claude`
- `%LOCALAPPDATA%\Claude-Data\*` (observed on the test machine)
- `%LOCALAPPDATA%\Packages\Claude_*\LocalCache\Roaming\Claude`
- `%LOCALAPPDATA%\Packages\Claude_*\LocalCache\Local\Claude`

Multiple qualifying roots require an explicit `--data-dir`. Transcripts default
to `%USERPROFILE%\.claude\projects`; override with `--projects-dir` if needed.
Selectors accept `ACCOUNT/ORG` or native Windows separators.

## Safety and platform differences

- Native process snapshots and token SIDs limit detection to the current user's
  `claude.exe`, `claude-desktop.exe`, `claude_code.exe` and `claude-code.exe`.
  Uninspectable matching processes block writes. Inaccessible unrelated processes
  do not. Desktop Electron children normally use `claude.exe` as well.
- `--force-quit` or the explicit menu choice terminates selected processes
  immediately on Windows. PID creation time and owner are checked on the same
  handle used for termination. Newly started processes are caught by the write
  guard. Force quit can lose unsaved work.
- A native exclusive file lock prevents concurrent sync/undo. Symlink/junction
  roots and unsafe relative paths are refused.
- Backups keep the existing `.linux-session-sync-backups` name for compatibility.
  Their Windows DACL is restricted to the current user and SYSTEM, with inheritance
  for new backup contents. Unix `0700`/`0600` checks remain on Linux.
- Payloads and manifests are flushed before publication. Windows does not offer
  the Unix directory-fsync guarantee through this implementation; power-loss
  durability of directory metadata/rename is not promised. Ordinary apply/undo,
  no-clobber publication and stale-target checks are covered by tests.
- Windows and Linux backups retain native absolute paths/session configurations;
  backups are for the originating profile, not cross-OS migration.

## Validation

Native Windows unit/integration tests cover synchronization, updates, deletion
markers, backup/undo, exclusive locking, ACL inheritance, discovery ambiguity,
slash selectors, process detection and stale-PID protection. Process termination
tests stop only a disposable helper created by the test.

To opt into replaying your live data on a temporary copy:

```powershell
$env:CLAUDE_SYNC_TEST_LIVE_COPY = '1'
go test -v -run TestWindowsLiveDataCopyRoundTrip .
Remove-Item Env:CLAUDE_SYNC_TEST_LIVE_COPY
```

The replay creates a synthetic destination only inside `t.TempDir()`, exercises
both copy modes, verifies idempotence and exact undo, and compares live session
and transcript hashes before/after. It currently expects one live account bucket.
It never bypasses the process guard for the original profile.

On 2026-11-02 the current machine yielded 5 accepted records, one account/org
bucket and 3 unsupported records. Both modes replayed
5 additions on the disposable copy and undid exactly. The live app had 12 Claude
processes; writes were refused. Live cross-account UI continuation cannot be
verified with only one account bucket and remains unverified.
