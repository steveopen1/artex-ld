# response-modifier 运行说明

本目录是 `response-modifier` skill 的运行组件：基于 mitmproxy 的响应体改写 addon。

## 组成

| 文件 | 作用 |
|---|---|
| `SKILL.md` | 方法论：先分析后修改的原则、登录状态伪造/验证码劫持/密码重置等模板、输出格式 |
| `response_modifier.py` | mitmproxy addon：按规则改写响应体中的字段 |
| `start_proxy.py` | 启动器：自动挑一个空闲端口起 mitmproxy 并加载规则 |
| `rules_example.json` | 规则文件示例（复制后改成 `rules.json` 使用） |

## 与 本平台 平台的关系

**平台本身不含响应体改写能力**：`intercept` 规则只做 allow / deny / ask 三种放行决策。
所以改写必须靠本目录的 mitmproxy addon。

平台流量录制（`traffic_search` / `traffic_get`）用的是**另一套代理**——任务启动时挂载的那个。
两者可以串联：

```
浏览器 → mitmproxy(本目录，:8090 等) → 平台流量录制代理 → 目标服务器
```

串联后既能在响应里改字段，又能在平台里回看到完整的改后流量。

## 用法

```bash
# 1) 依赖
pip install mitmproxy

# 2) 准备规则
cp rules_example.json rules.json
# 编辑 rules.json

# 3) 起代理（脚本会在 8090-8099 里挑一个没被占用的）
python3 start_proxy.py
```

启动后把浏览器（或目标请求）的 HTTP 代理指向打印出来的端口即可。

## 规则格式

```json
{
  "rules": [
    {
      "name": "规则名",
      "url_pattern": "/api/(login|signin)",   // 正则，匹配 URL 路径
      "method": "POST",                       // 可选，限定 HTTP 方法
      "actions": [
        { "field": "code", "value": 0 },              // 顶层字段
        { "field": "data.token", "value": "xxx" }    // 嵌套字段（点号路径）
      ]
    }
  ]
}
```

规则只应作用于**授权目标**。改写响应绕过认证属于高危操作，
在本平台上应配合任务的 `set_constraints` 声明明确的授权范围。

## 注意

- `__pycache__/` 是 Windows 下生成的字节码缓存，不要提交。
- 端口选择排除了 1080 / 8080 / 8083 / 7897 / 7890 / 888（常见占用）。
