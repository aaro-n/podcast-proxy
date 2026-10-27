package main

import (
	"net"
	"os"
	"testing"
)

// TestCheckEgressIP 验证出站 IP 判定
func TestCheckEgressIP(t *testing.T) {
	cases := []struct {
		ip           string
		allowPrivate bool
		wantAllowed  bool
	}{
		{"8.8.8.8", false, true},
		{"1.1.1.1", false, true},
		{"127.0.0.1", false, false},
		{"169.254.169.254", false, false}, // 云元数据
		{"10.0.0.1", false, false},
		{"172.16.0.1", false, false},
		{"192.168.1.1", false, false},
		{"100.64.0.1", false, false}, // CGNAT
		{"0.0.0.0", false, false},
		{"224.0.0.1", false, false},
		{"::1", false, false},
		{"::ffff:127.0.0.1", false, false}, // IPv4-mapped IPv6
		{"fd00::1", false, false},
		{"fe80::1", false, false},

		// 显式允许内网：仅放行私网，元数据/回环仍拦截
		{"192.168.1.1", true, true},
		{"10.0.0.1", true, true},
		{"169.254.169.254", true, false},
		{"127.0.0.1", true, false},
	}

	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("无法解析测试 IP: %s", c.ip)
		}
		err := checkEgressIP(ip, c.allowPrivate)
		allowed := err == nil
		if allowed != c.wantAllowed {
			t.Errorf("checkEgressIP(%s, allowPrivate=%v) = %v, 期望 allowed=%v",
				c.ip, c.allowPrivate, err, c.wantAllowed)
		}
	}
}

// TestEgressControl 验证端口白名单与整体开关
func TestEgressControl(t *testing.T) {
	os.Setenv("PODCAST_PROXY_APIKEY", "test-key")
	os.Setenv("EGRESS_GUARD", "true")
	os.Setenv("EGRESS_ALLOW_PRIVATE", "false")
	os.Setenv("EGRESS_ALLOWED_PORTS", "80,443")
	if err := InitConfig(); err != nil {
		t.Fatalf("配置初始化失败: %v", err)
	}

	cases := []struct {
		addr        string
		wantAllowed bool
	}{
		{"8.8.8.8:443", true},
		{"8.8.8.8:80", true},
		{"8.8.8.8:8080", false}, // 非白名单端口
		{"127.0.0.1:6379", false},
		{"169.254.169.254:80", false},
		{"192.168.1.1:443", false},
	}

	for _, c := range cases {
		err := egressControl("tcp", c.addr, nil)
		allowed := err == nil
		if allowed != c.wantAllowed {
			t.Errorf("egressControl(tcp, %s) = %v, 期望 allowed=%v", c.addr, err, c.wantAllowed)
		}
	}

	// 非 TCP 网络不校验
	if err := egressControl("unix", "/tmp/x.sock", nil); err != nil {
		t.Errorf("非 TCP 网络不应被拦截: %v", err)
	}
}
