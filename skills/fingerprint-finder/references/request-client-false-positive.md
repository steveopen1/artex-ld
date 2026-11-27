# 请求客户端误报参考（响应 400 不等于接口不可用）

> **本平台适配说明**：原文基于 该客户端 的 `http_fuzzer`。本平台没有 该客户端，
> 等价的是 `fetch` 工具与 Bash 里的 curl / raw socket。**下面要传达的方法论
> 不变**：某个客户端对同一路径返回 400 时，不要立刻判定"接口不可用"，
> 必须换一种传输方式二次确认，否则会漏掉真实存在的漏洞。
> 文中所有 `mcp__该客户端__http_fuzzer` 已统一替换为"平台 fetch 工具"。



> **所有需要发探测请求的 skill 必须读这份文档。**
>
> 这是实战中总结的"客户端误报"风险：不同 HTTP 客户端发送的请求格式在某些 nginx 配置下不一致，会导致**真实存在的漏洞被误判为"接口拒绝服务"**。

## 核心问题

| 工具 | 响应 | 真实情况 |
| --- | --- | --- |
| 平台 fetch 工具 | 400 Bad Request | ❌ 客户端请求格式问题（缺 Connection: close / CL 计算错） |
| Burp Suite | 200 OK | ✅ 服务端真实响应 |
| raw socket (Python) | 200 OK | ✅ 服务端真实响应 |

**案例**：锡惠停 `/charging/platform/test/langshine/query_token` 漏洞
- fetch 工具给 400 → 误判"接口拒绝"
- raw socket 给 200 → 实际拿到 access_token（CRITICAL 漏洞）

## 触发条件

满足以下**任意 2 条**即应怀疑是客户端误报：

1. fetch 工具给 400 Bad Request（generic nginx 错误页，Content-Length: 166）
2. 目标服务是 HTTPS + nginx
3. 请求 body 简短（≤ 50 字节）
4. 之前同一工具给过有效响应但现在突然 400
5. Burp / 浏览器可正常访问同一路径

## 鉴别 SOP（raw socket fallback）

任何 skill 检测到 400 时，**必须**调用下面的 raw socket 二次确认：

```python
import socket, ssl, gzip

def raw_socket_probe(host: str, path: str, method: str = "POST",
                     headers: dict = None, body: bytes = b"{}",
                     port: int = 443, timeout: int = 10) -> tuple[int, bytes]:
    """
    raw socket 复现请求，绕过 客户端请求格式问题。
    返回 (status_code, raw_response_body)。
    """
    headers = headers or {}
    headers.setdefault("Host", host)
    headers.setdefault("Connection", "close")
    headers.setdefault("Content-Length", str(len(body)))
    headers.setdefault("Content-Type", "application/json")

    header_lines = "\r\n".join(f"{k}: {v}" for k, v in headers.items())
    request = (
        f"{method} {path} HTTP/1.1\r\n"
        f"{header_lines}\r\n"
        f"\r\n"
    ).encode() + body

    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE

    with socket.create_connection((host, port), timeout=timeout) as s:
        with ctx.wrap_socket(s, server_hostname=host) as ss:
            ss.send(request)
            chunks = []
            while True:
                chunk = ss.recv(4096)
                if not chunk:
                    break
                chunks.append(chunk)
    raw = b"".join(chunks)

    # 解析 status code
    status_line = raw.split(b"\r\n", 1)[0].decode("latin1")
    status_code = int(status_line.split()[1]) if status_line else 0
    return status_code, raw


def decompress_gzip(raw_response: bytes) -> str:
    """解压 chunked + gzip 响应"""
    if b"\r\n\r\n" not in raw_response:
        return ""
    header_part, body = raw_response.split(b"\r\n\r\n", 1)

    # 解 chunked
    if b"Transfer-Encoding: chunked" in header_part:
        decoded = b""
        buf = body
        while buf:
            line_end = buf.find(b"\r\n")
            if line_end == -1:
                break
            size_str = buf[:line_end].strip()
            try:
                size = int(size_str, 16)
            except ValueError:
                break
            if size == 0:
                break
            decoded += buf[line_end + 2 : line_end + 2 + size]
            buf = buf[line_end + 2 + size + 2 :]
        body = decoded

    # 解 gzip
    if b"Content-Encoding: gzip" in header_part:
        try:
            body = gzip.decompress(body)
        except Exception:
            pass
    return body.decode("utf-8", errors="replace")
```

