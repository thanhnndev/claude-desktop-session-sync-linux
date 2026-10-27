# Run from the repository root: makepkg --nodeps
pkgname=claude-desktop-session-sync-linux
pkgver=0.3.2
pkgrel=1
pkgdesc='Offline bidirectional synchronization of Claude Desktop Code session records'
arch=('x86_64' 'aarch64')
url='https://github.com/thanhnndev/claude-desktop-session-sync-linux'
license=('MIT')
depends=()
makedepends=('go')
source=()
sha256sums=()

build() {
  cd "$startdir"
  CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$srcdir/claude-session-sync" .
}

check() {
  cd "$startdir"
  go test .
}

package() {
  install -Dm755 "$srcdir/claude-session-sync" "$pkgdir/usr/bin/claude-session-sync"
  install -Dm644 "$startdir/README.md" "$pkgdir/usr/share/doc/$pkgname/README.md"
  install -Dm644 "$startdir/README_VI.md" "$pkgdir/usr/share/doc/$pkgname/README_VI.md"
  install -Dm644 "$startdir/docs/VERIFICATION.md" "$pkgdir/usr/share/doc/$pkgname/docs/VERIFICATION.md"
  install -Dm644 "$startdir/LICENSE" "$pkgdir/usr/share/licenses/$pkgname/LICENSE"
}
