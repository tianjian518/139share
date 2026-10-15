# share

云盘分享链接转 WebDAV / strm 的小工具。

## 运行

```sh
docker run -d \
  -p 12395:12395 \
  -v $(pwd)/data:/app/data \
  -v $(pwd)/media_dir:/app/media_dir \
  --restart unless-stopped \
  IMAGE
```

打开 `http://服务器IP:12395`。

## 配置

首次打开进入初始化页面，登录后在「系统设置 → 云盘凭据」中填写自己的账号与授权信息即可。

无需配置环境变量。
