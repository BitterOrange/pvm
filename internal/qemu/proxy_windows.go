//go:build windows

package qemu

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// systemProxy reads the static proxy from the Internet settings, which Go
// does not use on its own. ProxyServer is host:port or a per-scheme list
// such as "http=host:port;https=host:port". pac reports a proxy script.
func systemProxy() (server string, pac bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	if v, _, err := k.GetStringValue("AutoConfigURL"); err == nil && v != "" {
		pac = true
	}
	enabled, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return "", pac
	}
	list, _, err := k.GetStringValue("ProxyServer")
	if err != nil || list == "" {
		return "", pac
	}
	if !strings.Contains(list, "=") {
		return list, pac
	}
	for _, scheme := range []string{"https=", "http="} {
		for _, part := range strings.Split(list, ";") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(part), scheme); ok && v != "" {
				return v, pac
			}
		}
	}
	return "", pac
}
