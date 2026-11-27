# 自研系统 API 测试矩阵（强制）

> 来源：指纹识别方法论「二、什么指纹 → 1、自研系统 a–i」。
> **触发条件**：`fingerprint-finder` 判定 `system_type=自研`，或目标无 CMS/通用组件指纹、认证流高度业务化。
> **职责边界**：
> - 指纹识别 / 信号采集 → `fingerprint-finder`（[auth-fingerprint.md](../../fingerprint-finder/references/auth-fingerprint.md)）
> - **可执行测试 / fuzz / 凭据组合 / 签名伪造** → 本文件（api-tester）
> - OAuth2 / SSO 专项测试执行 → `07-oauth2-bypass.md` / `08-sso-bypass.md`（fingerprint-finder 识别后回本 skill 做接口级验证）
>
> ⚠️ **禁止**：识别出自研后只做端点枚举或 JSON fuzz，跳过本矩阵 a–i。

---

## 0. 调度总览

```
fingerprint-finder 判定自研
        │
        ▼
api-tester 加载本矩阵，按优先级执行：
  P0  d 注册拿 token  +  e 一切可拿 token 的接口
  P0  a 接口未授权（去 Cookie 复测全量接口）
  P1  c 路由守卫绕过（直打后端 API）
  P1  b 前端硬编码（凭证 / 调试开关 / 内网）
  P1  f 权限提升（垂直越权 / role 参数）
  P1  h 鉴权字段绕过（异常请求头 / Cookie 身份字段）
  P1  i 签名字段逆向伪造
  P2  g 认证结构（SSO / OAuth2 密钥与方式）→ 专项文档 + 本 skill 验证
        │
        ▼
拿到任意凭据 / 绕过成功 → 立即组合测试所有已发现接口（见 04-test-scenarios.md §7）
```

**工具硬性要求**（本平台 适配）：
- 请求测试优先用平台 `fetch` 工具（要完整回包时）或流量录制回看（`traffic_search`/`traffic_get`）
- 平台流量录制在任务启动时已自动挂载，无需手动起 MITM 代理
- JS 加解密复刻：用浏览器 MCP 取 JS，或从已录制流量（`traffic_get`）里捞出 JS 后本地解析
- 编解码：用 Python / 工具目录里的 `jq`、`gson` 等（见 `tool-arsenal` skill）

---

## a. 接口未授权

### 目标
验证业务 / 管理接口是否在**无 Cookie、无 Authorization、无自定义鉴权头**时仍返回有效数据。

### SOP
```
1. 从 平台 traffic_search / traffic_get / JS 分析得到接口清单（含登录后才触发的）
2. 对每个接口构造「裸请求」：
   - 删除 Cookie
   - 删除 Authorization / X-Token / token 查询参数
   - 删除自定义鉴权头（X-User-Id、X-Auth-* 等，见 h）
3. 平台 fetch 工具 批量重放，标记：
   - 200 + 业务 JSON / HTML 数据 → 未授权访问（高危）
   - 200 + 空列表 / 通用错误结构但仍泄露字段名 → 信息泄露（中）
   - 401/403/302 登录页 → 已鉴权
4. 对未授权接口继续：
   - 水平：改 id / orderId / userId 遍历
   - 垂直：/api/admin/*、/manage/*、/console/*
   - 批量：{1..N} 或 pageSize 放大
```

### 判定与输出
| 结果 | 严重度 | 记录字段 |
|------|--------|----------|
| 无鉴权返回他人/全量业务数据 | 高危/严重 | endpoint, method, sample_id, response_snippet |
| 无鉴权仅返回当前会话无关的公开配置 | 低/中 | endpoint, data_class |

### Payload 骨架
```http
GET /api/user/profile HTTP/1.1
Host: target
# 故意不带 Cookie / Authorization

GET /api/order/detail?id=1001 HTTP/1.1
Host: target

GET /api/admin/list HTTP/1.1
Host: target
```

---

## b. 前端硬编码

### 目标
从 HTML/JS/配置资源提取可直接利用的账号、密钥、调试开关、内网地址，并**验证**可用性（不只是报告字符串）。

### SOP
```
1. 拉取全部 JS/HTML（含 sourcemap、.env*、config.js、settings.json）
2. 关键词检索（配合 sensitive-info-leak）：
   admin/123456, demo/demo, test/test
   password, secret, apiKey, token, ak/sk
   isDebug, DEBUG, VUE_APP_*, process.env
   13800138000, test@, @internal
   http://192.168, http://10., http://172.16
3. 分类验证：
   - 账号密码 → 走登录接口（业务逻辑弱口令字典，禁止无脑公网爆破）
   - API Key / Secret → 带上请求业务接口
   - isDebug=true → 找 debug 路由 / mock 开关 / 日志接口
   - 内网 URL → 记录为跳板线索，不直接当外网 POC
4. 命中有效凭据 → 立即进入组合测试
```

