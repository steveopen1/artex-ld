---
name: "fingerprint-finder"
description: "Web指纹识别工具，用于识别目标网站使用的框架、CMS、技术栈、组件和认证结构（OAuth2/SSO/JWT）。覆盖自研系统/二开系统/通用系统三类差异化识别策略。⚠️ 与主动探针（平台 fetch / 流量录制 / 浏览器 MCP）并行使用、穿插调用；skill 提供指纹 SOP 和系统分类 A/B/C/D/E 判定，探针提供主动验证和经验加载；禁止只调其一，禁止串行等待。当用户需要对网站进行指纹识别、技术侦察、识别认证结构或深挖注册/越权入口时调用此技能。"
---

# Web 指纹识别技能

> ## ⚠️ 必读：本 skill 必须与主动探针并行使用
>
> **本 skill 定位**：提供指纹识别 SOP + 系统分类 A/B/C/D/E 判定 + 分类证据校验（≥3 独立证据）
> **探针定位**：主动探测 / 经验加载 / 浏览器取样
> **并行模式**（✅ 正确）：同轮回复同时调 `Skill('fingerprint-finder')` + `fetch`（平台请求工具）+ `traffic_search`（回看已录制流量）
> **串行反模式**（❌ 错误）：等本 skill 跑完再发探针 = 浪费时间
> **伪并行反模式**（❌ 错误）：调完本 skill 不主动发探针 = 覆盖率不足
>
> **⚠️ 探测误报陷阱**：指纹探测路径返回 400 时**不要直接放弃**，必须换一种方式二次确认
> （[参考文档](references/request-client-false-positive.md)）。路径探测可能因客户端差异全部返回
> 400，导致系统分类 A/B/C/D/E 误判。
>
> 详细配比原则见 `references/system-category-routing.md`（分类编排路由）。
>
> **本平台适配说明**：原文基于 Claude Code + yakit MCP。本平台没有 yakit，
> 等价能力为：`fetch`（发请求看回包）、`traffic_search`/`traffic_get`（回看录制过的流量，
> 避免重复请求）、`mcp__browser__*`（浏览器取样）。方法论、指纹库与分类判定部分未改动。

此技能帮助你全面识别目标网站的技术栈，包括框架、CMS、组件和版本信息。支持多种指纹识别方法和深度探测技术。以及识别waf的品牌,根据waf策略进行测试。

**本 skill 的核心任务**：不是写一份"指纹报告"，而是为后续测试指明方向。

