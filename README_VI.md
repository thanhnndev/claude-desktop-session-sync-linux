# Claude Desktop Session Sync — Linux

[English](README.md) | **Tiếng Việt**

Công cụ dòng lệnh cho **session local tạo trong tab Code của Claude Desktop**, ưu tiên CachyOS/Arch. Đồng bộ hai chiều các bản ghi sidebar giữa các account và organization trên **cùng máy, cùng Linux user**. Transcript vốn dùng chung nên không cần chuyển sang CLI để làm tiếp.

**Trạng thái 0.3.2:** đã kiểm tra đường dẫn, schema, transcript và dry-run trên CachyOS với `claude-desktop 3.6556.2-1`; đã kiểm thử ghi/backup/undo trên dữ liệu thử. **Chưa kiểm chứng mở lại/tiếp tục session trong Desktop sau khi đổi account.** Không xem đây là bản đã được chứng nhận tương thích UI. Xem [báo cáo xác minh](docs/VERIFICATION.md).

## Hai lựa chọn sao chép

| Option | Nội dung |
|---|---|
| `--copy all` **(mặc định)** | Sao chép nguyên byte bản ghi session: cấu hình, quyền, MCP, trạng thái, liên kết transcript/worktree, và mọi trường khác. **Không lọc secrets nằm trong bản ghi.** |
| `--copy chat` | Tạo bản ghi tối thiểu để mở lịch sử, giữ ID và thông tin thư mục/worktree. Không chuyển cấu hình chạy/MCP/quyền đã cấp; đặt permission mode về `default`. |

`all` giữ nguyên cấu hình **của session**. Không đọc, chuyển hay tráo cookie đăng nhập Claude, keyring, profile trình duyệt, `~/.claude/.credentials.json` hoặc token tài khoản. Đăng nhập/chuyển account được thực hiện trong Claude Desktop. MCP dùng OAuth của account mới có thể cần đăng nhập lại.

Phạm vi `all` là nội dung `local_*.json`, **không phải toàn bộ profile Claude**. Các thiết lập toàn cục và saved environment theo account (`ccd-session-presets.json`, `mcp-user-tool-toggles.json`, extensions, settings/hooks) không được gộp. Công cụ giữ nguyên các tham chiếu nằm trong session; một tham chiếu tới cấu hình riêng của account cũ chưa được chứng minh hoạt động ở account mới. Không có bộ lọc ngầm trong `all`.

## Cài trên CachyOS/Arch

Bản binary là ELF tĩnh, không cần Python, Node, Go hay quyền root để chạy. Claude Desktop cần được cài sẵn và có tab Code hoạt động. Máy khảo sát có package `claude-desktop` từ repository `cachyos`; công cụ này không cài hoặc đóng gói lại Claude Desktop.

Tải và giải nén bản `linux-amd64.tar.gz` từ Releases, sau đó:

```bash
sha256sum -c SHA256SUMS --ignore-missing
mkdir -p ~/.local/bin
install -m 755 claude-session-sync ~/.local/bin/claude-session-sync
~/.local/bin/claude-session-sync version
```

Hoặc cài file `*.pkg.tar.zst` đã build bằng `sudo pacman -U <file.pkg.tar.zst>`. ARM64 chỉ được cross-compile; chưa chạy thử trên phần cứng ARM64.

## Giao diện terminal tiếng Anh, Force quit và Retry

Chạy `claude-session-sync` trong terminal hoặc `claude-session-sync interactive` để mở menu tiếng Anh. Menu có xem trước, backup + đồng bộ và hoàn tác. Kết quả tương tác hiển thị số lượng ngắn gọn, không in toàn bộ JSON.

Nếu Claude còn chạy khi apply hoặc undo, app cho chọn:

- **R — Retry:** bro tự đóng Claude rồi kiểm tra lại.
- **F — Force quit:** dừng các tiến trình Claude Desktop/CLI/worker được phát hiện thuộc Linux user hiện tại, kiểm tra lại rồi tiếp tục. Thao tác có thể ngắt công việc đang chạy. App gửi TERM, đợi tối đa 6 giây, rồi chỉ KILL các tiến trình trong danh sách ban đầu vẫn còn chạy. Danh tính tiến trình được kiểm tra trước khi gửi tín hiệu; Claude mới khởi động lại sẽ không tự bị dừng.
- **C — Cancel:** hủy thao tác, không bắt đầu đồng bộ mới.

Dùng prompt tương tác với lệnh trực tiếp:

```bash
claude-session-sync sync --copy all --apply --offline --interactive
```

Hoặc chọn dừng tiến trình rõ ràng ngay từ câu lệnh:

```bash
claude-session-sync sync --copy all --apply --offline --force-quit --summary
```

`--force-quit` bắt buộc có `--apply --offline`, không dùng được với dry-run. Không chọn **F** và không truyền cờ này thì app không tự dừng tiến trình. Backup được tạo sau khi Claude đã dừng và trước khi ghi session đầu tiên. Force quit không bảo đảm Claude kịp lưu phần việc chưa được ghi xuống đĩa.