### 注意
- 硬编码命中 ≠ 漏洞；**登录成功 / 接口接受 key** 才记为 finding
- 与 `sensitive-info-leak` 交叉去重，本 skill 负责「用起来测接口」

---

## c. 路由守卫绕过

### 目标
区分「仅前端路由鉴权」与「后端接口鉴权」。前端 `beforeEach` / `meta.requiresAuth` 挡不住直接打 API。

### SOP
```
1. 浏览器访问需登录页面（/admin、/dashboard、/manage）
   - 被踢回登录页 → 记录前端守卫存在
2. 从 Network / JS 提取该页实际调用的 API 列表
3. 不经过前端路由，直接：
   平台工具 / http_fuzzer 请求这些 API
   （先带登录态确认正常响应形态，再去掉鉴权对比 → 衔接 a）
4. 常见绕过姿势：
   - 直打 /api/* 忽略 /admin SPA 路由
   - 历史路径 /#/admin 与 /admin 混用
   - 移动端 / H5 同源另一套无守卫入口
5. 若 API 仍 401 → 后端有鉴权；若 200 → 路由守卫形同虚设（高危）
```

### 与 a 的关系
- **c** 解决「怎么找到被前端藏起来的 API」
- **a** 解决「这些 API 是否真鉴权」
- 必须串联：c 发现 → a 验证 → 有 token 后 f/h 提升

---

## d. 注册功能（获取 token）⚠️ P0

### 目标
开放注册是自研系统**最高优先级**合法凭据来源（优于登录爆破）。

### SOP
```
1. 发现注册入口：
   /api/register /signup /user/add /account/create
   页面「注册/开通/试用」按钮 → 抓包
2. 分析必填参数；测试可删除字段（验证码、inviteCode、companyId）
3. ⚠️ 注册体注入权限字段（与 f 合并，0 值必测）：
   role/permission/level/user_type/is_admin/is_super/group_id
   值：0,1,2,3,admin,super,owner,true,false
4. 验证码绕过：
   - 删参数 / 空值 / 固定 000000
   - response-modifier 改 verified=true
   - 短信/邮箱验证码回显（见 e）
5. 注册成功 → 登录拿 token → 立即全量接口组合测试
6. 双号注册 → 为水平越权准备 userA / userB
```

### 输出
```yaml
register_url: /api/v1/register
register_success: true
token: eyJ...
role_injected: 0   # 若注入成功
next: combine_all_apis
```

---

## e. 一切可以获取 token 的接口 ⚠️ P0

> 不限于登录/注册。凡能颁发 session / JWT / access_token / ticket 的接口都要扫。

### 接口类型清单

| 类型 | 路径特征 | 测试要点 |
|------|----------|----------|
| 登录 | /login /signin /oauth/token | 弱口令按业务构造；空密；SQL 特征交 sqli-tester |
| 注册 | 见 d | P0 |
| 刷新 | /refresh /token/refresh | 过期 refresh 重放；窃取的 refresh 换 access |
| 游客/匿名 token | /guest /anonymous /visitor/token | 权限是否等同正式用户 |
| 短信/邮箱登录 | /sms/login /email/login | 验证码回显、爆破限制、固定码 |
| 找回/重置密码 | /forgot /resetPassword /retrieve | reset token 是否可猜/可换 userid |
| 第三方回调 | /callback /oauth/callback /sso/acs | 见 g；code 重放 |
| 切换租户/身份 | /switchTenant /impersonate /sudo | 普通用户调是否提权 |
| 开发/调试发 token | /dev/token /test/login /mock/auth | 生产残留 |
| 微信/小程序 | code2session / wx/login | openid 绑定劫持、unionid 替换 |

### SOP（统一）
```
1. JS + 流量双向枚举「响应里出现 token/accessToken/Authorization 的接口」
2. 每个接口记录：请求参数、响应 token 字段名、有效期、绑定的 user 标识
3. 优先无害获取（注册/游客）；其次逻辑缺陷（重置 token、回调 userid）
4. 任一 token 到手 → 组合测试；多 token 对比权限差（普通 vs 游客 vs 管理）
```

### 重置密码专项（高频高危）
```
1. 触发重置，截获 resetToken / 链接中的 key
2. 替换 userid / email / mobile 为受害者标识
3. 不校验旧密码直接 setPassword
4. host 头 / 回调链接投毒（若邮件链接依赖 Host）
```