**最重要的事情**：识别完成后必须回答三个问题：
1. **这是自研 / 二开 / 通用系统？**（系统形态，决定测试策略）
2. **推荐哪个测试策略类别（A/B/C/D/E】？**（详见 [references/system-categories.md §2](references/system-categories.md#2-系统形态--测试策略映射aberde)）

   ⚠️ **A类极易误判**：A类=第三方商业产品（用友/致远/泛微/Jenkins等），**不是"有CVE的自研系统"**。
   - "memory有CVE记录" → ❌ 不能反推A类
   - "自研+商业组件嵌入" → 应判B类（双线并行）
   - "系统建设方自研" → 按自研/二开判定，不是A类
   - 系统形态=自研/二开，即使命中CVE也走B/C类

3. **版本是多少？**（决定打哪个 CVE/联网搜索方向）
4. **下一步调哪个 skill？**（api-tester / poc-scan / sqli-tester）
5. 用到了哪些组件？
6. **推荐走哪个场景？**（输出 recommended_scenario，详见 [references/system-categories.md §2.5](references/system-categories.md#25-测试策略--推荐场景映射)）

---

## 三类系统识别思维（核心决策树）（必须强制走完！！！）

⚠️ **识别完成后必须走对应分支，不是停在报告**。每类系统的后续路径完全不同：

```
目标指纹识别完成
        ↓
┌───────────────────────────────────────────────┐
│ ④ WAF 识别？（与系统类型并行）               │
│  - 阿里云 / 玄武盾 / 腾讯云 / Cloudflare 等│
│  → 标记 WAF 类型 → poc-scan + WAF 适配     │
└───────────────────────────────────────────────┘
        ↓
┌───────────────────────────────────────────────┐
│ ① 自研系统？                                   │
│  - 无 meta generator / JS 高度业务化           │
│  - API 路径完全自定义                          │
│  → 调度 api-tester 执行 a-i 认证矩阵           │
│  → 重点：注册拿 token / 鉴权字段绕过 / 签名伪造 │
└───────────────────────────────────────────────┘
        ↓ 否
┌───────────────────────────────────────────────┐
│ ② 二开系统？                                   │
│  - 有框架指纹 + 组件关联路径                   │
│  - JeecgBoot / RuoYi / 人人 / JEPaaS / CAS 等│
│  → 关联路径探测 → poc-scan → 互联网搜索兜底   │
│  → 重点：Druid/Swagger/ureport 未授权         │
│         + Shiro rememberMe 反序列化             │
└───────────────────────────────────────────────┘
        ↓ 否
┌───────────────────────────────────────────────┐
│ ③ 通用系统？                                   │
│  - 有 meta generator / CMS 标识 / 版本号       │
│  - 用友 / 致远 / 泛微 / WordPress / Shiro 等  │
│  → poc-scan 直接打 → 互联网搜索兜底            │
│  → 重点：已知 CVE / 默认口令 / 未授权          │
└───────────────────────────────────────────────┘
        ↓
┌───────────────────────────────────────────────┐
│ ⑤ 子域名探测？（贯穿全程）                    │
│  - 主站加固 → 子域名往往裸奔                  │
│  → /js/ 中提取子域名                         │
│  → crt.sh 被动枚举                           │
│  → 精简字典主动爆破（m/api/admin/mobile等）   │
└───────────────────────────────────────────────┘
```

---

### ① 自研系统识别 Checklist

```
[ ] 无独立 meta generator
[ ] JS 完全业务化（无通用库名）
[ ] API 路径完全自定义（/api/v1/xxx 而非 /api/auth）
[ ] 管理后台路径自定义
满足 ≥3 项 → 自研系统
```

### ② 二开系统识别 Checklist

```
[ ] 有框架指纹（Spring Boot / ThinkPHP / Vue 等）
[ ] 存在关联组件路径（/druid、/ureport、/actuator）
[ ] Shiro rememberMe Cookie 共存
[ ] 框架版本号过期（如 Spring Boot 2.0.x）
[ ] 默认账号可能仍可登录（admin/123456）
满足 ≥2 项 → 二开系统
```

### ③ 通用系统识别 Checklist

```
[ ] HTML 有 meta generator（如 WordPress 5.8）
[ ] 有明确 CMS / OA 标识
[ ] 默认管理后台路径保留（/wp-admin/、/dede/）
[ ] 版本号可直接读出
满足 ≥1 项 → 通用系统
```

---

## 二开系统识别模式（高频目标）

**国内政务/教育/医疗 80%+ 系统都是二开**。识别重点不是"表面特征"，而是**底层组件关联路径**。

**识别思维链**：
```
① 先找 1 个已知路径（/actuator、/login、/druid）
         ↓
② 反推同类系统的其他默认组件
         ↓
③ 查 poc-scan INDEX 定位对应 POC 目录
         ↓
④ poc-scan 打 POC（命中则直接利用） -> 后续由 poc-scan 执行互联网搜索补充
         ↓
⑤ poc-scan 0 命中 → 后续由 poc-scan 执行互联网兜底搜索
```

**常见二开系统识别模式（示例）**：

| 看到 | 判定 | 立即做 |
|---|---|---|
| `/login` + `renren` + `rememberMe` Cookie | **人人权限框架** | `/admin`、`/generator` → 调 poc-scan |
| `/login` + `ry-ui` + `/captchaImage` | **若依 RuoYi** | `/druid`、`/swagger-ui` → 调 poc-scan |
| `/jeecg-boot/` + `Ant Design Vue` | **JeecgBoot** | `/jmreport/list`、`/qurest/` → 调 poc-scan |
| `JEPaaS` + `j_spring_security_check` | **JEPaaS 低代码** | `/document/`、`/module/` → 调 poc-scan |
| 302 重定向到 `/login?service=xxx` | **CAS 单点登录** | `/serviceValidate` → 调 poc-scan |
| `/s/` + 短码 + 302 重定向 | **短链接系统** | 枚举短码、`/admin` → api-tester 手动测试 |
| `/form/` + JSON POST 含 `fieldName` | **表单系统** | `/form/list`、`/form/export` → api-tester 手动测 |
| `/actuator/` + `Whitelabel Error Page` | **Spring Boot** | `/actuator/env`、`/heapdump` → 调 poc-scan |
| `app="jeecg-boot"` / `Ant Design Vue` | **Spring Boot + JeecgBoot** | 同上，版本探测优先 |

---

## 通用系统识别模式（商业 OA / 组件 / CMS）->（示例）

| 看到 | 判定 | 立即做 |
|---|---|---|
| `meta generator` + WordPress/DedeCMS | **开源 CMS** | 调 poc-scan |
| `/wp-admin/`、`/dede/` | **CMS 管理后台** | 调 poc-scan |
| `yongyou` / `NCCloud` | **用友 NC** | 调 poc-scan |
| `seeyon` / `A8` | **致远 OA** | 调 poc-scan |
| `weaver` / `ecology` | **泛微 OA** | 调 poc-scan |
| `rememberMe` Cookie | **Apache Shiro** | 调 poc-scan（Shiro 反序列化） |
| `struts2` / `S2-` | **Apache Struts2** | 调 poc-scan |
| `WebLogic` / `/console/` | **WebLogic** | 调 poc-scan |
| `fastjson` / JSON Content-Type | **Fastjson** | 调 poc-scan |
| `CVE-xxxx-xxxxx` | **给定 CVE** | 调 poc-scan --cve |


③ 查 poc-scan INDEX 定位对应 POC 目录
         ↓
④ poc-scan 打 POC（命中则直接利用） -> 后续由 poc-scan 执行互联网搜索补充
         ↓
⑤ poc-scan 0 命中 → 后续由 poc-scan 执行互联网兜底搜索

---

## 核心功能

- **主动指纹识别**：直接探测目标特征
- **被动指纹识别**：从公开资源收集信息
- **框架指纹识别**：识别各类 Web 框架
- **组件指纹识别**：识别前端组件、库和插件
- **深度指纹发现**：通过 JS 和接口进行二次开发系统探测
- **目录 Fuzz**：基于框架特征的目录探测
- **JS 加解密指纹识别**：识别 RSA / AES / HMAC / OAuth2 / SSO / JWT 等
- **小程序 / APP 关键字识别**
- **认证结构指纹识别**：自研系统的接口未授权 / 前端硬编码 / 路由守卫 / 注册功能 / token 接口 / 权限提升 / 鉴权字段绕过 / **签名字段逆向伪造**
- **OAuth2 绕过指纹**：client_secret / userid / passwd 三类攻击面
- **SSO 绕过指纹**：加密字段置空 / 跨域跳转 / callback 越权 / cookie 复制
- **自研 → api-tester handoff**：判定自研后必须调度 `api-tester` 的 [06-self-dev-auth-matrix.md](../api-tester/references/06-self-dev-auth-matrix.md) 完成 a–i 验证（禁止只识别不测试）
- **后端语言与操作系统判断**： 根据目标系统，组件特征，系统特性，等综合判断目标站点后端语言构成+目标服务器的操作系统(windows/linux/other)

## 指纹识别方法论

> ⚠️ **防误报前置**：任何"信号"（路径 / 响应头 / 前端框架 / UI 组件 / banner / 品牌名）都只是**候选假设**，不是结论。定后端语言必须有"芯信号"（错误回显 / 异常文本 / JWT 报错 / 错误页字节 / 凭据行为差），否则结论只能写"推测"。完整分级与反证 SOP 见 [stack-evidence-grading.md](references/stack-evidence-grading.md)。

### 1. 主动指纹识别

#### 1.1 HTTP 响应头指纹

**关键响应头**：
```
X-Powered-By          - 技术栈信息
Server               - Web 服务器类型
X-Generator          - CMS 标识
X-Drupal-Cache       - Drupal CMS
X-ASP.NET-Version   - ASP.NET 版本
X-ThinkPHP-Version   - ThinkPHP 版本
```

**常见框架响应头**：
- **ThinkPHP**：`X-Powered-By: ThinkPHP`
- **Django**：`Server: gunicorn`, `X-Frame-Options: DENY`
- **Spring**：`X-Application-Context`
- **Express**：`X-Powered-By: Express`
- **Laravel**：`Set-Cookie: laravel_session`

#### 1.2 路径指纹

**常见管理后台**：
```
/admin/
/manage/
/backend/
/wp-admin/          - WordPress
/dashboard/
/console/
/control/
```

**常见框架路径**：
```
/phpmyadmin/        - phpMyAdmin
/admin.php          - 通用后台
/manager/          - Tomcat
/axis2/             - Apache Axis2
/console/           - WebLogic, Spring Boot
/actuator/          - Spring Boot Actuator
/ureport/           - 报表系统
/datasource/        - 数据源配置
/api-docs/          - Swagger API 文档
/swagger-ui/        - Swagger UI
/v2/api-docs/       - Springfox Swagger
```

#### 1.3 HTML 源码指纹

**关键特征**：
```html
<!-- CMS 标识 -->
<meta name="generator" content="WordPress 5.8">
<meta name="generator" content="DedeCMS">

<!-- 技术特征 -->
<link rel="stylesheet" href="/themes/xxx">
<link rel="stylesheet" href="/static/css/xxx">
```

**框架特征**：
```html
<!-- Vue -->
<div id="app">

<!-- React -->
<div id="root">

<!-- jQuery -->
<script src="/jquery-xxx.min.js">
```

### 2. 被动指纹识别

#### 2.1 DNS 记录分析

**DNS 历史记录**：
- 查看 DNS 历史记录
- MX 记录分析
- TXT 记录中的服务标识

**CDN 指纹**：
- Cloudflare: `CF-Ray`, `__cfduid`
- Akamai: `Akamai-Ghost`, `Akamai-Origin-Hop`
- 阿里云: `Alibaba Cloud`
- 腾讯云: `Tencent Cloud`

#### 2.2 SSL/TLS 证书

**证书信息**：
- 证书中的组织信息
- 证书序列号
- 证书指纹（SHA1/SHA256）

**工具**：
- `censys.io` - SSL 证书搜索引擎
- `crt.sh` - 证书搜索
- `shodan.io` - IoT 搜索引擎

#### 2.3 第三方服务指纹

**追踪代码**：Google Analytics、百度统计、CNZZ、51LA

**社交插件**：微信分享、QQ 互联、微博分享

### 3. 深度指纹发现（重点）

⚠️ **核心原则**：基于框架特征进行目录 Fuzz，尤其适用于二次开发系统。

**问题说明**：
- 二次开发的系统往往会弱化原始系统的指纹特征
- 管理员可能修改了默认的 logo、标题等表面特征
- 但底层框架代码和目录结构往往保持不变

**发现策略**：
```
看见 /datasource → 尝试 /ureport
看见 /admin → 尝试 /ureport, /datasource, /js, /api
看见特定接口 → 反推可能的其他接口
```

**框架关联目录字典**：
```
Spring Boot:
/actuator/ → /actuator/health, /actuator/env, /actuator/beans
/datasource → /ureport, /druid, /webconsole

ThinkPHP:
/datasource → /ureport, /phpmyadmin, /admin

通用:
/api → /api-docs, /swagger-ui, /v2/api-docs
/datasource → /ureport, /druid, /webconsole
```

**JS 指纹挖掘**：提取 JS 文件，搜索框架名称、版本号、API 端点、配置信息（baseUrl, apiKey 等），推断隐藏路径。

**接口指纹挖掘**：分析现有 API 模式，推断 /api/admin、/api/config 等路径，尝试 /api/login、/api/upload 等常见端点。

详细策略参见 [references/deep-discovery.md](references/deep-discovery.md)。

### 4. JS 指纹识别 → js-fingerprint.md（AI 跑核心）

**触发条件**：已经从 HTML/响应头/Packet 中获取到 JS 资源 URL。

详见 [references/js-fingerprint.md](references/js-fingerprint.md)：
- **JS 加解密关键字识别**（RSA / AES / HMAC / OAuth2 / SSO / JWT）→ 测试入口速查
- **反编译后文件目录 prompt**：遍历所有 .js 提取 API 端点 + 硬编码凭证
- **识别结果 → 测试动作的强制映射表**：识别出 OAuth2 必须追加弱口令测，识别出 AES 必须用前端 key 重加密，**禁止仅停留在识别**

### 5. 系统类别视角 → system-categories.md（必做的第一步）

⚠️ **核心原则**：所有指纹识别完成后，第一时间回答："这是 **自研系统** / **二开系统** / **通用系统**？"
不同类别对应完全不同的识别策略与测试入口。

**二开系统是最高频目标**：国内政务/教育/医疗 80%+ 的系统都是二开——即在 Spring Boot / ThinkPHP / JeecgBoot / RuoYi / 人人框架 / CAS 等基础上二次开发。**识别重点不是"表面特征"，而是"底层组件关联路径"**。

**二开系统识别思维链**：
```
① 先找 1 个已知路径（/actuator、/login、/druid）
         ↓
② 反推同类系统的其他默认组件（ureport、swagger-ui、jmreport）
         ↓
③ 探测 Shiro rememberMe Cookie（人人/RuoYi 高频共存）
         ↓
④ 查 poc-scan INDEX 定位对应 POC 目录
         ↓
⑤ 按 ROI 打 POC（未授权 > SQL注入 > RCE）
```

**常见二开系统识别模式**：

| 看到 | 判定 | 立即做 |
|---|---|---|
| `/login` + `renren` + `rememberMe` Cookie | **人人权限框架** | `/admin`、`/generator` → poc-scan 打 Shiro 反序列化 |
| `/login` + `ry-ui` + `/captchaImage` | **若依 RuoYi** | `/druid`、`/swagger-ui` → poc-scan 打 CVE-2023-49371 |
| `/jeecg-boot/` + `Ant Design Vue` | **JeecgBoot** | `/jmreport/list`、`/qurest/` → poc-scan 打 CVE-2023-1454 |
| `JEPaaS` + `j_spring_security_check` | **JEPaaS 低代码** | `/document/`、`/module/` → poc-scan 打文件上传 RCE |
| 302 重定向到 `/login?service=xxx` | **CAS 单点登录** | `/serviceValidate` → poc-scan 打票据伪造 |
| `/s/` + 短码 + 302 重定向 | **短链接系统** | 枚举短码、`/admin` → 手动测试 |
| `/form/` + JSON POST 含 `fieldName` | **表单系统** | `/form/list`、`/form/export` → 手动测未授权/XSS |

| 类别 | 测试入口偏好 | 详见 |
|---|---|---|
| **自研系统** | 鉴权结构 a–i / 注册·发 token / 鉴权字段 / **签名伪造** → 交 `api-tester` 矩阵验证 | [auth-fingerprint.md](references/auth-fingerprint.md) + [api-tester 06 矩阵](../api-tester/references/06-self-dev-auth-matrix.md) |
| **二开系统** | 框架组件关联路径 + 版本探测 + 历史 nday + **Shiro rememberMe 高危** | [deep-discovery.md](references/deep-discovery.md) + [system-categories.md](references/system-categories.md) |
| **通用系统** | nday / 0day 直接打（调 `poc-scan` 系列） | [system-categories.md §4](references/system-categories.md#4-通用系统--nday--0day) |

详见 [references/system-categories.md](references/system-categories.md)：
- 三类系统识别 Checklist（自研 / 二开 / 通用）
- 二开框架速查（JeecgBoot / RuoYi / Guns / iBase4J / FastAdmin / Discuz / WordPress）
- 通用系统直接调 `poc-scan` 系列打 nday（本地约 2.5 万文件 POC 语料，跨源未去重）
- 系统类别采集模板

### 6. OAuth2 / SSO 绕过指纹（认证指纹的延伸）

JS 中识别出对应关键字后立即跳转：

| 识别到 | 跳转 |
|---|---|
| `OAuth` / `OAuth2` / `access_token` / `client_secret` | [references/oauth2-fingerprint.md](references/oauth2-fingerprint.md) → 识别后交 `api-tester` 验证 |
| `SSO` / `CAS` / `saml` / `SAML` | [references/sso-fingerprint.md](references/sso-fingerprint.md) → 识别后交 `api-tester` 验证 |

详细分类见对应文档。

## 测试流程(举例:)

### 1. 信息收集阶段
- 获取 HTTP 响应头（Server, X-Powered-By, 版本）
- 分析 HTML 源码（meta generator, CSS/JS 路径, 版权信息）
- 被动信息收集（DNS, SSL 证书, 历史记录）

### 2. 主动探测阶段(举例:)
- 路径探测（管理后台、框架特征路径、API 端点）
- 深度 Fuzz（基于已知路径和框架关联的 Fuzz, JS 挖掘）
- 组件探测（数据库工具、服务器组件、中间件）

### 3. 指纹分析阶段(举例:)
- 特征匹配（对比指纹库、匹配框架和组件特征）
- 版本识别（从响应头、错误页面、JS 文件提取版本）
- 关联分析（分析框架与组件关系、推断系统架构）

## 输出格式

### 1. 基础信息(举例:)
```
目标：https://example.com
IP 地址：1.2.3.4
服务器：nginx/1.18.0
操作系统：Linux
```

### 2. 框架识别(举例:)
```
主框架：Spring Boot 2.4.0
前端框架：Vue 2.6.12
UI 组件：Element UI 2.15.0
构建工具：Webpack
```

### 3. 组件识别(举例:)
```
Web 服务器：nginx 1.18.0
应用服务器：Apache Tomcat 9.0.38
数据库：MySQL 8.0.21
缓存：Redis 6.0.9
```

### 4. 指纹特征(举例:)
```
[+] 发现管理后台：/admin, /manage
[+] 发现 API 文档：/swagger-ui.html, /v2/api-docs
[+] 发现监控端点：/actuator/health, /actuator/env
[+] 发现配置页面：/druid/index.html
[+] 发现调试功能：/actuator/heapdump
```

### 5. 深度发现(举例:)
```
[+] 基于 /datasource 发现：
    - /ureport/
    - /phpmyadmin/
    - /druid/

[+] 基于 /actuator 发现：
    - /actuator/heapdump (可下载堆转储)
    - /actuator/env (可获取环境变量)
    - /actuator/beans (可获取 Bean 列表)

[+] 基于 JS 分析发现：
    - API 端点：/api/v1
    - 隐藏路径：/admin/backup
    - 配置泄露：baseUrl 配置
```

## 参考文档

### 基础层（既有）
- [框架特征指纹库](references/framework-signatures.md) - Java/Python/PHP/JS 各框架详细特征
- [深度指纹发现](references/deep-discovery.md) - JS 挖掘、接口分析、Fuzz 策略、二开组件关联字典
- [指纹库建设](references/fingerprint-library.md) - CMS、框架、组件指纹库表格

### 认证结构层（本轮新增）
- [**认证结构指纹库**](references/auth-fingerprint.md) - 自研系统专项：接口未授权 / 前端硬编码 / 路由守卫 / 注册功能 / token 接口 / 权限提升 / SSO OAuth2 / 鉴权字段绕过 / **签名字段逆向伪造 (i)**
- [**api-tester 自研测试矩阵**](../api-tester/references/06-self-dev-auth-matrix.md) - 判定自研后的强制验证 SOP（识别在本 skill，利用在 api-tester）
- [**OAuth2 指纹识别**](references/oauth2-fingerprint.md) - OAuth2 识别信号采集（验证归 api-tester）
- [**SSO 指纹识别**](references/sso-fingerprint.md) - SSO 识别信号采集（验证归 api-tester）

### 方法论层（本轮新增，从 SKILL.md 抽出）
- [**JS 指纹识别**](references/js-fingerprint.md) - JS 加解密关键字识别 + 小程序关键字命中后的路由指引 + 识别结果 → 测试动作映射表
- [**系统类别视角**](references/system-categories.md) - 自研 / 二开 / 通用三类系统差异化识别 + 二开框架速查 + POC 库联动
- [**定栈证据分级与反证 SOP**](references/stack-evidence-grading.md) - **防指纹误报**：壳信号 / 芯信号分级 + 后端语言判定规则 + 常见误报映射 + 可靠定栈信号清单

> ⚠️ **调用顺序**：先看方法论层（js-fingerprint / system-categories）确定识别策略 → 再用基础层（framework-signatures / deep-discovery）做细节探测 → 最后用认证结构层（auth-fingerprint / oauth2-bypass / sso-bypass）打认证侧。

## 最佳实践

- **系统性**：按照测试流程逐步探测
- **全面性**：不遗漏任何可能的指纹
- **深度性**：尤其注意二次开发系统的深层指纹
- **关联性**：利用已知特征发现更多特征
- **隐蔽性**：控制请求频率，避免被检测

## 注意事项

⚠️ **重要警告**：
- 不要对目标系统造成破坏性影响

## 工具集成

可配合以下工具使用：
- **fetch**：平台请求工具，发探针请求并查看回包（替代原文的 yakit http_fuzzer）
- **traffic_search / traffic_get**：回看已录制的流量，避免重复打同一个 URL
- **浏览器 MCP**：`mcp__browser__*`，用于前端 JS 取样（替代原文的 js-reverse-mcp）
- **工具目录**：若管理员配置了外部工具库（见 `tool-arsenal` skill），可直接调用
  `wafw00f` / `cdncheck` 等做 WAF 判定
- **Python**：自定义注入脚本
