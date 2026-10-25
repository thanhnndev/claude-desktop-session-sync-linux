# Claude Desktop Session Sync — Linux

[English](README.md) | **Tiếng Việt**

Công cụ dòng lệnh cho **session local tạo trong tab Code của Claude Desktop**, ưu tiên CachyOS/Arch. Đồng bộ các bản ghi sidebar còn thiếu giữa các account và organization trên **cùng máy, cùng Linux user**. Transcript vốn dùng chung nên không cần chuyển sang CLI để làm tiếp.

**Trạng thái 0.1.1:** đã kiểm tra đường dẫn, schema, transcript và dry-run trên CachyOS với `claude-desktop 3.6556.2-1`; đã kiểm thử ghi/backup/undo trên dữ liệu thử. **Chưa kiểm chứng mở lại/tiếp tục session trong Desktop sau khi đổi account.** Không xem đây là bản đã được chứng nhận tương thích UI. Xem [báo cáo xác minh](docs/VERIFICATION.md).

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

4. **Quit hoàn toàn Claude Desktop và mọi Claude worker/CLI.** Đóng cửa sổ chưa chắc đã thoát. Không khởi động lại app trong lúc đang sync/undo. Công cụ kiểm tra tiến trình và từ chối ghi khi phát hiện Claude; không tự kill tiến trình.
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

## Không ghi đè và xử lý xung đột

- Chỉ thêm file còn thiếu vào các thư mục account/org đã có. File đích được xuất bản nguyên vẹn bằng hard link nguyên tử và từ chối tên đã tồn tại.
- Bản ghi cùng tên nhưng khác nội dung được báo **conflict**, không chọn bản mới nhất theo thời gian. Một transcript được nhiều ID nội bộ trỏ tới cũng bị chặn để tránh nhân đôi.
- Khi tiếp tục làm việc ở account B, transcript chung vẫn cập nhật; metadata riêng của account A có thể cũ. Bản 0.1.1 **không tự hòa giải metadata bị lệch**. Phải kiểm tra conflict trước khi chuyển lại. Đây không phải đồng bộ liên tục toàn trạng thái runtime.
- Dấu `deleted_<id>` ở bất kỳ bucket nào ngăn thêm lại session có ID/alias tương ứng (`cliSessionId`, `unarchivedCliSessionId`, `priorCliSessionIds`, ID tên file).
- Không dọn/di chuyển các bản sao cũ đã tồn tại của session có tombstone, không phát tán tombstone, không sửa worktree registry.
- Đọc toàn bộ transcript theo luồng, kể cả dòng lớn và phần lịch sử kế thừa. Session cần có đúng ID và nguồn Desktop. Nhánh chưa có lượt Desktop riêng chỉ được chấp nhận khi metadata trỏ đúng session cha đã được xác minh là Desktop, transcript chứa lịch sử Desktop của cha và cả ID của nhánh mới. Chỉ có lịch sử kế thừa không liên quan hoặc dấu nguồn CLI thì không đủ. Transcript thiếu/chưa rõ nguồn hoặc có `.desktop-released.json` sẽ bị bỏ qua; JSON bị lỗi hay lỗi đọc file sẽ dừng lập kế hoạch và báo rõ nguyên nhân.
- Transcript Desktop không còn pointer được liệt kê để điều tra; công cụ **không bịa ID mới** hoặc tự dựng metadata/worktree lease.

Các giới hạn trên ưu tiên giữ dữ liệu gốc. Chạy `plan` trước mỗi lần áp dụng.

## Backup và hoàn tác

Trước khi thêm bất kỳ file nào, công cụ ghi manifest chứa toàn bộ payload đã chọn và checksum vào:

```text
<userData>/.linux-session-sync-backups/<backup-id>/manifest.json
```

Thư mục `0700`, file `0600`. `--copy all` lưu cả secrets nằm trong bản ghi vào backup. Không đưa backup/đầu ra plan đầy đủ lên GitHub. Dùng `--summary` nếu chỉ cần danh sách thao tác; không có option này, JSON có nội dung metadata.

Backup là bản sao của **các bản ghi sẽ được thêm**, không phải backup toàn bộ transcript/profile. Transcript và bản ghi gốc không bị sửa. Manifest được ghi bền vững trước thao tác đầu tiên; khi lỗi giữa chừng, dùng backup ID trong tên thư mục để hoàn tác.

```bash
claude-session-sync undo --backup BACKUP_ID
claude-session-sync undo --backup BACKUP_ID --apply --offline
```

Undo đưa bản ghi được thêm vào quarantine trong cùng backup, không hủy nội dung. Nếu app đã sửa bản ghi, undo từ chối để không mất thay đổi mới. Không chạy cùng lúc với app hoặc người dùng khác sửa dữ liệu. Không tự xóa backup. Bản 0.1.1 vẫn hỗ trợ hoàn tác backup của bản 0.1.0.

## Build và test

Go 1.25+; không có dependency bên ngoài:

```bash
go test -race -cover ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o claude-session-sync .
```

Đóng gói Arch từ source: `makepkg --nodeps`. Cần `makepkg`, `fakeroot`, Go; `--nodeps` chỉ phù hợp khi công cụ build đã được cài (ví dụ Go qua mise). Không chạy bằng root.

Nguồn tham khảo: [repo Windows gốc](https://github.com/realperest/claude-desktop-session-sync), [issue #74662](https://github.com/anthropics/claude-code/issues/74662). Đây là triển khai Go độc lập, không phải script PowerShell đổi tên; không liên kết với Anthropic.
