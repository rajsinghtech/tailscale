// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package nmcfg

import (
	"net/netip"
	"slices"
	"testing"

	"tailscale.com/tailcfg"
)

func TestHASubnetRouterAllowedIPs(t *testing.T) {
	// Test setup: Site A (10.100.0.0/16) and Site B (10.200.0.0/16) each have
	// two HA subnet routers. Control pairs them 1:1, so some peers appear
	// without subnet routes in AllowedIPs.
	siteASubnet := netip.MustParsePrefix("10.100.0.0/16")
	siteBSubnet := netip.MustParsePrefix("10.200.0.0/16")

	// IPv6 subnets for dual-stack testing
	siteASubnetV6 := netip.MustParsePrefix("fd00:a::/64")
	siteBSubnetV6 := netip.MustParsePrefix("fd00:b::/64")

	siteASR1Addr := netip.MustParsePrefix("100.64.0.1/32")
	siteASR2Addr := netip.MustParsePrefix("100.64.0.2/32")
	siteBSR1Addr := netip.MustParsePrefix("100.64.0.3/32")
	siteBSR2Addr := netip.MustParsePrefix("100.64.0.4/32")

	siteASR1 := &tailcfg.Node{
		ID:            1,
		Name:          "site-a-sr1",
		Tags:          []string{"tag:site-a-sr"},
		Addresses:     []netip.Prefix{siteASR1Addr},
		AllowedIPs:    []netip.Prefix{siteASR1Addr, siteASubnet},
		PrimaryRoutes: []netip.Prefix{siteASubnet},
	}

	siteASR2 := &tailcfg.Node{
		ID:            2,
		Name:          "site-a-sr2",
		Tags:          []string{"tag:site-a-sr"},
		Addresses:     []netip.Prefix{siteASR2Addr},
		AllowedIPs:    []netip.Prefix{siteASR2Addr, siteASubnet},
		PrimaryRoutes: []netip.Prefix{siteASubnet},
	}

	siteBSR1 := &tailcfg.Node{
		ID:            3,
		Name:          "site-b-sr1",
		Tags:          []string{"tag:site-b-sr"},
		Addresses:     []netip.Prefix{siteBSR1Addr},
		AllowedIPs:    []netip.Prefix{siteBSR1Addr, siteBSubnet},
		PrimaryRoutes: []netip.Prefix{siteBSubnet},
	}

	siteBSR2 := &tailcfg.Node{
		ID:            4,
		Name:          "site-b-sr2",
		Tags:          []string{"tag:site-b-sr"},
		Addresses:     []netip.Prefix{siteBSR2Addr},
		AllowedIPs:    []netip.Prefix{siteBSR2Addr, siteBSubnet},
		PrimaryRoutes: []netip.Prefix{siteBSubnet},
	}

	// Simulate control's HA pairing: Site B SR2 sees Site A SR1 without the subnet
	// (the bug we're fixing)
	siteASR1AsSeenBySiteBSR2 := &tailcfg.Node{
		ID:            1,
		Name:          "site-a-sr1",
		Tags:          []string{"tag:site-a-sr"},
		Addresses:     []netip.Prefix{siteASR1Addr},
		AllowedIPs:    []netip.Prefix{siteASR1Addr}, // Missing siteASubnet due to HA pairing!
		PrimaryRoutes: []netip.Prefix{siteASubnet},
	}

	// Site A SR2 is seen correctly (it's "paired" with Site B SR2)
	siteASR2AsSeenBySiteBSR2 := &tailcfg.Node{
		ID:            2,
		Name:          "site-a-sr2",
		Tags:          []string{"tag:site-a-sr"},
		Addresses:     []netip.Prefix{siteASR2Addr},
		AllowedIPs:    []netip.Prefix{siteASR2Addr, siteASubnet}, // Has subnet because paired
		PrimaryRoutes: []netip.Prefix{siteASubnet},
	}

	// IPv6 dual-stack nodes
	siteASR1V6 := &tailcfg.Node{
		ID:            11,
		Name:          "site-a-sr1-v6",
		Tags:          []string{"tag:site-a-sr-v6"},
		Addresses:     []netip.Prefix{siteASR1Addr},
		AllowedIPs:    []netip.Prefix{siteASR1Addr}, // Missing IPv6 subnet
		PrimaryRoutes: []netip.Prefix{siteASubnetV6},
	}

	siteASR2V6 := &tailcfg.Node{
		ID:            12,
		Name:          "site-a-sr2-v6",
		Tags:          []string{"tag:site-a-sr-v6"},
		Addresses:     []netip.Prefix{siteASR2Addr},
		AllowedIPs:    []netip.Prefix{siteASR2Addr, siteASubnetV6},
		PrimaryRoutes: []netip.Prefix{siteASubnetV6},
	}

	siteBSR2V6 := &tailcfg.Node{
		ID:            14,
		Name:          "site-b-sr2-v6",
		Tags:          []string{"tag:site-b-sr-v6"},
		Addresses:     []netip.Prefix{siteBSR2Addr},
		AllowedIPs:    []netip.Prefix{siteBSR2Addr, siteBSubnetV6},
		PrimaryRoutes: []netip.Prefix{siteBSubnetV6},
	}

	allPeers := []tailcfg.NodeView{
		siteASR1AsSeenBySiteBSR2.View(),
		siteASR2AsSeenBySiteBSR2.View(),
		siteBSR1.View(),
		// siteBSR2 is self, not in peers
	}

	allPeersV6 := []tailcfg.NodeView{
		siteASR1V6.View(),
		siteASR2V6.View(),
	}

	tests := []struct {
		name         string
		selfNode     *tailcfg.Node
		peer         *tailcfg.Node
		allPeers     []tailcfg.NodeView
		wantLen      int
		wantContains []netip.Prefix
	}{
		{
			name:         "site-b-sr2 seeing site-a-sr1 should get site-a subnet from site-a-sr2",
			selfNode:     siteBSR2,
			peer:         siteASR1AsSeenBySiteBSR2,
			allPeers:     allPeers,
			wantLen:      1, // Should add siteASubnet
			wantContains: []netip.Prefix{siteASubnet},
		},
		{
			name:     "site-b-sr2 seeing site-a-sr2 should not add anything (already has subnet)",
			selfNode: siteBSR2,
			peer:     siteASR2AsSeenBySiteBSR2,
			allPeers: allPeers,
			wantLen:  0, // siteASubnet already in AllowedIPs, shouldn't duplicate
		},
		{
			name: "non-subnet-router self should not expand",
			selfNode: &tailcfg.Node{
				ID:         99,
				Addresses:  []netip.Prefix{netip.MustParsePrefix("100.64.0.99/32")},
				AllowedIPs: []netip.Prefix{netip.MustParsePrefix("100.64.0.99/32")}, // Only node address, no subnets
			},
			peer:     siteASR1,
			allPeers: []tailcfg.NodeView{siteASR1.View(), siteASR2.View()},
			wantLen:  0,
		},
		{
			name:     "peer without tags should not expand",
			selfNode: siteBSR2,
			peer: &tailcfg.Node{
				ID:            99,
				Name:          "no-tags-peer",
				Tags:          nil, // No tags
				Addresses:     []netip.Prefix{netip.MustParsePrefix("100.64.0.99/32")},
				AllowedIPs:    []netip.Prefix{netip.MustParsePrefix("100.64.0.99/32")},
				PrimaryRoutes: []netip.Prefix{netip.MustParsePrefix("10.99.0.0/16")},
			},
			allPeers: allPeers,
			wantLen:  0,
		},
		{
			name:     "different tags should not expand",
			selfNode: siteBSR2,
			peer: &tailcfg.Node{
				ID:            99,
				Name:          "other-sr",
				Tags:          []string{"tag:other"},
				Addresses:     []netip.Prefix{netip.MustParsePrefix("100.64.0.99/32")},
				AllowedIPs:    []netip.Prefix{netip.MustParsePrefix("100.64.0.99/32")},
				PrimaryRoutes: []netip.Prefix{netip.MustParsePrefix("10.99.0.0/16")},
			},
			allPeers: allPeers,
			wantLen:  0,
		},
		{
			name:         "ipv6 subnets should expand correctly",
			selfNode:     siteBSR2V6,
			peer:         siteASR1V6,
			allPeers:     allPeersV6,
			wantLen:      1,
			wantContains: []netip.Prefix{siteASubnetV6},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selfView := tt.selfNode.View()
			peerView := tt.peer.View()

			got := haSubnetRouterAllowedIPs(selfView, peerView, tt.allPeers)

			if len(got) != tt.wantLen {
				t.Errorf("haSubnetRouterAllowedIPs() returned %d IPs, want %d; got: %v", len(got), tt.wantLen, got)
			}

			for _, want := range tt.wantContains {
				if !slices.Contains(got, want) {
					t.Errorf("haSubnetRouterAllowedIPs() missing expected IP %v; got: %v", want, got)
				}
			}
		})
	}
}

func TestCidrIsSubnet(t *testing.T) {
	node := &tailcfg.Node{
		Addresses: []netip.Prefix{
			netip.MustParsePrefix("100.64.0.1/32"),
			netip.MustParsePrefix("fd7a:115c:a1e0::1/128"),
		},
	}

	tests := []struct {
		cidr string
		want bool
	}{
		{"0.0.0.0/0", false},     // Default route
		{"::/0", false},          // Default route v6
		{"100.64.0.1/32", false}, // Node's own address
		{"10.100.0.0/16", true},  // Subnet route
		{"192.168.1.0/24", true}, // Subnet route
		{"100.64.0.2/32", true},  // Other node's address (is a subnet from our perspective)
	}

	for _, tt := range tests {
		t.Run(tt.cidr, func(t *testing.T) {
			pfx := netip.MustParsePrefix(tt.cidr)
			got := cidrIsSubnet(node.View(), pfx)
			if got != tt.want {
				t.Errorf("cidrIsSubnet(%s) = %v, want %v", tt.cidr, got, tt.want)
			}
		})
	}
}