---

## f. 权限提升（垂直越权）接口

### 目标
普通凭据访问管理功能，或通过参数把自身 role 抬升。

### SOP
```
1. 入口发现：
   - JS 中 /admin /manage /console /sys /permission
   - 菜单配置 JSON（routes、permissions 数组）
   - 响应中的 roles: [] 与按钮 v-if 权限码
2. 垂直访问：
   普通 token → 重放管理接口（GET/POST/DELETE 全方法）
3. 参数提权（注册/改资料/审批流）：
   见 04-test-scenarios.md §8；role=0 必测
4. 隐式提权：
   - tenantId / orgId 改为平台级
   - deptId=0、parentId=0
   - dataScope=all
5. 成功标准：
   - 管理接口 200 且返回管理数据
   - 或自身 profile.role 变为 admin 且新 token 权限变高
```

### Fuzz 字段快查
```
role, roles, permission, permissions, level, userType, user_type,
isAdmin, is_admin, isSuper, is_super, authLevel, groupId, group_id,
rank, dataScope, tenantId, orgId, deptId, adminFlag
```

---

## g. 认证结构（SSO / OAuth2）密钥与方式

### 目标
识别认证协议与密钥暴露面后，在 **API 层完成可复现验证**（细节矩阵见 fingerprint 专项文档）。

### 识别后强制动作

| 识别到 | 立即执行 |
|--------|----------|
| OAuth2 | 读 `07-oauth2-bypass.md`（本 skill 本地）；验证 client_secret / callback userid / passwd / code 重放 |
| SSO/CAS/SAML | 读 `08-sso-bypass.md`（本 skill 本地）；验证加密 userid 置空、callback、cookie 复制 |
| JWT | alg=none、空签名、弱密钥爆破（exec_codec / 字典）、kid 注入、role 声明篡改 |
| 微信 | code 重放、openid 绑定接口、unionid 越权 |

### API 层最小验证集
```
1. token 端点：client_id + 错误 client_secret / 空 secret
2. callback：改 userid / 置空 / 重放 code
3. 资源端点：只改 access_token 来源（Web token 调小程序 API 等跨端）
4. 密钥：JS 泄露的 secret 换正式 token
```

> 完整向量表不重复维护；**本文件要求「识别 → 专项文档 → 在本 skill 出 HTTP 证据」闭环**。

---

## h. 鉴权字段绕过（异常请求头 / 身份字段）

### 目标
服务端信任客户端提交的「身份声明字段」（明文或加密），通过替换/置空/重加密实现水平/垂直越权。

### 识别信号
```
请求头：
  X-User-Id, X-UserId, X-Uid, X-Account, X-Role, X-Tenant-Id,
  X-Auth-User, X-Admin, X-Org-Id, X-Emp-No, X-Real-User
Cookie：
  userid=, uid=, userId=, account=, role=, is_admin=,
  以及 AES/Base64 长串但键名仍像身份字段（u, _u, _token_user）
Body/Query：
  userId, user_id, loginUser, currentUser, empId, staffId
```

### SOP（四步）
```
1. 正常登录 A，从 MITM 流量列出所有非常规鉴权字段（对比未登录请求差集）
2. 判定编码：
   - 明文数字/账号 → 直接替换
   - Base64/Hex → exec_codec 解码看是否 userid
   - AES/DES/SM4 → JS 定位 key/iv/mode，evaluate_script 复刻加解密
3. 攻击向量（每个字段都跑）：
   - 替换为 B 的 userid（水平）
   - 替换为 admin / 1 / 0（垂直/默认账号）
   - 置空、null、删除该头/Cookie
   - 数组化或 JSON 嵌套（衔接 02-json-fuzz）
   - 仅改字段不改 session → 看以谁为准
4. 加密字段：明文改 B → 前端同算法加密 → 写回 Cookie/头 → 重放
```

### 判定
- 响应数据归属 B / admin → 鉴权字段伪造成功（严重）
- 置空后落到默认系统账号 → 与 SSO 置空同类高危
- 仅 401 → 字段可能是辅助，非唯一凭据；仍记录「存在身份头」供链式利用

---

## i. 签名字段逆向伪造 ⚠️ 自研高发

### 目标
请求中的 `sign` / `signature` / `msg_signature` 等若算法与密钥可在前端还原，则可**任意重放与篡改业务参数**。

