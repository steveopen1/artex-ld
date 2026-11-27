package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"

	"artex/proxyauth"
)

// dynamicForwardProxy is a credential-free, stable local proxy. Each request
// resolves the current upstream, so pool switches do not rebuild agents or MCP.
type dynamicForwardProxy struct {
	ln      net.Listener
	addr    string
	current func() string
	direct  func() bool
	server  *http.Server
	once    sync.Once
	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	// secret 用于签发/校验任务代理凭证（见 proxyauth 包）。
	// 进程级随机生成：代理地址本身也是启动时才知道的，不需要持久化。
	secret []byte
	// gate 是任务级范围闸门：给定任务与目标主机，返回非空字符串表示拒绝原因。
	// nil = 不做判定（未配置）。由 Server 注入 —— 代理是 Manager 级单例，
	// 拿不到 Server 的方法集，只能靠回调。
	gate func(taskID int64, host string) (string, bool)
}

func newDynamicForwardProxy(current func() string) (*dynamicForwardProxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		cancel()
		_ = ln.Close()
		return nil, err
	}
	p := &dynamicForwardProxy{ln: ln, addr: "http://" + ln.Addr().String(), current: current, direct: func() bool { return true }, conns: map[net.Conn]struct{}{}, ctx: ctx, cancel: cancel, secret: secret}
	p.server = &http.Server{Handler: http.HandlerFunc(p.serveHTTP), BaseContext: func(net.Listener) context.Context { return ctx }}
	go p.server.Serve(ln)
	return p, nil
}

func (p *dynamicForwardProxy) Addr() string {
	if p == nil {
		return ""
	}
	return p.addr
}
func (p *dynamicForwardProxy) Close() error {
	if p == nil {
		return nil
	}
	p.once.Do(func() {
		p.cancel()
		p.mu.Lock()
		for c := range p.conns {
			_ = c.Close()
		}
		p.conns = nil
		p.mu.Unlock()
		_ = p.server.Close()
	})
	return nil
}

func (p *dynamicForwardProxy) track(c net.Conn) func() {
	if c == nil {
		return func() {}
	}
	p.mu.Lock()
	if p.conns != nil {
		p.conns[c] = struct{}{}
	}
	p.mu.Unlock()
	return func() { p.mu.Lock(); delete(p.conns, c); p.mu.Unlock() }
}

// URLForTask 返回带任务凭证的代理地址，供该任务的 agent 通过 HTTP_PROXY 使用。
// 无凭证的流量仍然放行（UI/自动化/未归属场景），但只有带合法凭证的流量才会
// 被按任务范围判定 —— 见 checkScope 的注释说明这个边界。
func (p *dynamicForwardProxy) URLForTask(taskID int64) string {
	if p == nil {
		return ""
	}
	if taskID <= 0 {
		return p.addr
	}
	u, err := url.Parse(p.addr)
	if err != nil {
		return p.addr
	}
	user, pass := proxyauth.UserAndPass(p.secret, taskID)
	u.User = url.UserPassword(user, pass)
	return u.String()
}

// checkScope 解出任务凭证并按该任务范围判定目标主机。
//
// 返回值 (原因, 是否拒绝)。
//
// 边界说明（重要）：凭证缺失时**放行**。原因是同一个代理还被 UI 的请求重发、
// 未绑定任务的对话等复用，把这些一律拒掉会直接打断正常功能。代价是：一个
// 刻意在 Bash 里 unset HTTP_PROXY 的 agent 能绕过这一层 —— 但那样它也就失去了
// 代理（录制、出口池）能力，且文本层的 Guard 仍然会拦它的命令行。两层都不完美，
// 叠加才把"随手 curl 打到范围外"这条最常见的路径堵死。
func (p *dynamicForwardProxy) checkScope(r *http.Request, host string) (string, bool) {
	if p == nil || p.gate == nil || host == "" {
		return "", false
	}
	taskID, token, ok := proxyauth.Parse(r.Header.Get("Proxy-Authorization"))
	if !ok {
		return "", false // 无归属流量：放行（见方法注释的边界说明）
	}
	if !proxyauth.Verify(p.secret, taskID, token) {
		return "代理凭证无效", true
	}
	if reason, blocked := p.gate(taskID, host); blocked {
		return reason, true
	}
	return "", false
}

