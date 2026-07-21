package domain

import (
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// HostFromTarget returns the hostname (no port) from a target URL.
func HostFromTarget(target string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid target: %q", target)
	}
	return strings.ToLower(parsed.Hostname()), nil
}

// RootDomain returns the registrable domain (eTLD+1), e.g. aaa.dell.com → dell.com.
func RootDomain(host string) (string, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", fmt.Errorf("empty host")
	}
	// IP addresses have no public suffix; use as-is.
	if strings.Count(host, ".") == 3 {
		// crude IPv4 check
		parts := strings.Split(host, ".")
		allNum := true
		for _, p := range parts {
			if p == "" {
				allNum = false
				break
			}
			for _, c := range p {
				if c < '0' || c > '9' {
					allNum = false
					break
				}
			}
		}
		if allNum {
			return host, nil
		}
	}

	root, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		// fallback: last two labels
		parts := strings.Split(host, ".")
		if len(parts) >= 2 {
			return strings.Join(parts[len(parts)-2:], "."), nil
		}
		return host, nil
	}
	return strings.ToLower(root), nil
}

// HostOfURL extracts hostname from a URL string.
func HostOfURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}