## Đăng nhập nhiều account và làm tiếp công việc

1. Trong **Claude Desktop**, đăng nhập account A, mở tab **Code**. Làm tương tự với account B và các organization cần dùng. Việc đăng nhập tạo các thư mục tương ứng; công cụ chỉ sử dụng thư mục đã tồn tại, không tạo account giả.
2. Xem dữ liệu và account/org đã được phát hiện:

   ```bash
   claude-session-sync doctor --summary
   ```

   Trường `buckets` liệt kê `<account-uuid>/<org-uuid>`. Đây là ID thư mục, không phải email hay account đang active. Công cụ không đoán account hiện tại.

3. Xem trước toàn bộ thao tác; **không thay đổi file**:

   ```bash
   claude-session-sync plan --copy all --summary
   ```

   Chỉ muốn lịch sử:

   ```bash
   claude-session-sync plan --copy chat --summary
   ```

   Chỉ chuyển từ A sang B, thay các giá trị dưới đây bằng ID từ `doctor`:

   ```bash
   claude-session-sync plan --from ACCOUNT_A_UUID/ORG_A_UUID --to ACCOUNT_B_UUID/ORG_B_UUID --copy all --summary
   ```

4. **Quit hoàn toàn Claude Desktop và mọi Claude worker/CLI.** Đóng cửa sổ chưa chắc đã thoát. Không khởi động lại app trong lúc đang sync/undo. Công cụ kiểm tra tiến trình và từ chối ghi khi Claude còn chạy. Giao diện tương tác có Force quit / Retry / Cancel; chỉ dừng tiến trình khi chọn Force quit hoặc truyền rõ `--force-quit`.
5. Áp dụng đúng chế độ/phạm vi vừa xem:

   ```bash
   claude-session-sync sync --copy all --apply --offline --summary
   ```

   Hoặc dùng `--copy chat`, hoặc thêm cùng `--from`/`--to` đã chọn ở bước xem trước. Bỏ `--apply` thì `sync` cũng chỉ dry-run. `--dry-run` được hỗ trợ; không được kết hợp với `--apply`.

