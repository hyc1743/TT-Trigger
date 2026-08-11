# TT-Trigger Windows本地服务

本压缩包只包含localhost/Tailscale本地服务。Chrome插件和Python示例脚本需要从同一版本的独立发布文件下载。

## 启动

1. 完整解压本压缩包。
2. 运行 `start.bat`，首次运行会生成 `config.json`。
3. 另行下载并安装 `TT-Trigger-Chrome-4.0.0.zip`。
4. 打开插件，选择“本地 / Tailscale”。
5. 将 `config.json` 中的 `extension_token` 填入插件并连接。

本地API：

```text
http://127.0.0.1:8788/webhook
```

如果系统已安装并连接Tailscale，启动脚本还会自动监听检测到的Tailscale IPv4。

## 调用

可以使用包内的本地PowerShell示例：

```powershell
.\invoke-trigger.ps1 -Symbol BTC
.\invoke-trigger.ps1 -Symbol "BG-P:SIREN/USDT+OD-S:SIREN/USDT" -AddPair
```

如需Python调用，下载独立的 `invoke-trigger.py`，放入本目录后运行：

```powershell
python3 invoke-trigger.py --symbol BTC
python3 invoke-trigger.py --symbol "BG-P:SIREN/USDT+OD-S:SIREN/USDT" --add-pair
```

Python脚本会自动读取同目录唯一的有效JSON配置。

## 管理脚本

- `start.bat`：启动服务。
- `stop.bat`：停止服务。
- `status.bat`：查看状态。
- `configure.bat`：显示配置说明。
- `manage-keys.bat`：管理本地HMAC调用密钥。
