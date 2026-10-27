package main

import (
	"context"
	"errors"
	"log"
	"net"
	"strings"
	"syscall"
	"time"
)

// errBlockedEgress 表示目标地址被出站守卫（Egress Guard）拒绝
var errBlockedEgress = errors.New("egress destination blocked")

// blockedEgressCIDRs 禁止访问的网段。
// 仅在 EGRESS_ALLOW_PRIVATE=false（默认）时生效；
// 开启后仍会拦截回环、链路本地（云元数据）和组播地址。
var blockedEgressCIDRs = mustParseCIDRs(
	"0.0.0.0/8",       // 本网络
	"10.0.0.0/8",      // RFC1918
	"100.64.0.0/10",   // CGNAT / 部分云厂商内部网络
	"127.0.0.0/8",     // 回环
	"169.254.0.0/16",  // 链路本地，含云元数据 169.254.169.254
	"172.16.0.0/12",   // RFC1918
	"192.0.0.0/24",    // IETF 协议保留
	"192.0.2.0/24",    // TEST-NET-1
	"192.88.99.0/24",  // 6to4 中继任播
	"192.168.0.0/16",  // RFC1918
	"198.18.0.0/15",   // 基准测试
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"224.0.0.0/4",     // 组播
	"240.0.0.0/4",     // 保留
	"::/128",          // 未指定
	"::1/128",         // 回环
	"fc00::/7",        // 唯一本地地址
	"fe80::/10",       // 链路本地
	"ff00::/8",        // 组播
)

// mustParseCIDRs 预解析 CIDR 列表，避免每次拨号重复解析
func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("invalid CIDR: " + c)
		}
		nets = append(nets, n)
	}
	return nets
}

// egressDialer 全局共享拨号器（net.Dialer 可安全并发使用）
var egressDialer = &net.Dialer{
	Timeout:   10 * time.Second,
	KeepAlive: 30 * time.Second,
	Control:   egressControl,
}

// egressDialContext 供 http.Transport.DialContext 使用
func egressDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return egressDialer.DialContext(ctx, network, address)
}

// egressControl 出站守卫。
//
// net.Dialer.Control 在「DNS 解析完成之后、实际建立连接之前」被调用，
// 因此这里拿到的 host 已经是解析后的真实 IP，天然具备两个好处：
//  1. 防 DNS rebinding：校验的是 IP，不是域名，换解析也绕不过去；
//  2. 覆盖所有重定向：每一跳都要重新拨号，每一跳都会经过这里。
func egressControl(network, address string, _ syscall.RawConn) error {
	cfg := GetConfig()
	if cfg == nil || !cfg.EgressGuard {
		return nil
	}

	// 只校验 TCP（HTTP/HTTPS 都是 TCP）
	if !strings.HasPrefix(network, "tcp") {
		return nil
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		log.Printf("[EGRESS] 拒绝无法解析的目标: %s", address)
		return errBlockedEgress
	}

	if !cfg.EgressAllowedPorts[port] {
		log.Printf("[EGRESS] 拒绝非白名单端口: %s", address)
		return errBlockedEgress
	}

	ip := net.ParseIP(host)
	if ip == nil {
		// Control 在解析之后调用，正常不会进入此分支
		log.Printf("[EGRESS] 拒绝非 IP 目标: %s", address)
		return errBlockedEgress
	}

	if err := checkEgressIP(ip, cfg.EgressAllowPrivate); err != nil {
		log.Printf("[EGRESS] 拒绝内网/保留地址: %s", address)
		return err
	}

	return nil
}

// checkEgressIP 判断解析出的 IP 是否允许访问
func checkEgressIP(ip net.IP, allowPrivate bool) error {
	if ip == nil {
		return errBlockedEgress
	}

	// 无论配置如何，这些地址一律拒绝：
	// 未指定地址、组播、回环、链路本地（云元数据 169.254.169.254 就在这里）
	if ip.IsUnspecified() ||
		ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() {
		return errBlockedEgress
	}

	// 显式允许内网（例如 NAS 上的播客源）时，跳过私网/CIDR 检查
	if allowPrivate {
		return nil
	}

	if ip.IsPrivate() {
		return errBlockedEgress
	}

	// 兼容 IPv4-mapped IPv6（::ffff:127.0.0.1）
	candidates := []net.IP{ip}
	if v4 := ip.To4(); v4 != nil {
		candidates = append(candidates, v4)
	}

	for _, candidate := range candidates {
		for _, n := range blockedEgressCIDRs {
			if n.Contains(candidate) {
				return errBlockedEgress
			}
		}
	}

	return nil
}