6. Mở Claude Desktop, đăng nhập/chọn account và organization đích, mở **Code → Local/Recents** rồi chọn session. Kiểm tra lịch sử, thư mục, nhánh và cấu hình trước khi gửi tiếp. Việc hiển thị và gửi tiếp dưới account khác cần được kiểm chứng trên bản app của bạn; ghi nhận theo [checklist](docs/VERIFICATION.md#kiem-chung-ui-con-thieu).

Không dùng `claude --resume` hoặc import CLI để thay thế bước này: mục tiêu của công cụ là bản ghi sidebar của session Desktop.

## Đường dẫn được xác minh và đường dẫn tùy chỉnh

Trên máy CachyOS đã khảo sát:

```text
~/.config/Claude/claude-code-sessions/<account>/<org>/local_<id>.json
~/.claude/projects/<project>/<cli-session-id>.jsonl
```

`doctor` chỉ chấp nhận root thực có `claude-code-sessions`, không tạo các đường dẫn phỏng đoán. Nó kiểm tra cả `$XDG_CONFIG_HOME/Claude` và `~/.config/Claude`; nếu tìm được nhiều root khác nhau hoặc không có root phù hợp, phải chỉ định:

```bash
claude-session-sync doctor --data-dir /duong/dan/userData-da-kiem-tra --projects-dir /duong/dan/projects-da-kiem-tra --summary
```

Các lệnh khác nhận cùng hai option này. Không tự chuyển đổi layout Flatpak, Wine, Windows, macOS, SSH hay WSL.

## Đồng bộ hai chiều, message và session đã xóa

Mặc định, **mọi thư mục account/org đã có đều vừa là nguồn vừa là đích**. App thêm session còn thiếu và cập nhật bản ghi đã có khi nơi khác có phiên bản mới hơn. Chạy sync trước khi đổi account để account tiếp theo nhận đúng transcript và trạng thái mới nhất.

- Chọn bản hợp lệ mới nhất theo thời gian sửa file; dùng `lastActivityAt` để phân định khi bằng thời gian sửa. Giữ nguyên thời gian nguồn khi chép để tránh cập nhật qua lại mãi. Nếu hai bản vẫn khác nhau nhưng cùng độ mới, app giữ nguyên và báo xung đột; dùng `--from` để chọn nguồn rõ ràng.
- `--from ACCOUNT/ORG --to ACCOUNT/ORG` giới hạn thành chuyển một chiều từ nguồn đã chọn. Nguồn này có thể thay thế cả bản đích mới hơn; hãy xem plan trước. Mỗi bản đích bị thay thế đều được backup.
- `--copy all` giữ nguyên bản ghi mới nhất, toàn bộ cấu hình và ID transcript hiện tại/trước đó. Bản ghi đã có chỉ được thay thế nguyên tử sau khi backup bền vững cả dữ liệu trước và sau thay đổi.
- `--copy chat` cập nhật lịch sử và liên kết transcript/worktree, giữ cấu hình chạy cùng quyền riêng của đích đã tồn tại. Đích mới dùng bản ghi tối thiểu và quyền mặc định.
- File message trong thư mục projects dùng chung **không bị ghi đè, viết lại hay gộp**. Các account đọc cùng transcript trên máy. Cập nhật metadata giúp account trỏ đúng transcript mới nhất, kể cả khi CLI session ID thay đổi. Toàn bộ file transcript cũ vẫn được giữ.
- Truyền dấu xóa sang các thư mục đích. Bản ghi cũ khớp dấu xóa được đưa khỏi danh sách hoạt động, nhưng toàn bộ byte vẫn còn trong backup/quarantine. So dấu xóa với ID tên file, `cliSessionId`, `unarchivedCliSessionId` và `priorCliSessionIds` để tránh session xuất hiện lại.
- Nhiều ID nội bộ khác nhau trỏ cùng transcript vẫn được báo để xử lý, không tự gộp. Không phát tán bản ghi hỏng. Transcript cần được đọc toàn bộ và xác minh nguồn Desktop hoặc quan hệ cha của nhánh; thiếu/chưa rõ nguồn hoặc có `.desktop-released.json` thì bỏ qua. Lỗi đọc hay JSON hỏng được báo rõ và dừng lập kế hoạch.
- Transcript không còn pointer được liệt kê để điều tra; không tự bịa ID, dựng worktree lease hay sửa registry worktree.

Sau khi làm việc ở account khác, chạy sync lại trước khi đổi về. Đây là thao tác offline, chưa phải watcher chạy cùng Claude. Menu có Force quit / Retry / Cancel. Việc gửi tiếp trong UI dưới account khác vẫn cần kiểm tra trên bản app đang cài.

## Backup và hoàn tác

Trước khi thêm bất kỳ file nào, công cụ ghi manifest chứa toàn bộ payload đã chọn và checksum vào:

```text
<userData>/.linux-session-sync-backups/<backup-id>/manifest.json
```

Thư mục `0700`, file `0600`. `--copy all` lưu cả secrets nằm trong bản ghi vào backup. Không đưa backup/đầu ra plan đầy đủ lên GitHub. Dùng `--summary` nếu chỉ cần danh sách thao tác; không có option này, JSON có nội dung metadata.

Backup chứa **cả byte gốc và byte thay thế cho mọi thay đổi dự kiến**, không phải toàn bộ transcript/profile. Manifest được ghi bền vững trước thay đổi đầu tiên; metadata sau đó có thể được thay thế nguyên tử hoặc đưa vào quarantine, còn transcript không bị sửa. Khi lỗi giữa chừng, dùng backup ID để hoàn tác phần đã thực hiện, giữ nguyên phần chưa chạy.

```bash
claude-session-sync undo --backup BACKUP_ID
claude-session-sync undo --backup BACKUP_ID --apply --offline
```

Undo phục hồi phiên bản trước, khôi phục bản ghi đã được sync đưa vào quarantine và đảo ngược các dấu xóa/bản ghi mới được thêm. Phiên bản đã áp dụng vẫn còn trong thư mục `undone` của backup. Nếu app đã sửa bản ghi sau sync, undo từ chối ghi đè để không mất thay đổi mới. Không chạy cùng lúc với app hoặc người dùng khác sửa dữ liệu. Không tự xóa backup. Bản 0.3.2 vẫn hỗ trợ hoàn tác backup của bản 0.1.0, 0.1.1, 0.2.0, 0.3.0 và 0.3.1.

## Build và test

Go 1.25+; không có dependency bên ngoài:

```bash
go test -race -cover ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o claude-session-sync .
```

Đóng gói Arch từ source: `makepkg --nodeps`. Cần `makepkg`, `fakeroot`, Go; `--nodeps` chỉ phù hợp khi công cụ build đã được cài (ví dụ Go qua mise). Không chạy bằng root.

Nguồn tham khảo: [repo Windows gốc](https://github.com/realperest/claude-desktop-session-sync), [issue #74662](https://github.com/anthropics/claude-code/issues/74662). Đây là triển khai Go độc lập, không phải script PowerShell đổi tên; không liên kết với Anthropic.

## Transcript trùng ID trong worktree

Cùng transcript ID có thể có một file lịch sử Desktop và các file phụ chỉ chứa metadata ở những thư mục khác. Bản 0.3.2 xác minh từng file riêng và không để file phụ chặn đồng bộ. Các bản lịch sử giống hệt nhau được chấp nhận; các lịch sử đầy đủ khác nhau được báo xung đột và chỉ bỏ qua session liên quan. Sidecar release ở cạnh bất kỳ bản nào đều chặn session. Không di chuyển, gộp hay xóa transcript.
