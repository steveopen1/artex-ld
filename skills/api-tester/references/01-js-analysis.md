# JS 逆向分析详细说明

## 1. 前端资源分析

### 1.1 提取 JS 文件
- **内联 JS**：HTML 源码中的 `<script>` 标签
- **外链 JS**：`<script src="...">` 引用的独立文件
- **动态加载**：通过代码动态加载的 JS 模块

### 1.2 分析方法
1. 查看页面源码，搜索所有 JS 引用
2. 使用 Chrome DevTools Network 面板过滤 JS 文件
3. 分析 webpack 打包的模块（查找 `webpackJsonp`、`__webpack_require__`）
4. 查找动态加载的 JS（`import()`、`require()`）

## 2. 参数提取技术

### 2.1 API 端点搜索
```
搜索模式：
- /api/
- /v1/, /v2/, /v3/
- /graphql
- baseUrl
- endpoint
- apiUrl
```

### 2.2 参数来源
- **全局变量**：`window.config`、`window.API_CONFIG`
- **LocalStorage/SessionStorage**：存储的 token、userId
- **Cookie**：认证相关的 cookie
- **URL 参数**：路由参数、查询参数

### 2.3 加密参数识别
- 签名算法：`sign()`、`md5()`、`sha256()`
- 加密函数：`encrypt()`、`AES`、`RSA`
- 密钥提取：硬编码密钥、变量中的密钥

## 3. 逆向技术

### 3.1 反混淆技术
- **Deverools de4js**：在线 JS 反混淆
- **Chrome DevTools**：格式化、美化代码
- **动态调试**：设置断点、跟踪调用栈

### 3.2 Hook 技术
```javascript
// Hook fetch
const originalFetch = window.fetch;
window.fetch = function(...args) {
  console.log('Fetch:', args);
  return originalFetch.apply(this, args);
};

// Hook XMLHttpRequest
const originalXHR = window.XMLHttpRequest.prototype.open;
window.XMLHttpRequest.prototype.open = function(...args) {
  console.log('XHR:', args);
  return originalXHR.apply(this, args);
};
```

### 3.3 webpack 模块分析
1. 查找 `webpackJsonp` 调用
2. 分析模块 ID 和依赖关系
3. 提取异步加载的 chunk

## 4. 常见框架配置

### 4.1 Vue
```javascript
// Vue.config.js
// API 配置
axios.defaults.baseURL = '/api'

// 拦截器
axios.interceptors.request.use(config => {
  config.headers['X-Token'] = localStorage.getItem('token')
  return config
})
```

### 4.2 React
```javascript
// React 项目常见配置
// API 基础路径
const API_BASE = 'https://api.example.com'

// 请求配置
fetch(API_BASE + '/users', {
  headers: { 'Authorization': 'Bearer ' + token }
})
```

### 4.3 Angular
```javascript
// Angular HTTP 拦截器
@Injectable()
export class AuthInterceptor implements HttpInterceptor {
  intercept(req: HttpRequest<any>, next: HttpHandler) {
    const token = localStorage.getItem('token')
    const authReq = req.clone({
      headers: req.headers.set('Authorization', 'Bearer ' + token)
    })
    return next.handle(authReq)
  }
}
```

## 5. 关键搜索正则

```regex
# API 端点
/(api\/[a-zA-Z0-9_\/]+)/g
/(v[0-9]+\/[a-zA-Z0-9_\/]+)/g

# Token/Key
/(token|apiKey|secret|password)[":\s]+["']?([a-zA-Z0-9_\-\.]+)["']?/gi

# URL
/https?:\/\/[a-zA-Z0-9\._\-\/]+/g

# 加密函数
/(encrypt|decrypt|sign|encode|decode)\s*\(/gi
```

## 6. 实用工具

- **Chrome DevTools**：反混淆、调试、Hook 函数
- **浏览器 MCP**：`mcp__browser__*`（本平台 已接入，用于前端 JS 取样）