### 识别信号
```
参数名：sign, signature, signData, msg_signature, sig, hash, mac
请求头：X-Sign, X-Signature, X-Ca-Signature, X-Hmac-Signature
JS 关键字：
  md5(, sha1(, sha256(, hmac, HmacSHA, CryptoJS, JSEncrypt
  sign(, getSign(, makeSign(, createSignature(
  secret, appSecret, appKey, salt, iv, key
常见拼串：
  key排序 + secret、timestamp+nonce+body、method+path+body
```

### SOP
```
1. 抓一条带 sign 的完整请求，固定全部参数
2. JS 定位签名函数（断点 / evaluate_script Hook）：
   - 输入：哪些字段进入签名、是否含 body 原始字节
   - 密钥：硬编码 / 配置下发 / 登录后下发
   - 算法：MD5/SHA1/SHA256/HMAC/RSA-SHA256/SM3
3. 本地复现：相同输入必须得到相同 sign（复现失败则未找全字段）
4. 绕过与利用矩阵：
   a. 删除 sign / 空 sign / sign=undefined
   b. 错误 sign（测是否仅前端校验）
   c. 重放旧 sign + 旧参数（无 timestamp 校验）
   d. 重放旧 sign + 改业务参数（测 sign 覆盖范围）
   e. 改 timestamp 为未来/过去（窗口过大）
   f. 已知 secret → 任意参数重签名（完整伪造）
   g. RSA 验签若公钥当密钥用 / 算法降级
5. 伪造成功后：改 userid/amount/role 再签名 → 接 a/f/h 放大危害
```

### 最小复现模板（记录到 findings）
```yaml
endpoint: POST /api/order/create
sign_field: sign
algo: MD5
sign_string: "amount={amount}&userId={userId}&key={secret}"
secret_source: static/js/main.js#line_or_symbol
bypass:
  drop_sign: fail
  empty_sign: fail
  re_sign_with_secret: success
forged_example:
  original_amount: 100
  forged_amount: 0.01
  forged_sign: "..."
```

### 与场景 6 的关系
`04-test-scenarios.md` 场景 6 是单例；**本节是自研矩阵强制项**，要求对**所有**带签名的业务接口建立「能否伪签」结论，不能只分析一个 demo 接口。

---

## 组合优先级（自研专用）

拿到任一突破后立即执行，不要等矩阵跑完：

| 突破类型 | 立刻做什么 |
|----------|------------|
| 注册/游客 token | 全接口扫描 + f 垂直 + a 对比有无鉴权差 |
| 未授权接口 | id 遍历 + 敏感字段提取 + 写操作试探（谨慎） |
| 硬编码账号 | 登录后同 token 组合测试 |
| 鉴权字段伪造 | 批量换 userid 导出数据 |
| 伪签能力 | 改关键业务参数（金额/归属/权限）批量重放 |
| OAuth/SSO 绕过 | 换绑受害者身份后组合测试 |

---

## 自研矩阵执行 Checklist（写入 work-space）

路径建议：`work-space/[目标]/findings/self-dev-auth-matrix.md`

```markdown
# 自研认证测试矩阵 - [目标] - [日期]

## 判定来源
- fingerprint: system_type=自研
- 信号: ...

## 进度
- [ ] a 接口未授权
- [ ] b 前端硬编码验证
- [ ] c 路由守卫绕过
- [ ] d 注册获取 token
- [ ] e 其他发 token 接口
- [ ] f 垂直越权 / role
- [ ] g SSO/OAuth2/JWT（若存在）
- [ ] h 鉴权字段绕过
- [ ] i 签名逆向伪造
- [ ] 凭据组合测试（全接口）

## Findings 摘要
| ID | 向量 | 接口 | 严重度 | 证据文件 |
|----|------|------|--------|----------|
|    |      |      |        |          |

## 凭据库存
| 类型 | 值/位置 | 权限 | 来源向量 |
|------|---------|------|----------|
```

---



## Agent 调度 Prompt 模板

```
目标已判定为自研系统。按 api-tester/references/06-self-dev-auth-matrix.md 执行：
1. P0：注册/发 token 接口（d/e）→ 拿到 token
2. P0：全量接口去鉴权复测（a）+ 路由直打（c）
3. P1：硬编码验证（b）、role 提权（f）、鉴权字段（h）、签名伪造（i）
4. 若存在 OAuth2/SSO：读对应 bypass 文档并在本 skill 产出 HTTP 证据（g）
5. 任一突破立即组合测试；每完成一项更新 checklist 与 Task
6. 输出 self-dev-auth-matrix.md + 漏洞证据到 work-space/[目标]/
工具：平台 fetch 工具 + 平台流量录制 + 浏览器 MCP，禁止只扫描不验证。
```
