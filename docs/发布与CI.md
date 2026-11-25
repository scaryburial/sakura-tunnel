# 发布与 CI

> 本仓库**故意不包含 `.github/workflows/`**：作者的 PAT 缺 `workflow` 权限，推送工作流会被 GitHub 拒绝，而且失败记录会污染历史导致后续 push 全部失败。
> 需要自动化时，在网页上手动新建这两个文件（或用有 workflow 权限的 token）。

## 1. 打 tag 自动构建 + 发布

`.github/workflows/release.yml`：

```yaml
name: release
on:
  push:
    tags: ['v*']

permissions:
  contents: write

jobs:
  build:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        include:
          - { goos: linux,   goarch: amd64 }
          - { goos: linux,   goarch: arm64 }
          - { goos: windows, goarch: amd64 }
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27'
      - name: build
        env:
          GOOS: ${{ matrix.goos }}
          GOARCH: ${{ matrix.goarch }}
          CGO_ENABLED: '0'
        run: |
          name="sakura-${GOOS}-${GOARCH}"
          [ "$GOOS" = "windows" ] && name="${name}.exe"
          go build -trimpath -buildvcs=false -tags noweb -ldflags "-s -w" -o "dist/$name" ./cmd/sakura
          cp "dist/$name" "$name"
      - name: upx
        run: |
          sudo apt-get update -qq && sudo apt-get install -y upx-ucl
          for f in dist/*; do
            case "$f" in *-upx*) continue ;; esac
            cp "$f" "$f-upx"
            upx --best --lzma -q "$f-upx"
          done
      - uses: actions/upload-artifact@v4
        with:
          name: binaries-${{ matrix.goos }}-${{ matrix.goarch }}
          path: |
            dist/sakura-*
            dist/*.exe

  release:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/download-artifact@v4
        with:
          pattern: binaries-*
          merge-multiple: true
          path: dist
      - uses: softprops/action-gh-release@v2
        with:
          files: dist/*
          generate_release_notes: true
```

## 2. 每次提交跑测试

`.github/workflows/ci.yml`：

```yaml
name: ci
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27'
      - run: go vet -tags noweb ./pkg/... ./server/... ./client/...
      - run: go test -tags noweb -count=1 ./pkg/... ./server/... ./client/...
```

## 3. 手动发版（没有 CI 也能干）

```bash
bash build/build-release.sh
gh release create v1.0.0 dist/sakura-* --generate-notes   # 需要 gh CLI 登录
# 或网页 Releases → Draft a new release → 上传 dist/ 下的文件
```

**上传到 Release 的文件名必须保持**：

```
sakura-linux-amd64
sakura-linux-arm64
sakura-windows-amd64.exe
```

`install.sh` / `install.ps1` 就是按这个命名拼 URL 的（还会自动回退试 `-upx` 后缀）。

## 4. 发版检查清单

- [ ] `go test -tags noweb ./...` 全绿
- [ ] 三平台二进制 `sakura version` 能跑
- [ ] 装一台干净机器跑 `install.sh`，面板能打开
- [ ] 面板建用户 → 一键接入 → 隧道通
- [ ] 限速/配额实测一遍
- [ ] README 的体积表数字更新