func (p *dynamicForwardProxy) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.serveConnect(w, r)
		return
	}
	up := strings.TrimSpace(p.current())
	if up == "" && p.direct != nil && !p.direct() {
		http.Error(w, "代理不可用", 502)
		return
	}
	tr := &http.Transport{Proxy: nil}
	if up != "" {
		u, err := url.Parse(up)
		if err != nil {
			http.Error(w, "代理不可用", 502)
			return
		}
		if strings.HasPrefix(strings.ToLower(u.Scheme), "socks5") {
			d, err := socksDialer(u)
			if err != nil {
				http.Error(w, "代理不可用", 502)
				return
			}
			tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialProxyContext(ctx, d, network, addr)
			}
		} else {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	defer tr.CloseIdleConnections()
	rr := r.Clone(r.Context())
	if rr.URL.IsAbs() == false {
		rr.URL.Scheme = "http"
		rr.URL.Host = r.Host
	}
	rr.RequestURI = ""
	if reason, blocked := p.checkScope(r, rr.URL.Hostname()); blocked {
		http.Error(w, reason, http.StatusForbidden)
		return
	}
	proxyauth.StripHeader(rr.Header)
	resp, err := tr.RoundTrip(rr)
	if err != nil {
		http.Error(w, "代理不可用", 502)
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *dynamicForwardProxy) serveConnect(w http.ResponseWriter, r *http.Request) {
	// CONNECT 是最关键的一条：HTTPS 流量全部走隧道，隧道一旦建立，代理就再也
	// 看不到里面访问了什么，所以必须在这里按 r.Host 判定。
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if reason, blocked := p.checkScope(r, host); blocked {
		http.Error(w, reason, http.StatusForbidden)
		return
	}
	up := strings.TrimSpace(p.current())
	if up == "" && p.direct != nil && !p.direct() {
		http.Error(w, "代理不可用", 502)
		return
	}
	target := r.Host
	var conn net.Conn
	var err error
	if up == "" {
		conn, err = (&net.Dialer{Timeout: 10 * time.Second}).DialContext(r.Context(), "tcp", target)
	} else {
		u, e := url.Parse(up)
		if e != nil {
			err = e
		} else if strings.HasPrefix(strings.ToLower(u.Scheme), "socks5") {
			d, e := socksDialer(u)
			if e != nil {
				err = e
			} else {
				conn, err = dialProxyContext(r.Context(), d, "tcp", target)
			}
		} else {
			conn, err = dialHTTPConnect(r.Context(), u, target)
		}
	}
	if err != nil {
		http.Error(w, "代理不可用", 502)
		return
	}
	h, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "代理不可用", 500)
		return
	}
	client, rw, err := h.Hijack()
	if err != nil {
		return
	}
	removeClient := p.track(client)
	defer removeClient()
	removeUpstream := p.track(conn)
	defer removeUpstream()
	defer client.Close()
	defer conn.Close()
	_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	_ = rw.Flush()
	clientReader := io.Reader(client)
	if rw.Reader != nil && rw.Reader.Buffered() > 0 {
		clientReader = io.MultiReader(rw.Reader, client)
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(conn, clientReader); _ = conn.Close(); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, conn); _ = client.Close(); done <- struct{}{} }()
	<-done
}

func socksDialer(u *url.URL) (proxy.Dialer, error) {
	var auth *proxy.Auth
	if u.User != nil {
		pw, _ := u.User.Password()
		auth = &proxy.Auth{User: u.User.Username(), Password: pw}
	}
	return proxy.SOCKS5("tcp", u.Host, auth, &net.Dialer{Timeout: 10 * time.Second})
}

func dialProxyContext(ctx context.Context, d proxy.Dialer, network, addr string) (net.Conn, error) {
	if cd, ok := d.(proxy.ContextDialer); ok {
		return cd.DialContext(ctx, network, addr)
	}
	type result struct {
		c   net.Conn
		err error
	}
	ch := make(chan result, 1)
	go func() { c, err := d.Dial(network, addr); ch <- result{c, err} }()
	select {
	case r := <-ch:
		return r.c, r.err
	case <-ctx.Done():
		go func() {
			r := <-ch
			if r.c != nil {
				_ = r.c.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func dialHTTPConnect(ctx context.Context, u *url.URL, target string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second}
	addr := u.Host
	if !strings.Contains(addr, ":") {
		addr += ":80"
	}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(u.Scheme, "https") {
		conn = tls.Client(conn, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
		if err := conn.(*tls.Conn).HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	br := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	fmt.Fprintf(br, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target)
	if u.User != nil {
		req := &http.Request{Header: http.Header{}}
		req.SetBasicAuth(u.User.Username(), func() string { p, _ := u.User.Password(); return p }())
		fmt.Fprintf(br, "Proxy-Authorization: %s\r\n", req.Header.Get("Authorization"))
	}
	fmt.Fprint(br, "\r\n")
	if err := br.Flush(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	type responseResult struct {
		resp *http.Response
		err  error
	}
	readDone := make(chan responseResult, 1)
	go func() {
		resp, err := http.ReadResponse(br.Reader, &http.Request{Method: http.MethodConnect})
		readDone <- responseResult{resp: resp, err: err}
	}()
	var rr responseResult
	select {
	case rr = <-readDone:
	case <-readCtx.Done():
		_ = conn.Close()
		<-readDone
		return nil, readCtx.Err()
	}
	resp, err := rr.resp, rr.err
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		_ = conn.Close()
		return nil, fmt.Errorf("upstream CONNECT: %s", resp.Status)
	}
	return &bufferedConn{Conn: conn, reader: br.Reader}, nil
}