**调用示例**：

```python
status, raw = raw_socket_probe(
    host="api.xihuiting.com",
    path="/charging/platform/test/langshine/query_token",
    method="POST",
    headers={
        "Host": "api.xihuiting.com",
        "User-Agent": "Mozilla/5.0 ...",
        "Content-Type": "application/json",
        "Accept-Encoding": "gzip, deflate",
    },
    body=b"{}",
)
if status == 200:
    body = decompress_gzip(raw)
    # body 是真实 JSON 响应
elif status == 400:
    # 真正拒绝 —— 不是客户端误报
    pass
```

## 决策树

```
fetch 工具 返回 400
    │
    ├─ Burp / raw socket 也给 400 → 服务端真拒绝 → 标记"端点拒绝服务"
    │
    ├─ Burp / raw socket 给 200/404 → 客户端请求格式问题
    │     │
    │     └─ 用 raw socket 拿真实响应继续测试
    │
    └─ Burp / raw socket 给其他 4xx → 服务端有应用层校验，该客户端没发对
          │
          └─ 调整 request headers（特别是 Content-Length / Connection）
```

## 各 skill 接入点

| Skill | 在哪些场景触发 fallback |
| --- | --- |
| `poc-scan` | POC payload 返回 400，怀疑 该客户端问题 |
| `sqli-tester` | 注入 payload 返回 400，怀疑 该客户端问题 |
| `api-tester` | 认证矩阵 a-i 测试中端点全 400 |
| `sensitive-info-leak` | 用泄露凭据登录测试时返回 400 |
| `fingerprint-finder` | 指纹识别探测路径返回 400 |

## 各 skill 的强制检查点

| Skill | 必查时机 |
| --- | --- |
| `poc-scan` | 命中 POC 后执行前：raw socket 验证目标可达 |
| `sqli-tester` | 注入 payload 给 400：立即 raw socket 二次确认 |
| `api-tester` | a-i 矩阵任一维度全 400：先 raw socket 排除 该客户端 bug |
| `sensitive-info-leak` | 凭据"测试失败"前：raw socket 二次确认 |
| `fingerprint-finder` | 路径探测 400：raw socket 二次确认 |

## 报告输出规范

发现漏洞时（如 该客户端返回 400 但实际 200）：
- 报告里写明 **"漏洞复现需用 Burp 或 raw socket"**
- 截图里附上 raw socket 真实响应作为证据
- 不要在截图里只放 该客户端 的 400（会被审计质疑）

## 改进方向

- 等待 该客户端 MCP 修复 HTTP 客户端 bug
- 或在 该客户端 配置中显式添加 `Connection: close` header
- 或用 Burp raw bytes 模式（如果 该客户端 支持）

## 反模式

❌ 看到 该客户端返回 400 就放弃，错过 critical 漏洞
❌ 在截图里只放 该客户端 的 400（会被审计质疑"为什么用有 bug 的工具测试"）
❌ 直接在报告里写"接口拒绝服务 / 不可用"，未二次确认
❌ 反复用 该客户端 测试同一个端点，期望某次能拿到正确响应
❌ 跳过 raw socket fallback，认为是浪费时间

## 一句话铁律

> **fetch 工具 给 400 + raw socket 给 200 = 真实 critical 漏洞，必须用 raw socket 重新取证。**
>
> 任何情况下，看到 该客户端返回 400 时，**第一步**不是改 payload，而是 **raw socket 复现**。