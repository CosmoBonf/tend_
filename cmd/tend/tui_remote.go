package main

import (
	"net"
	"os/user"
	"strings"
	"sync"
	"time"
)

// The remote shortcut on the status bar: "⇄ cf@10.8.0.2", the ssh target
// another machine reaches this session by, and a click copies the command
// to type there — tend --remote cf@10.8.0.2. The owner reaches tend from a
// laptop over a VPN and wanted the way in written where it is always seen.

// remoteTargetLocked is what the bar says: the target this client came in
// by, when it came in over ssh, or this machine's own.
func (t *tui) remoteTargetLocked() string {
	if t.host != "" {
		return t.host
	}
	return localTarget()
}

var localTargetCache struct {
	sync.Mutex
	at     time.Time
	target string
}

// localTarget is user@address for this machine, looked up again every half
// minute: a VPN comes up after tend does.
func localTarget() string {
	c := &localTargetCache
	c.Lock()
	defer c.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < 30*time.Second {
		return c.target
	}
	c.at = time.Now()
	addr := reachableAddress()
	if addr == "" {
		c.target = ""
		return ""
	}
	name := "user"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	c.target = name + "@" + addr
	return c.target
}

// reachableAddress is the address another machine most likely reaches this
// one at: a VPN's (WireGuard, a tun, Tailscale), since that is the one that
// works from outside, else the first private address on a real interface —
// not a container's bridge, which nothing outside can reach.
func reachableAddress() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var vpn, lan string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		ip := firstIPv4(addrs)
		if ip == "" {
			continue
		}
		switch kind := interfaceKind(ifc.Name); {
		case kind == "vpn" && vpn == "":
			vpn = ip
		case kind == "lan" && lan == "":
			lan = ip
		}
	}
	if vpn != "" {
		return vpn
	}
	return lan
}

// interfaceKind sorts an interface by its name: "vpn", "lan", or "" for
// one no other machine reaches this one through.
func interfaceKind(name string) string {
	for _, p := range []string{"wg", "tun", "utun", "tailscale", "zt", "nordlynx"} {
		if strings.HasPrefix(name, p) {
			return "vpn"
		}
	}
	for _, p := range []string{"docker", "br-", "veth", "virbr", "lxc", "lxd", "vmnet", "vboxnet", "cni", "flannel", "podman"} {
		if strings.HasPrefix(name, p) {
			return ""
		}
	}
	return "lan"
}

func firstIPv4(addrs []net.Addr) string {
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip4 := ipn.IP.To4(); ip4 != nil && !ip4.IsLoopback() && !ip4.IsLinkLocalUnicast() {
			return ip4.String()
		}
	}
	return ""
}

// copyRemoteCommand puts the command to reach this session on the
// clipboard, to be pasted on the other machine.
func (t *tui) copyRemoteCommand() {
	t.mu.Lock()
	target := t.remoteTargetLocked()
	t.mu.Unlock()
	if target == "" {
		return
	}
	cmd := "tend --remote " + target
	if t.session != "" && t.session != "default" {
		cmd += " --session " + t.session
	}
	t.copyToClipboard(cmd, "copied: "+cmd)
}
